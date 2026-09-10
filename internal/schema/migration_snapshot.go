package schema

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

const MigrationSnapshotFormat = "pgbase.schema-migration/v1"

var ErrMigrationSnapshotMismatch = errors.New("database schema does not match migration snapshot")

// MigrationSnapshot is a source-generation snapshot. Unlike SchemaDocument it
// keeps field IDs so a diff can distinguish a rename from drop-and-add. The IDs
// are logical matching hints: replayed databases may allocate different IDs.
type MigrationSnapshot struct {
	Format      string                        `json:"format"`
	Collections []MigrationSnapshotCollection `json:"collections"`
}

type MigrationSnapshotCollection struct {
	Name       string          `json:"name"`
	RecordID   *RecordIDPolicy `json:"recordId,omitempty"`
	Fields     []FieldInput    `json:"fields,omitempty"`
	Indexes    []IndexInput    `json:"indexes,omitempty"`
	View       *ViewInput      `json:"view,omitempty"`
	DependsOn  []string        `json:"dependsOn,omitempty"`
	UserAccess bool            `json:"userAccess"`
	Rules      json.RawMessage `json:"rules"`
}

type MigrationImpact struct {
	Operations   []string `json:"operations"`
	DataLoss     []string `json:"dataLoss,omitempty"`
	Irreversible []string `json:"irreversible,omitempty"`
}

func (s *Service) ExportMigrationSnapshot(ctx context.Context) ([]byte, error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	var snapshot []byte
	err := s.pool.InTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly}, func(tx pgx.Tx) error {
		var err error
		snapshot, err = ExportMigrationSnapshot(ctx, tx)
		return err
	})
	return snapshot, err
}

func ExportMigrationSnapshot(ctx context.Context, tx pgx.Tx) ([]byte, error) {
	store := NewStore(tx)
	version, err := store.SchemaVersion(ctx)
	if err != nil {
		return nil, err
	}
	catalog, err := store.ReadCatalog(ctx, version)
	if err != nil {
		return nil, err
	}
	snapshot := MigrationSnapshot{Format: MigrationSnapshotFormat}
	names := make([]string, 0, len(catalog.Collections))
	for name := range catalog.Collections {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		collection, err := migrationSnapshotCollection(ctx, tx, catalog, catalog.Collections[name])
		if err != nil {
			return nil, err
		}
		snapshot.Collections = append(snapshot.Collections, collection)
	}
	return MarshalMigrationSnapshot(snapshot)
}

func migrationSnapshotCollection(ctx context.Context, tx pgx.Tx, catalog Catalog, collection Collection) (MigrationSnapshotCollection, error) {
	item := MigrationSnapshotCollection{
		Name:  collection.Name,
		Rules: json.RawMessage(`{"list":{"op":"owner"},"view":{"op":"owner"},"create":{"op":"owner"},"update":{"op":"owner"},"delete":{"op":"owner"}}`),
	}
	if collection.RecordID.Kind == RecordIDText {
		policy := collection.RecordID
		item.RecordID = &policy
	}
	for _, field := range collection.Fields {
		id := field.ID
		input := FieldInput{
			ID: &id, Name: field.Name, Type: field.Type, Required: field.Required,
			Unique: field.Unique, Indexed: field.Indexed, Default: append(json.RawMessage(nil), field.Default...),
			Options: cloneFieldOptionsPointer(field.Options), Hidden: field.Hidden,
			Presentable: field.Presentable, Help: field.Help,
		}
		if field.Relation != nil {
			target, ok := collectionByID(catalog, field.Relation.TargetCollectionID)
			if !ok {
				return item, ErrCatalogInvalid
			}
			input.Relation = &RelationInput{
				TargetCollection: target.Name, Cardinality: field.Relation.Cardinality,
				MinSelect: intPointer(field.Relation.MinSelect), MaxSelect: intPointer(field.Relation.MaxSelect),
				OnDelete: field.Relation.OnDelete,
			}
		}
		item.Fields = append(item.Fields, input)
	}
	for _, index := range collection.Indexes {
		item.Indexes = append(item.Indexes, IndexInput{Name: index.Name, Fields: append([]string(nil), index.Fields...), Unique: index.Unique})
	}
	if collection.View != nil {
		item.View = &ViewInput{
			Source: collection.View.Source, Projection: append([]string(nil), collection.View.Projection...),
			Filter: cloneViewFilter(collection.View.Filter), SQL: collection.View.SQL,
		}
		for _, dependencyID := range collection.View.Dependencies {
			if dependency, ok := collectionByID(catalog, dependencyID); ok {
				item.DependsOn = append(item.DependsOn, dependency.Name)
			}
		}
		if collection.View.SQL == "" && collection.View.Source != "" {
			item.DependsOn = append(item.DependsOn, collection.View.Source)
		}
	}
	if err := tx.QueryRow(ctx, `select coalesce((select enabled from pgbase_meta.collection_access where collection_id=$1),false)`, collection.ID).Scan(&item.UserAccess); err != nil {
		return item, err
	}
	var rules []byte
	err := tx.QueryRow(ctx, `select rules from pgbase_meta.collection_rules where collection_id=$1`, collection.ID).Scan(&rules)
	if err == nil {
		item.Rules = append(json.RawMessage(nil), rules...)
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return item, err
	}
	return item, nil
}

