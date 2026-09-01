package recordops

import (
	"context"
	"errors"
	"fmt"
	"sort"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/webisnoteasy/PgBase/internal/query"
	"github.com/webisnoteasy/PgBase/internal/relation"
	"github.com/webisnoteasy/PgBase/internal/schema"
)

type deleteRecordSnapshot struct {
	ref             relation.RecordRef
	collection      schema.Collection
	record          map[string]any
	schemaVersion   int64
	schemaDigest    [32]byte
	previousVersion int64
	previousETag    string
	revisionPresent bool
}

type deleteArtifactIdentity struct {
	eventID   uuid.UUID
	validator []byte
}

// deleteArtifactIdentityCache belongs to one public Delete call, outside the
// transaction retry loop. It retains identities only as allocation state;
// each attempt receives a projection containing its current closure, so stale
// records from a rolled-back attempt can never produce database artifacts.
type deleteArtifactIdentityCache struct {
	rootEventID uuid.UUID
	byRecord    map[relation.RecordRef]deleteArtifactIdentity
}

func newDeleteArtifactIdentityCache(rootEventID uuid.UUID) *deleteArtifactIdentityCache {
	return &deleteArtifactIdentityCache{
		rootEventID: rootEventID,
		byRecord:    make(map[relation.RecordRef]deleteArtifactIdentity),
	}
}

func (s *Service) deleteGraphInTransaction(
	ctx context.Context,
	tx pgx.Tx,
	catalog schema.Catalog,
	rootCollection schema.Collection,
	request DeleteRequest,
	identityCache *deleteArtifactIdentityCache,
	inputHash [32]byte,
	compatibility Compatibility,
) (MutationResult, error) {
	graph, err := relation.BindDeleteGraph(catalog)
	if err != nil {
		return MutationResult{}, normalizeRelationError(err)
	}
	root := relation.RecordRef{CollectionID: rootCollection.ID, RecordID: request.ID}
	plan, err := graph.Plan(ctx, tx, root)
	if err != nil {
		return MutationResult{}, normalizeRelationError(err)
	}
	if err := validateEntityTagSet(request.IfMatch); err != nil {
		return MutationResult{}, err
	}

	deletes := plan.Deletes()
	updates := plan.Updates()
	snapshots, err := captureDeleteSnapshots(ctx, tx, catalog, root, deletes, updates)
	if err != nil {
		return MutationResult{}, err
	}
	rootSnapshot, ok := snapshots[root]
	if !ok {
		return MutationResult{}, schema.ErrConflict
	}
	if request.IfMatch.Present && !request.IfMatch.matches(rootSnapshot.previousETag) {
		return MutationResult{}, &Error{Code: CodePreconditionFailed, CurrentETag: rootSnapshot.previousETag}
	}

	identities, err := identityCache.identitiesFor(s, root, deletes, updates)
	if err != nil {
		return MutationResult{}, err
	}
	if err := plan.ExecuteStorage(ctx); err != nil {
		return MutationResult{}, normalizeRelationError(err)
	}
	if err := touchDeleteGraphUpdates(ctx, tx, snapshots, updates); err != nil {
		return MutationResult{}, err
	}
	updateRefs := make([]relation.RecordRef, len(updates))
	for index, update := range updates {
		updateRefs[index] = update.Source
	}
	afterRecords, err := readDeleteGraphRecords(ctx, tx, catalog, updateRefs)
	if err != nil {
		return MutationResult{}, err
	}

	for _, update := range updates {
		snapshot, ok := snapshots[update.Source]
		if !ok || len(update.ChangedFields) == 0 || !sortedUniqueStrings(update.ChangedFields) {
			return MutationResult{}, schema.ErrConflict
		}
		identity, ok := identities[update.Source]
		if !ok || len(identity.validator) != 32 {
			return MutationResult{}, schema.ErrConflict
		}
		after, ok := afterRecords[update.Source]
		if !ok {
			return MutationResult{}, schema.ErrConflict
		}
		if err := applyDeleteGraphUpdate(ctx, tx, request, snapshot, after, update, identity, inputHash); err != nil {
			return MutationResult{}, err
		}
	}

	for _, deleted := range deletes {
		snapshot, ok := snapshots[deleted]
		if !ok {
			return MutationResult{}, schema.ErrConflict
		}
		identity, ok := identities[deleted]
		if !ok {
			return MutationResult{}, schema.ErrConflict
		}
		deleteCompatibility := CompatibilityNone
		if deleted == root {
			deleteCompatibility = compatibility
		}
		if err := applyDeleteGraphDelete(ctx, tx, request, snapshot, identity.eventID, inputHash, deleteCompatibility); err != nil {
			return MutationResult{}, err
		}
	}

	return MutationResult{EventID: identityCache.rootEventID, Compatibility: compatibility}, nil
}

