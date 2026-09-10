package recordops

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"sort"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/yakisoba0728/PgBase/internal/accessrule"
	"github.com/yakisoba0728/PgBase/internal/identity"
	"github.com/yakisoba0728/PgBase/internal/membership"
	"github.com/yakisoba0728/PgBase/internal/query"
	"github.com/yakisoba0728/PgBase/internal/recordaccess"
	"github.com/yakisoba0728/PgBase/internal/recordartifact"
	"github.com/yakisoba0728/PgBase/internal/relation"
	"github.com/yakisoba0728/PgBase/internal/schema"
)

type deleteRecordSnapshot struct {
	record          map[string]any
	previousVersion int64
	previousETag    string
	revisionPresent bool
}

const (
	maximumDeleteMaterializedBytes int64 = 32 << 20
	maximumDeleteHydrationRows     int64 = 100_000
	maximumDeleteHydrationFields   int64 = 256
)

type deleteReadBudget struct{ bytes, rows, fields int64 }

// deleteGroup is one collection's slice of the delete closure. Groups keep the
// plan's global (collection, record) order, so batching per collection commits
// artifacts in the same audit sequence the per-record path produced.
type deleteGroup struct {
	collection schema.Collection
	digest     [32]byte
	deletes    []uuid.UUID
	updates    []relation.Update
	snapshots  map[uuid.UUID]deleteRecordSnapshot
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
	writeFence membership.WriteFence,
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
	groups, err := groupDeleteClosure(catalog, root, deletes, updates)
	if err != nil {
		return MutationResult{}, err
	}
	readBudget := deleteReadBudget{bytes: maximumDeleteMaterializedBytes, rows: maximumDeleteHydrationRows, fields: maximumDeleteHydrationFields}
	if err := captureDeleteSnapshots(ctx, tx, catalog, groups, &readBudget); err != nil {
		return MutationResult{}, err
	}
	rootGroup, ok := findDeleteGroup(groups, root.CollectionID)
	if !ok {
		return MutationResult{}, schema.ErrConflict
	}
	rootSnapshot, ok := rootGroup.snapshots[root.RecordID]
	if !ok {
		return MutationResult{}, schema.ErrConflict
	}
	if request.IfMatch.Present && !request.IfMatch.matches(rootSnapshot.previousETag) {
		return MutationResult{}, &Error{Code: CodePreconditionFailed, CurrentETag: rootSnapshot.previousETag}
	}
	if err := authorizeDeleteClosure(ctx, tx, catalog, groups, request.Principal, request.RuleContext); err != nil {
		return MutationResult{}, err
	}

	identities, err := identityCache.identitiesFor(s, root, deletes, updates)
	if err != nil {
		return MutationResult{}, err
	}
	if err := plan.ExecuteStorage(ctx); err != nil {
		return MutationResult{}, normalizeRelationError(err)
	}
	updateBatches, err := touchDeleteSurvivors(ctx, tx, catalog, groups, identities, &readBudget)
	if err != nil {
		return MutationResult{}, err
	}
	if err := commitMutation(ctx, tx, catalog, request.Principal, request.RequestID, inputHash[:], updateBatches...); err != nil {
		return MutationResult{}, err
	}

	removedMembership := make([]membership.Record, 0, len(deletes))
	for _, group := range groups {
		if len(group.deletes) == 0 {
			continue
		}
		tag, err := tx.Exec(ctx, "DELETE FROM "+quotedRecordCollection(group.collection)+
			" WHERE id = ANY($1::uuid[])", group.deletes)
		if err != nil {
			return MutationResult{}, err
		}
		if err := expectRows(tag, len(group.deletes), schema.ErrConflict); err != nil {
			return MutationResult{}, err
		}
		for _, recordID := range group.deletes {
			removedMembership = append(removedMembership, membership.Record{
				CollectionID: group.collection.ID, RecordID: recordID,
			})
		}
	}
	// Membership rows are removed only after every base record in the closure
	// is gone. WriteFence.Delete verifies that ordering again against the base
	// tables before deleting any sidecar.
	if err := writeFence.Delete(ctx, removedMembership); err != nil {
		return MutationResult{}, transactionalMembership(err)
	}

	deleteBatches, err := deleteArtifactBatches(catalog, root, groups, identities, compatibility)
	if err != nil {
		return MutationResult{}, err
	}
	if err := commitMutation(ctx, tx, catalog, request.Principal, request.RequestID, inputHash[:], deleteBatches...); err != nil {
		return MutationResult{}, err
	}
	return MutationResult{EventID: identityCache.rootEventID, Compatibility: compatibility}, nil
}