func MarshalMigrationSnapshot(snapshot MigrationSnapshot) ([]byte, error) {
	canonical, err := canonicalMigrationSnapshot(snapshot)
	if err != nil {
		return nil, err
	}
	return json.MarshalIndent(canonical, "", "  ")
}

func CanonicalMigrationSnapshot(raw []byte) ([]byte, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var snapshot MigrationSnapshot
	if err := decoder.Decode(&snapshot); err != nil {
		return nil, fmt.Errorf("decode migration snapshot: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return nil, errors.New("decode migration snapshot: trailing JSON value")
	}
	return MarshalMigrationSnapshot(snapshot)
}

func canonicalMigrationSnapshot(snapshot MigrationSnapshot) (MigrationSnapshot, error) {
	if snapshot.Format != MigrationSnapshotFormat {
		return MigrationSnapshot{}, fmt.Errorf("unsupported migration snapshot format %q", snapshot.Format)
	}
	snapshot.Collections = append([]MigrationSnapshotCollection(nil), snapshot.Collections...)
	sort.Slice(snapshot.Collections, func(i, j int) bool { return snapshot.Collections[i].Name < snapshot.Collections[j].Name })
	collectionNames := map[string]bool{}
	fieldIDs := map[uuid.UUID]string{}
	for index := range snapshot.Collections {
		item := &snapshot.Collections[index]
		if item.Name == "" || collectionNames[item.Name] {
			return MigrationSnapshot{}, fmt.Errorf("invalid or duplicate collection name %q", item.Name)
		}
		collectionNames[item.Name] = true
		item.Fields = cloneFieldInputs(item.Fields)
		for fieldIndex := range item.Fields {
			field := &item.Fields[fieldIndex]
			if field.ID == nil || *field.ID == uuid.Nil {
				return MigrationSnapshot{}, fmt.Errorf("collection %s field %s has no stable ID", item.Name, field.Name)
			}
			if prior := fieldIDs[*field.ID]; prior != "" {
				return MigrationSnapshot{}, fmt.Errorf("field ID %s is shared by %s and %s.%s", field.ID.String(), prior, item.Name, field.Name)
			}
			fieldIDs[*field.ID] = item.Name + "." + field.Name
			canonicalizeSnapshotStringSets(field.Options)
			var err error
			field.Default, err = canonicalSnapshotJSON(field.Default)
			if err != nil {
				return MigrationSnapshot{}, fmt.Errorf("collection %s field %s default: %w", item.Name, field.Name, err)
			}
		}
		item.Indexes = cloneIndexInputs(item.Indexes)
		sort.Slice(item.Indexes, func(i, j int) bool { return item.Indexes[i].Name < item.Indexes[j].Name })
		if item.View != nil {
			view := *item.View
			view.Projection = append([]string(nil), view.Projection...)
			view.Filter = cloneViewFilter(view.Filter)
			item.View = &view
		}
		item.DependsOn = sortedUniqueStrings(item.DependsOn)
		var err error
		item.Rules, err = canonicalSnapshotJSON(item.Rules)
		if err != nil {
			return MigrationSnapshot{}, fmt.Errorf("collection %s rules: %w", item.Name, err)
		}
		if len(item.Rules) == 0 {
			return MigrationSnapshot{}, fmt.Errorf("collection %s rules are required", item.Name)
		}
	}
	for _, item := range snapshot.Collections {
		for _, field := range item.Fields {
			if field.Relation != nil && !collectionNames[field.Relation.TargetCollection] {
				return MigrationSnapshot{}, fmt.Errorf("collection %s field %s references missing collection %s", item.Name, field.Name, field.Relation.TargetCollection)
			}
		}
		for _, dependency := range item.DependsOn {
			if !collectionNames[dependency] {
				return MigrationSnapshot{}, fmt.Errorf("view %s references missing dependency %s", item.Name, dependency)
			}
		}
	}
	if len(orderedMigrationViews(snapshot, false)) != countMigrationViews(snapshot) {
		return MigrationSnapshot{}, errors.New("migration snapshot contains cyclic view dependencies")
	}
	return snapshot, nil
}

func canonicalSnapshotJSON(raw json.RawMessage) (json.RawMessage, error) {
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil, nil
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return nil, errors.New("trailing JSON value")
	}
	canonical, err := json.Marshal(value)
	return json.RawMessage(canonical), err
}