func captureDeleteSnapshots(
	ctx context.Context,
	tx pgx.Tx,
	catalog schema.Catalog,
	root relation.RecordRef,
	deletes []relation.RecordRef,
	updates []relation.Update,
) (map[relation.RecordRef]deleteRecordSnapshot, error) {
	refs := make([]relation.RecordRef, 0, len(deletes)+len(updates))
	seen := make(map[relation.RecordRef]struct{}, len(deletes)+len(updates))
	for _, ref := range deletes {
		if ref.CollectionID == uuid.Nil || ref.RecordID == uuid.Nil {
			return nil, schema.ErrConflict
		}
		if _, duplicate := seen[ref]; duplicate {
			return nil, schema.ErrConflict
		}
		seen[ref] = struct{}{}
		refs = append(refs, ref)
	}
	if _, found := seen[root]; !found {
		return nil, schema.ErrConflict
	}
	for _, update := range updates {
		if update.Source.CollectionID == uuid.Nil || update.Source.RecordID == uuid.Nil {
			return nil, schema.ErrConflict
		}
		if _, deleted := seen[update.Source]; deleted {
			return nil, schema.ErrConflict
		}
		if _, duplicate := seen[update.Source]; duplicate {
			return nil, schema.ErrConflict
		}
		seen[update.Source] = struct{}{}
		refs = append(refs, update.Source)
	}
	sort.Slice(refs, func(i, j int) bool { return relationRecordRefLess(refs[i], refs[j]) })

	records, err := readDeleteGraphRecords(ctx, tx, catalog, refs)
	if err != nil {
		return nil, err
	}
	collections := catalogCollectionsByID(catalog)
	digests := make(map[uuid.UUID][32]byte, len(collections))
	snapshots := make(map[relation.RecordRef]deleteRecordSnapshot, len(refs))
	for _, ref := range refs {
		collection, ok := collections[ref.CollectionID]
		if !ok {
			return nil, schema.ErrConflict
		}
		digest, ok := digests[ref.CollectionID]
		if !ok {
			var err error
			digest, err = schemaDigest(collection)
			if err != nil {
				return nil, err
			}
			digests[ref.CollectionID] = digest
		}
		record, ok := records[ref]
		if !ok {
			return nil, schema.ErrConflict
		}
		version, validator, revisionPresent, err := readRevision(ctx, tx, ref.CollectionID, ref.RecordID)
		if err != nil {
			return nil, err
		}
		etag, err := recordETag(ref.CollectionID, ref.RecordID, digest, record, version, validator, revisionPresent)
		if err != nil {
			return nil, err
		}
		snapshots[ref] = deleteRecordSnapshot{
			ref: ref, collection: collection, record: record, schemaVersion: catalog.Version, schemaDigest: digest,
			previousVersion: version, previousETag: etag, revisionPresent: revisionPresent,
		}
	}
	return snapshots, nil
}