func authorizeDeleteClosure(ctx context.Context, tx pgx.Tx, catalog schema.Catalog, groups []deleteGroup, principal Principal, request *accessrule.RequestContext) error {
	if principal.Kind == PrincipalAdministrator {
		return nil
	}
	for _, group := range groups {
		checks := []struct {
			action string
			ids    []uuid.UUID
		}{{"delete", group.deletes}}
		updates := make([]uuid.UUID, len(group.updates))
		for i := range group.updates {
			updates[i] = group.updates[i].Source.RecordID
		}
		checks = append(checks, struct {
			action string
			ids    []uuid.UUID
		}{"update", updates})
		for _, check := range checks {
			if len(check.ids) == 0 {
				continue
			}
			if err := recordaccess.AuthorizeSubject(ctx, tx, group.collection.AccessCollectionID(), principal, check.action); err != nil {
				return err
			}
			if principal.Kind == identity.Service {
				continue
			}
			allowed, err := recordaccess.AllowsRecordsInCatalogWithRequest(ctx, tx, catalog, policyCollection(group.collection), principal, check.action, check.ids, false, request)
			if err != nil {
				return err
			}
			if !allowed {
				return query.ErrRecordNotFound
			}
		}
	}
	return nil
}

// groupDeleteClosure splits the plan's closure into per-collection batches
// while re-verifying the disjointness the plan promises. deletes and updates
// arrive sorted by (collection, record), so first appearance is already the
// global collection order.
func groupDeleteClosure(
	catalog schema.Catalog,
	root relation.RecordRef,
	deletes []relation.RecordRef,
	updates []relation.Update,
) ([]deleteGroup, error) {
	collections := catalogCollectionsByID(catalog)
	byCollection := make(map[uuid.UUID]*deleteGroup, len(collections))
	order := make([]uuid.UUID, 0, len(collections))
	group := func(collectionID uuid.UUID) (*deleteGroup, error) {
		if existing, ok := byCollection[collectionID]; ok {
			return existing, nil
		}
		collection, ok := collections[collectionID]
		if !ok {
			return nil, schema.ErrConflict
		}
		digest, err := recordartifact.SchemaDigest(collection)
		if err != nil {
			return nil, err
		}
		created := &deleteGroup{collection: collection, digest: digest}
		byCollection[collectionID] = created
		order = append(order, collectionID)
		return created, nil
	}

	seen := make(map[relation.RecordRef]struct{}, len(deletes)+len(updates))
	rootFound := false
	for _, ref := range deletes {
		if ref.CollectionID == uuid.Nil || ref.RecordID == uuid.Nil {
			return nil, schema.ErrConflict
		}
		if _, duplicate := seen[ref]; duplicate {
			return nil, schema.ErrConflict
		}
		seen[ref] = struct{}{}
		rootFound = rootFound || ref == root
		current, err := group(ref.CollectionID)
		if err != nil {
			return nil, err
		}
		current.deletes = append(current.deletes, ref.RecordID)
	}
	if !rootFound {
		return nil, schema.ErrConflict
	}
	for _, update := range updates {
		if update.Source.CollectionID == uuid.Nil || update.Source.RecordID == uuid.Nil {
			return nil, schema.ErrConflict
		}
		if _, duplicate := seen[update.Source]; duplicate {
			return nil, schema.ErrConflict
		}
		if len(update.ChangedFields) == 0 || !sortedUniqueStrings(update.ChangedFields) {
			return nil, schema.ErrConflict
		}
		seen[update.Source] = struct{}{}
		current, err := group(update.Source.CollectionID)
		if err != nil {
			return nil, err
		}
		current.updates = append(current.updates, update)
	}

	sort.Slice(order, func(i, j int) bool { return bytes.Compare(order[i][:], order[j][:]) < 0 })
	groups := make([]deleteGroup, 0, len(order))
	for _, collectionID := range order {
		groups = append(groups, *byCollection[collectionID])
	}
	return groups, nil
}

func findDeleteGroup(groups []deleteGroup, collectionID uuid.UUID) (deleteGroup, bool) {
	for _, group := range groups {
		if group.collection.ID == collectionID {
			return group, true
		}
	}
	return deleteGroup{}, false
}