var snapshotOptionStringSets = map[string]struct{}{
	"allowedDomains": {},
	"allowedValues":  {},
	"deniedDomains":  {},
	"mimeTypes":      {},
	"thumbs":         {},
}

// canonicalizeSnapshotStringSets uses JSON field names so additions to
// FieldOptions, such as deniedDomains, become deterministic without a second
// migration-only representation of that public schema contract.
func canonicalizeSnapshotStringSets(options any) {
	value := reflect.ValueOf(options)
	if !value.IsValid() || value.Kind() != reflect.Pointer || value.IsNil() || value.Elem().Kind() != reflect.Struct {
		return
	}
	value = value.Elem()
	typeOf := value.Type()
	for index := 0; index < value.NumField(); index++ {
		field := value.Field(index)
		if !field.CanSet() || field.Kind() != reflect.Slice || field.Type().Elem().Kind() != reflect.String {
			continue
		}
		name := strings.Split(typeOf.Field(index).Tag.Get("json"), ",")[0]
		if _, isSet := snapshotOptionStringSets[name]; !isSet {
			continue
		}
		items := append([]string(nil), field.Interface().([]string)...)
		sort.Strings(items)
		write := 0
		for _, item := range items {
			if write > 0 && items[write-1] == item {
				continue
			}
			items[write] = item
			write++
		}
		field.Set(reflect.ValueOf(items[:write]))
	}
}

func sortedUniqueStrings(values []string) []string {
	result := append([]string(nil), values...)
	sort.Strings(result)
	write := 0
	for _, value := range result {
		if value == "" || write > 0 && result[write-1] == value {
			continue
		}
		result[write] = value
		write++
	}
	return result[:write]
}

func MigrationSnapshotsEquivalent(left, right []byte) (bool, error) {
	leftLogical, err := logicalMigrationSnapshot(left)
	if err != nil {
		return false, err
	}
	rightLogical, err := logicalMigrationSnapshot(right)
	if err != nil {
		return false, err
	}
	return bytes.Equal(leftLogical, rightLogical), nil
}

func logicalMigrationSnapshot(raw []byte) ([]byte, error) {
	canonical, err := CanonicalMigrationSnapshot(raw)
	if err != nil {
		return nil, err
	}
	var snapshot MigrationSnapshot
	if err := json.Unmarshal(canonical, &snapshot); err != nil {
		return nil, err
	}
	for collectionIndex := range snapshot.Collections {
		for fieldIndex := range snapshot.Collections[collectionIndex].Fields {
			snapshot.Collections[collectionIndex].Fields[fieldIndex].ID = nil
		}
	}
	return json.Marshal(snapshot)
}