func (c *deleteArtifactIdentityCache) identitiesFor(
	s *Service,
	root relation.RecordRef,
	deletes []relation.RecordRef,
	updates []relation.Update,
) (map[relation.RecordRef]deleteArtifactIdentity, error) {
	if c == nil || c.rootEventID == uuid.Nil || c.byRecord == nil {
		return nil, errors.New("invalid root delete event id")
	}
	identities := make(map[relation.RecordRef]deleteArtifactIdentity, len(deletes)+len(updates))
	for _, ref := range deletes {
		identity, found := c.byRecord[ref]
		if ref == root {
			identity.eventID = c.rootEventID
		} else if !found {
			var err error
			identity.eventID, err = uuid.NewV7()
			if err != nil {
				return nil, fmt.Errorf("generate derived delete event id: %w", err)
			}
		}
		if identity.eventID == uuid.Nil {
			return nil, errors.New("invalid derived delete event id")
		}
		c.byRecord[ref] = identity
		identities[ref] = identity
	}
	for _, update := range updates {
		identity, found := c.byRecord[update.Source]
		if !found {
			var err error
			identity.eventID, err = uuid.NewV7()
			if err != nil {
				return nil, fmt.Errorf("generate relation clear event id: %w", err)
			}
		}
		if len(identity.validator) == 0 {
			var err error
			identity.validator, err = s.newValidator()
			if err != nil {
				return nil, err
			}
		}
		if identity.eventID == uuid.Nil || len(identity.validator) != 32 {
			return nil, errors.New("invalid relation clear identity")
		}
		c.byRecord[update.Source] = identity
		identities[update.Source] = identity
	}
	return identities, nil
}

func applyDeleteGraphUpdate(
	ctx context.Context,
	tx pgx.Tx,
	request DeleteRequest,
	snapshot deleteRecordSnapshot,
	after map[string]any,
	update relation.Update,
	identity deleteArtifactIdentity,
	inputHash [32]byte,
) error {
	newVersion := snapshot.previousVersion + 1
	newETag := revisionETag(snapshot.ref.CollectionID, snapshot.ref.RecordID, snapshot.schemaDigest, identity.validator)
	previousVersion := snapshot.previousVersion
	if err := writeAudit(ctx, tx, auditWrite{
		EventID: identity.eventID, Principal: request.Principal, RequestID: request.RequestID,
		Collection: snapshot.collection, RecordID: snapshot.ref.RecordID, Operation: "update",
		SchemaVersion: snapshot.schemaVersion, SchemaDigest: snapshot.schemaDigest,
		PreviousVersion: &previousVersion, NewVersion: newVersion,
		PreviousETag: snapshot.previousETag, NewETag: newETag,
		ChangedFields: append([]string(nil), update.ChangedFields...), InputHash: inputHash[:],
		Compatibility: CompatibilityNone,
	}); err != nil {
		return err
	}
	if err := writeRevision(ctx, tx, snapshot.ref.CollectionID, snapshot.ref.RecordID,
		snapshot.previousVersion, newVersion, identity.validator, identity.eventID); err != nil {
		return err
	}
	return writeOutbox(ctx, tx, identity.eventID, snapshot.collection, snapshot.ref.RecordID,
		"update", newVersion, mutationChanges(after, update.ChangedFields), false)
}

func touchDeleteGraphUpdates(
	ctx context.Context,
	tx pgx.Tx,
	snapshots map[relation.RecordRef]deleteRecordSnapshot,
	updates []relation.Update,
) error {
	for _, update := range updates {
		snapshot, ok := snapshots[update.Source]
		if !ok {
			return schema.ErrConflict
		}
		// updated_at uses statement_timestamp() like every other record write so a
		// derived update never stamps a time older than a PATCH that committed
		// while this delete waited on the collection locks.
		tag, err := tx.Exec(ctx, "UPDATE "+quotedRecordCollection(snapshot.collection)+
			" SET updated_at = statement_timestamp() WHERE id = $1", snapshot.ref.RecordID)
		if err != nil {
			return err
		}
		if tag.RowsAffected() != 1 {
			return schema.ErrConflict
		}
	}
	return nil
}