// captureDeleteSnapshots reads the closure's pre-images with two statements per
// collection plus one per multiple-relation field, instead of two per record.
func captureDeleteSnapshots(ctx context.Context, tx pgx.Tx, catalog schema.Catalog, groups []deleteGroup, budget *deleteReadBudget) error {
	for index := range groups {
		group := &groups[index]
		ids := make([]uuid.UUID, 0, len(group.deletes)+len(group.updates))
		ids = append(ids, group.deletes...)
		for _, update := range group.updates {
			ids = append(ids, update.Source.RecordID)
		}
		records, err := readRecordBatch(ctx, tx, catalog, group.collection, ids, budget)
		if err != nil {
			return err
		}
		revisions, err := readRevisionBatch(ctx, tx, group.collection.ID, ids)
		if err != nil {
			return err
		}
		group.snapshots = make(map[uuid.UUID]deleteRecordSnapshot, len(ids))
		for _, id := range ids {
			revision, present := revisions[id]
			etag, err := recordartifact.ETag(
				group.collection.ID, id, group.digest, records[id], revision.validator, present)
			if err != nil {
				return err
			}
			group.snapshots[id] = deleteRecordSnapshot{
				record: records[id], previousVersion: revision.version,
				previousETag: etag, revisionPresent: present,
			}
		}
	}
	return nil
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

// touchDeleteSurvivors stamps every surviving source and reads back the record
// its cleared relations produced, then describes the derived update artifacts.
func touchDeleteSurvivors(
	ctx context.Context,
	tx pgx.Tx,
	catalog schema.Catalog,
	groups []deleteGroup,
	identities map[relation.RecordRef]deleteArtifactIdentity,
	budget *deleteReadBudget,
) ([]artifactBatch, error) {
	batches := make([]artifactBatch, 0, len(groups))
	for _, group := range groups {
		if len(group.updates) == 0 {
			continue
		}
		ids := make([]uuid.UUID, len(group.updates))
		for index, update := range group.updates {
			ids[index] = update.Source.RecordID
		}
		// updated_at uses statement_timestamp() like every other record write so a
		// derived update never stamps a time older than a PATCH that committed
		// while this delete waited on the collection locks.
		tag, err := tx.Exec(ctx, "UPDATE "+quotedRecordCollection(group.collection)+
			" SET updated_at = statement_timestamp() WHERE id = ANY($1::uuid[])", ids)
		if err != nil {
			return nil, err
		}
		if err := expectRows(tag, len(ids), schema.ErrConflict); err != nil {
			return nil, err
		}
		after, err := readRecordBatch(ctx, tx, catalog, group.collection, ids, budget)
		if err != nil {
			return nil, err
		}

		batch := artifactBatch{
			collection: group.collection, operation: "update",
			schemaVersion: catalog.Version, schemaDigest: group.digest,
			rows: make([]artifactRow, 0, len(group.updates)),
		}
		for _, update := range group.updates {
			snapshot, ok := group.snapshots[update.Source.RecordID]
			if !ok {
				return nil, schema.ErrConflict
			}
			identity, ok := identities[update.Source]
			if !ok || len(identity.validator) != 32 {
				return nil, schema.ErrConflict
			}
			previousVersion := snapshot.previousVersion
			batch.rows = append(batch.rows, artifactRow{
				eventID: identity.eventID, recordID: update.Source.RecordID,
				previousVersion: &previousVersion, newVersion: previousVersion + 1,
				previousETag: snapshot.previousETag,
				newETag: recordartifact.RevisionETag(
					group.collection.ID, update.Source.RecordID, group.digest, identity.validator),
				changedFields: append([]string(nil), update.ChangedFields...),
				changes:       after[update.Source.RecordID],
				validator:     identity.validator,
			})
		}
		batches = append(batches, batch)
	}
	return batches, nil
}

func deleteArtifactBatches(
	catalog schema.Catalog,
	root relation.RecordRef,
	groups []deleteGroup,
	identities map[relation.RecordRef]deleteArtifactIdentity,
	compatibility Compatibility,
) ([]artifactBatch, error) {
	batches := make([]artifactBatch, 0, len(groups))
	for _, group := range groups {
		if len(group.deletes) == 0 {
			continue
		}
		batch := artifactBatch{
			collection: group.collection, operation: "delete",
			schemaVersion: catalog.Version, schemaDigest: group.digest,
			rows: make([]artifactRow, 0, len(group.deletes)),
		}
		for _, recordID := range group.deletes {
			ref := relation.RecordRef{CollectionID: group.collection.ID, RecordID: recordID}
			snapshot, ok := group.snapshots[recordID]
			if !ok {
				return nil, schema.ErrConflict
			}
			identity, ok := identities[ref]
			if !ok {
				return nil, schema.ErrConflict
			}
			deleteCompatibility := CompatibilityNone
			if ref == root {
				deleteCompatibility = compatibility
			}
			previousVersion := snapshot.previousVersion
			batch.rows = append(batch.rows, artifactRow{
				eventID: identity.eventID, recordID: recordID,
				previousVersion: &previousVersion, newVersion: previousVersion + 1,
				previousETag: snapshot.previousETag, changedFields: []string{},
				tombstone: true, compatibility: deleteCompatibility, changes: snapshot.record,
				revisionPresent: snapshot.revisionPresent,
			})
		}
		batches = append(batches, batch)
	}
	return batches, nil
}

func catalogCollectionsByID(catalog schema.Catalog) map[uuid.UUID]schema.Collection {
	collections := make(map[uuid.UUID]schema.Collection, len(catalog.Collections))
	for _, collection := range catalog.Collections {
		collections[collection.ID] = collection
	}
	return collections
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