func AnalyzeMigrationSnapshots(beforeRaw, afterRaw []byte) (MigrationImpact, error) {
	before, err := decodeMigrationSnapshot(beforeRaw)
	if err != nil {
		return MigrationImpact{}, err
	}
	after, err := decodeMigrationSnapshot(afterRaw)
	if err != nil {
		return MigrationImpact{}, err
	}
	impact := MigrationImpact{}
	beforeCollections, afterCollections := migrationCollectionMap(before), migrationCollectionMap(after)
	for _, beforeCollection := range before.Collections {
		operationsBeforeCollection := len(impact.Operations)
		afterCollection, exists := afterCollections[beforeCollection.Name]
		if !exists {
			message := "drop collection " + beforeCollection.Name
			impact.Operations = append(impact.Operations, message)
			impact.DataLoss = append(impact.DataLoss, message+" and its records")
			impact.Irreversible = append(impact.Irreversible, message+" cannot restore removed records")
			continue
		}
		if beforeCollection.View != nil || afterCollection.View != nil {
			if !migrationCollectionEqual(beforeCollection, afterCollection) {
				impact.Operations = append(impact.Operations, "replace view "+beforeCollection.Name)
			}
			continue
		}
		beforeByID := migrationFieldMap(beforeCollection.Fields)
		afterByID := migrationFieldMap(afterCollection.Fields)
		for id, field := range beforeByID {
			next, kept := afterByID[id]
			if !kept {
				message := fmt.Sprintf("drop field %s.%s", beforeCollection.Name, field.Name)
				impact.Operations = append(impact.Operations, message)
				impact.DataLoss = append(impact.DataLoss, message+" values")
				impact.Irreversible = append(impact.Irreversible, message+" cannot restore removed values")
			} else if field.Type != next.Type {
				message := fmt.Sprintf("change field type %s.%s from %s to %s", beforeCollection.Name, field.Name, field.Type, next.Type)
				impact.Operations = append(impact.Operations, message)
				impact.Irreversible = append(impact.Irreversible, message+" cannot guarantee an inverse conversion")
			} else if field.Name != next.Name {
				impact.Operations = append(impact.Operations, fmt.Sprintf("rename field %s.%s to %s", beforeCollection.Name, field.Name, next.Name))
			}
		}
		for id, field := range afterByID {
			if _, exists := beforeByID[id]; !exists {
				message := fmt.Sprintf("add field %s.%s", afterCollection.Name, field.Name)
				impact.Operations = append(impact.Operations, message)
				impact.DataLoss = append(impact.DataLoss, "Down "+message+" drops values written after Up")
			}
		}
		if !migrationCollectionEqual(beforeCollection, afterCollection) && len(impact.Operations) == operationsBeforeCollection {
			impact.Operations = append(impact.Operations, "update collection "+beforeCollection.Name)
		}
	}
	for _, afterCollection := range after.Collections {
		if _, exists := beforeCollections[afterCollection.Name]; !exists {
			impact.Operations = append(impact.Operations, "create collection "+afterCollection.Name)
			impact.DataLoss = append(impact.DataLoss, "Down drops collection "+afterCollection.Name+" and records written after Up")
		}
	}
	sort.Strings(impact.Operations)
	sort.Strings(impact.DataLoss)
	sort.Strings(impact.Irreversible)
	return impact, nil
}

func decodeMigrationSnapshot(raw []byte) (MigrationSnapshot, error) {
	canonical, err := CanonicalMigrationSnapshot(raw)
	if err != nil {
		return MigrationSnapshot{}, err
	}
	var snapshot MigrationSnapshot
	err = json.Unmarshal(canonical, &snapshot)
	return snapshot, err
}

func migrationCollectionMap(snapshot MigrationSnapshot) map[string]MigrationSnapshotCollection {
	result := make(map[string]MigrationSnapshotCollection, len(snapshot.Collections))
	for _, collection := range snapshot.Collections {
		result[collection.Name] = collection
	}
	return result
}