// readDeleteGraphRecords reuses one relation binding per collection and
// hydrates all source IDs in a single query per multi field. Base-row reads
// remain one per record because query owns their catalog-shaped scan codec;
// the expensive edge-table side is bounded by collection/field, not record.
func readDeleteGraphRecords(
	ctx context.Context,
	tx pgx.Tx,
	catalog schema.Catalog,
	refs []relation.RecordRef,
) (map[relation.RecordRef]map[string]any, error) {
	result := make(map[relation.RecordRef]map[string]any, len(refs))
	if len(refs) == 0 {
		return result, nil
	}
	collections := catalogCollectionsByID(catalog)
	byCollection := make(map[uuid.UUID][]uuid.UUID)
	for _, ref := range refs {
		if _, ok := collections[ref.CollectionID]; !ok || ref.RecordID == uuid.Nil {
			return nil, schema.ErrConflict
		}
		byCollection[ref.CollectionID] = append(byCollection[ref.CollectionID], ref.RecordID)
	}
	collectionIDs := make([]uuid.UUID, 0, len(byCollection))
	for collectionID := range byCollection {
		collectionIDs = append(collectionIDs, collectionID)
	}
	sort.Slice(collectionIDs, func(i, j int) bool { return collectionIDs[i].String() < collectionIDs[j].String() })
	for _, collectionID := range collectionIDs {
		collection := collections[collectionID]
		ids := append([]uuid.UUID(nil), byCollection[collectionID]...)
		sort.Slice(ids, func(i, j int) bool { return ids[i].String() < ids[j].String() })
		for index := 1; index < len(ids); index++ {
			if ids[index] == ids[index-1] {
				return nil, schema.ErrConflict
			}
		}
		bindings, err := bindRecordRelations(catalog, collection)
		if err != nil {
			return nil, err
		}
		records := make(map[uuid.UUID]map[string]any, len(ids))
		for _, id := range ids {
			record, err := readRecord(ctx, tx, collection, id, false)
			if err != nil {
				if errors.Is(err, query.ErrRecordNotFound) {
					return nil, schema.ErrConflict
				}
				return nil, err
			}
			records[id] = record
		}
		if bindings != nil && bindings.HasMultiple() {
			if err := bindings.Hydrate(ctx, tx, ids, records); err != nil {
				return nil, normalizeRelationError(err)
			}
		}
		for _, id := range ids {
			result[relation.RecordRef{CollectionID: collectionID, RecordID: id}] = records[id]
		}
	}
	return result, nil
}

func applyDeleteGraphDelete(
	ctx context.Context,
	tx pgx.Tx,
	request DeleteRequest,
	snapshot deleteRecordSnapshot,
	eventID uuid.UUID,
	inputHash [32]byte,
	compatibility Compatibility,
) error {
	statement := query.CompileDelete(snapshot.collection, snapshot.ref.RecordID)
	tag, err := tx.Exec(ctx, statement.SQL, statement.Args...)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return schema.ErrConflict
	}
	newVersion := snapshot.previousVersion + 1
	previousVersion := snapshot.previousVersion
	if err := writeAudit(ctx, tx, auditWrite{
		EventID: eventID, Principal: request.Principal, RequestID: request.RequestID,
		Collection: snapshot.collection, RecordID: snapshot.ref.RecordID, Operation: "delete",
		SchemaVersion: snapshot.schemaVersion, SchemaDigest: snapshot.schemaDigest,
		PreviousVersion: &previousVersion, NewVersion: newVersion,
		PreviousETag: snapshot.previousETag, ChangedFields: []string{}, InputHash: inputHash[:],
		Compatibility: compatibility,
	}); err != nil {
		return err
	}
	if snapshot.revisionPresent {
		tag, err := tx.Exec(ctx, `DELETE FROM pgbase_meta.record_revisions
			WHERE collection_id = $1 AND record_id = $2 AND version = $3`,
			snapshot.ref.CollectionID, snapshot.ref.RecordID, snapshot.previousVersion)
		if err != nil {
			return err
		}
		if tag.RowsAffected() != 1 {
			return errors.New("record revision changed outside the transaction kernel")
		}
	}
	return writeOutbox(ctx, tx, eventID, snapshot.collection, snapshot.ref.RecordID,
		"delete", newVersion, nil, true)
}

func catalogCollectionsByID(catalog schema.Catalog) map[uuid.UUID]schema.Collection {
	collections := make(map[uuid.UUID]schema.Collection, len(catalog.Collections))
	for _, collection := range catalog.Collections {
		collections[collection.ID] = collection
	}
	return collections
}

func relationRecordRefLess(left, right relation.RecordRef) bool {
	if left.CollectionID != right.CollectionID {
		return left.CollectionID.String() < right.CollectionID.String()
	}
	return left.RecordID.String() < right.RecordID.String()
}

func sortedUniqueStrings(values []string) bool {
	for index, value := range values {
		if value == "" || (index > 0 && values[index-1] >= value) {
			return false
		}
	}
	return true
}

func quotedRecordCollection(collection schema.Collection) string {
	return pgx.Identifier{"app", collection.TableName}.Sanitize()
}