func migrationFieldMap(fields []FieldInput) map[uuid.UUID]FieldInput {
	result := make(map[uuid.UUID]FieldInput, len(fields))
	for _, field := range fields {
		if field.ID != nil {
			result[*field.ID] = field
		}
	}
	return result
}

func migrationCollectionEqual(left, right MigrationSnapshotCollection) bool {
	leftRaw, _ := json.Marshal(left)
	rightRaw, _ := json.Marshal(right)
	return bytes.Equal(leftRaw, rightRaw)
}

// ApplyMigrationSnapshot applies one generated snapshot transition inside the
// user migration transaction. The runner owns the single schema-version bump.
func ApplyMigrationSnapshot(ctx context.Context, tx pgx.Tx, beforeRaw, afterRaw []byte) error {
	before, err := decodeMigrationSnapshot(beforeRaw)
	if err != nil {
		return fmt.Errorf("before snapshot: %w", err)
	}
	after, err := decodeMigrationSnapshot(afterRaw)
	if err != nil {
		return fmt.Errorf("after snapshot: %w", err)
	}
	currentRaw, err := ExportMigrationSnapshot(ctx, tx)
	if err != nil {
		return err
	}
	equivalent, err := MigrationSnapshotsEquivalent(currentRaw, beforeRaw)
	if err != nil || !equivalent {
		if err != nil {
			return err
		}
		return fmt.Errorf("%w: before transition", ErrMigrationSnapshotMismatch)
	}
	store := NewStore(tx)
	version, err := store.LockedSchemaVersion(ctx)
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `select pg_advisory_xact_lock($1)`, schemaAdvisoryLockKey); err != nil {
		return err
	}
	service := &Service{}
	companions, err := companionCapabilityReady(ctx, tx)
	if err != nil {
		return err
	}
	catalog, err := store.ReadCatalog(ctx, version)
	if err != nil {
		return err
	}
	beforeMap, afterMap := migrationCollectionMap(before), migrationCollectionMap(after)
	currentViews := orderedMigrationViews(before, true)
	for _, item := range currentViews {
		if err := applySnapshotChange(ctx, tx, service, store, &catalog, NewDeleteChange(item.Name), MigrationSnapshotCollection{}, companions); err != nil {
			return err
		}
	}
	for _, item := range after.Collections {
		if item.View != nil {
			continue
		}
		if prior, exists := beforeMap[item.Name]; exists && prior.View == nil {
			continue
		}
		scalars := make([]FieldInput, 0, len(item.Fields))
		for _, field := range item.Fields {
			if field.Type == TypeRelation {
				continue
			}
			field.ID = nil
			scalars = append(scalars, field)
		}
		change := NewCreateChange(CreateCollectionInput{Name: item.Name, RecordID: item.RecordID, Fields: scalars})
		if err := applySnapshotChange(ctx, tx, service, store, &catalog, change, item, companions); err != nil {
			return err
		}
	}
	for _, item := range after.Collections {
		if item.View != nil {
			continue
		}
		baseline := beforeMap[item.Name]
		if baseline.View != nil {
			baseline = MigrationSnapshotCollection{}
		}
		current := catalog.Collections[item.Name]
		fields, err := mappedMigrationFields(item.Fields, baseline.Fields, current.Fields)
		if err != nil {
			return err
		}
		indexes := cloneIndexInputs(item.Indexes)
		change := NewUpdateChange(UpdateCollectionInput{Name: item.Name, RecordID: item.RecordID, Fields: fields, Indexes: &indexes})
		if err := applySnapshotChange(ctx, tx, service, store, &catalog, change, item, companions); err != nil {
			return err
		}
	}
	for _, item := range before.Collections {
		if item.View != nil {
			continue
		}
		if _, exists := afterMap[item.Name]; exists {
			if afterMap[item.Name].View == nil {
				continue
			}
		}
		current := catalog.Collections[item.Name]
		scalars := make([]FieldInput, 0, len(current.Fields))
		for _, field := range current.Fields {
			if field.Type == TypeRelation {
				continue
			}
			id := field.ID
			scalars = append(scalars, FieldInput{ID: &id, Name: field.Name, Type: field.Type, Required: field.Required, Unique: field.Unique, Indexed: field.Indexed, Default: append(json.RawMessage(nil), field.Default...), Options: cloneFieldOptionsPointer(field.Options), Hidden: field.Hidden, Presentable: field.Presentable, Help: field.Help})
		}
		if len(scalars) != len(current.Fields) {
			emptyIndexes := []IndexInput{}
			change := NewUpdateChange(UpdateCollectionInput{Name: item.Name, RecordID: item.RecordID, Fields: scalars, Indexes: &emptyIndexes})
			if err := applySnapshotChange(ctx, tx, service, store, &catalog, change, item, companions); err != nil {
				return err
			}
		}
	}
	for _, item := range before.Collections {
		if item.View != nil {
			continue
		}
		if target, exists := afterMap[item.Name]; exists && target.View == nil {
			continue
		}
		if err := applySnapshotChange(ctx, tx, service, store, &catalog, NewDeleteChange(item.Name), MigrationSnapshotCollection{}, companions); err != nil {
			return err
		}
	}
	for _, item := range orderedMigrationViews(after, false) {
		change := NewCreateChange(CreateCollectionInput{Name: item.Name, View: item.View})
		if err := applySnapshotChange(ctx, tx, service, store, &catalog, change, item, companions); err != nil {
			return err
		}
	}
	for _, item := range after.Collections {
		collection, ok := catalog.Collections[item.Name]
		if !ok {
			return ErrMigrationSnapshotMismatch
		}
		if _, err := tx.Exec(ctx, `insert into pgbase_meta.collection_access(collection_id,enabled) values($1,$2) on conflict(collection_id) do update set enabled=excluded.enabled`, collection.ID, item.UserAccess); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `insert into pgbase_meta.collection_rules(collection_id,rules) values($1,$2::jsonb) on conflict(collection_id) do update set rules=excluded.rules,revision=pgbase_meta.collection_rules.revision+1,updated_at=clock_timestamp()`, collection.ID, string(item.Rules)); err != nil {
			return err
		}
	}
	finalRaw, err := ExportMigrationSnapshot(ctx, tx)
	if err != nil {
		return err
	}
	equivalent, err = MigrationSnapshotsEquivalent(finalRaw, afterRaw)
	if err != nil || !equivalent {
		if err != nil {
			return err
		}
		return fmt.Errorf("%w: after transition", ErrMigrationSnapshotMismatch)
	}
	if _, err := tx.Exec(ctx, `select pg_notify('pgbase_schema_changed',$1)`, fmt.Sprint(version+1)); err != nil {
		return err
	}
	return nil
}

func applySnapshotChange(ctx context.Context, tx pgx.Tx, service *Service, store *Store, catalog *Catalog, change Change, snapshot MigrationSnapshotCollection, companions bool) error {
	var err error
	if isSQLViewChange(change) {
		change, err = inspectSQLViewChange(ctx, tx, *catalog, change)
		if err != nil {
			return err
		}
	}
	plan, err := compilePlan(*catalog, change, companions)
	if err != nil {
		return err
	}
	if snapshot.View != nil && len(snapshot.Fields) != 0 {
		if err := applyMigrationViewFields(&plan, snapshot); err != nil {
			return err
		}
	}
	now := time.Now().UTC()
	switch change.kind {
	case changeCreate:
		collection, err := newCollectionMetadata(plan.Definition)
		if err != nil {
			return err
		}
		collection.CreatedAt = now
		if err := service.executePlan(ctx, tx, plan); err != nil {
			return err
		}
		if err := store.InsertCollection(ctx, collection, plan.Definition, now); err != nil {
			return err
		}
	case changeUpdate:
		current := catalog.Collections[change.update.Name]
		if err := validateRequiredRelationAdds(ctx, tx, current, plan.Definition, true); err != nil {
			return err
		}
		fields, err := updatedFieldMetadata(current.ID, current.Fields, plan.Definition)
		if err != nil {
			return err
		}
		indexes, err := updatedIndexMetadata(current.Name, current.Indexes, plan.Definition)
		if err != nil {
			return err
		}
		next := Collection{ID: current.ID, Name: current.Name, TableName: current.TableName, Fields: fields, Indexes: indexes, View: resolvedViewSpec(plan.Definition.View), CreatedAt: current.CreatedAt}
		if err := validateChangedFieldOptions(ctx, tx, current, next); err != nil {
			return err
		}
		if err := service.executePlan(ctx, tx, plan); err != nil {
			return err
		}
		if err := validateChangedRecordSizes(ctx, tx, current, next); err != nil {
			return err
		}
		if err := store.UpdateCollection(ctx, next, plan.Definition, now); err != nil {
			return err
		}
	case changeDelete:
		current := catalog.Collections[change.deleteName]
		if err := service.executePlan(ctx, tx, plan); err != nil {
			return err
		}
		if err := store.DeleteCollection(ctx, current.ID); err != nil {
			return err
		}
	default:
		return ErrConflict
	}
	*catalog, err = store.ReadCatalog(ctx, catalog.Version)
	return err
}

func applyMigrationViewFields(plan *Plan, snapshot MigrationSnapshotCollection) error {
	if len(snapshot.Fields) != len(plan.Definition.Fields) {
		return validation("collections."+snapshot.Name+".fields", "view_field_mismatch")
	}
	for index := range plan.Definition.Fields {
		provided, inferred := snapshot.Fields[index], &plan.Definition.Fields[index]
		if provided.Name != inferred.Name || provided.Type != inferred.Type || provided.Required != inferred.Required ||
			provided.Unique != inferred.Unique || provided.Indexed != inferred.Indexed || len(provided.Default) != 0 || provided.Relation != nil {
			return validation("collections."+snapshot.Name+".fields", "view_field_mismatch")
		}
		inferred.Options = FieldOptions{}
		if provided.Options != nil {
			inferred.Options = cloneFieldOptions(*provided.Options)
		}
		inferred.Hidden, inferred.Presentable, inferred.Help = provided.Hidden, provided.Presentable, provided.Help
	}
	return nil
}

func mappedMigrationFields(target, baseline []FieldInput, current []Field) ([]FieldInput, error) {
	baselineByID := migrationFieldMap(baseline)
	currentByName := make(map[string]Field, len(current))
	for _, field := range current {
		currentByName[field.Name] = field
	}
	result := cloneFieldInputs(target)
	for index := range result {
		logicalID := result[index].ID
		result[index].ID = nil
		if logicalID == nil {
			return nil, ErrMigrationSnapshotMismatch
		}
		name := result[index].Name
		if prior, exists := baselineByID[*logicalID]; exists {
			name = prior.Name
		}
		if actual, exists := currentByName[name]; exists {
			id := actual.ID
			result[index].ID = &id
			if result[index].Options == nil {
				result[index].Options = &FieldOptions{}
			}
		}
	}
	return result, nil
}

func orderedMigrationViews(snapshot MigrationSnapshot, reverse bool) []MigrationSnapshotCollection {
	views := make([]SchemaDocumentCollection, 0)
	byName := map[string]MigrationSnapshotCollection{}
	for _, item := range snapshot.Collections {
		if item.View == nil {
			continue
		}
		views = append(views, SchemaDocumentCollection{Name: item.Name, DependsOn: append([]string(nil), item.DependsOn...)})
		byName[item.Name] = item
	}
	ordered, err := orderImportedViews(views)
	if err != nil {
		return nil
	}
	result := make([]MigrationSnapshotCollection, 0, len(ordered))
	for _, item := range ordered {
		result = append(result, byName[item.Name])
	}
	if reverse {
		for left, right := 0, len(result)-1; left < right; left, right = left+1, right-1 {
			result[left], result[right] = result[right], result[left]
		}
	}
	return result
}

func countMigrationViews(snapshot MigrationSnapshot) int {
	count := 0
	for _, item := range snapshot.Collections {
		if item.View != nil {
			count++
		}
	}
	return count
}

func MigrationNameHint(operation, collection string) string {
	operation = strings.TrimPrefix(operation, "collection_")
	return operation + "_" + collection
}
