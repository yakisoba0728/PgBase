package schema

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/yakisoba0728/PgBase/internal/database"
)

const SchemaDocumentFormat = "pgbase.schema/v1"

// SchemaDocument is a portable logical schema. Database IDs, physical names,
// timestamps and policy revisions are deliberately absent.
type SchemaDocument struct {
	Format      string                     `json:"format"`
	Collections []SchemaDocumentCollection `json:"collections"`
}

type SchemaDocumentCollection struct {
	Name              string          `json:"name"`
	CompatibilityName string          `json:"compatibilityName,omitempty"`
	Type              CollectionType  `json:"type,omitempty"`
	System            bool            `json:"system,omitempty"`
	ValidationMode    ValidationMode  `json:"validationMode,omitempty"`
	Fields            []FieldInput    `json:"fields,omitempty"`
	Indexes           []IndexInput    `json:"indexes,omitempty"`
	View              *ViewInput      `json:"view,omitempty"`
	DependsOn         []string        `json:"dependsOn,omitempty"`
	UserAccess        bool            `json:"userAccess"`
	Rules             json.RawMessage `json:"rules"`
}

type SchemaImportPlan struct {
	BaseVersion       int64          `json:"baseVersion"`
	Checksum          string         `json:"checksum"`
	PolicyChecksum    string         `json:"policyChecksum"`
	Changes           []Plan         `json:"changes"`
	Document          SchemaDocument `json:"document"`
	validationCatalog Catalog
}

type SchemaImportIntent struct {
	Document       SchemaDocument
	BaseVersion    int64
	Checksum       string
	Actor          uuid.UUID
	OperationID    uuid.UUID
	IdempotencyKey string
	ValidateRules  func(Collection, json.RawMessage) error
}

type SchemaImportResult struct {
	OperationID uuid.UUID `json:"operationId"`
	Status      string    `json:"status"`
	Version     int64     `json:"version,omitempty"`
	Applied     int       `json:"applied,omitempty"`
}

func (s *Service) ExportSchema(ctx context.Context) (SchemaDocument, error) {
	doc := SchemaDocument{Format: SchemaDocumentFormat}
	err := s.pool.InTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly}, func(tx pgx.Tx) error {
		store := NewStore(tx)
		version, err := store.SchemaVersion(ctx)
		if err != nil {
			return err
		}
		catalog, err := store.ReadCatalog(ctx, version)
		if err != nil {
			return err
		}
		names := make([]string, 0, len(catalog.Collections))
		for name := range catalog.Collections {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			collection := catalog.Collections[name]
			item, err := exportDocumentCollection(ctx, tx, catalog, collection)
			if err != nil {
				return err
			}
			doc.Collections = append(doc.Collections, item)
		}
		return nil
	})
	return doc, err
}

func exportDocumentCollection(ctx context.Context, tx pgx.Tx, catalog Catalog, c Collection) (SchemaDocumentCollection, error) {
	item := SchemaDocumentCollection{Name: c.Name, CompatibilityName: c.CompatibilityName, Type: c.Type, System: c.System, ValidationMode: c.ValidationMode, Rules: json.RawMessage(`{"list":{"op":"owner"},"view":{"op":"owner"},"create":{"op":"owner"},"update":{"op":"owner"},"delete":{"op":"owner"}}`)}
	for _, field := range c.Fields {
		system := field.System
		input := FieldInput{Name: field.Name, Type: field.Type, Required: field.Required, Unique: field.Unique, Indexed: field.Indexed, Hidden: field.Hidden, Presentable: field.Presentable, Help: field.Help, System: &system, Default: append(json.RawMessage(nil), field.Default...), Options: cloneFieldOptionsPointer(field.Options)}
		if field.Relation != nil {
			target, ok := collectionByID(catalog, field.Relation.TargetCollectionID)
			if !ok {
				return item, ErrCatalogInvalid
			}
			input.Relation = &RelationInput{TargetCollection: target.Name, Cardinality: field.Relation.Cardinality, MinSelect: intPointer(field.Relation.MinSelect), MaxSelect: intPointer(field.Relation.MaxSelect), OnDelete: field.Relation.OnDelete}
		}
		item.Fields = append(item.Fields, input)
	}
	for _, index := range c.Indexes {
		item.Indexes = append(item.Indexes, IndexInput{Name: index.Name, Fields: append([]string(nil), index.Fields...), Unique: index.Unique})
	}
	if c.View != nil {
		item.View = &ViewInput{Source: c.View.Source, Projection: append([]string(nil), c.View.Projection...), Filter: cloneViewFilter(c.View.Filter), SQL: c.View.SQL}
		for _, dependencyID := range c.View.Dependencies {
			if dependency, ok := collectionByID(catalog, dependencyID); ok {
				item.DependsOn = append(item.DependsOn, dependency.Name)
			}
		}
		if c.View.SQL == "" && c.View.Source != "" {
			item.DependsOn = append(item.DependsOn, c.View.Source)
		}
		sort.Strings(item.DependsOn)
	}
	if err := tx.QueryRow(ctx, `select coalesce((select enabled from pgbase_meta.collection_access where collection_id=$1),false)`, c.ID).Scan(&item.UserAccess); err != nil {
		return item, err
	}
	var rules []byte
	err := tx.QueryRow(ctx, `select rules from pgbase_meta.collection_rules where collection_id=$1`, c.ID).Scan(&rules)
	if err == nil {
		item.Rules = append(json.RawMessage(nil), rules...)
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return item, err
	}
	return item, nil
}

func cloneFieldOptionsPointer(options FieldOptions) *FieldOptions {
	cloned := cloneFieldOptions(options)
	if encoded, _ := json.Marshal(cloned); string(encoded) == "{}" {
		return nil
	}
	return &cloned
}

func (s *Service) PreviewSchemaImport(ctx context.Context, document SchemaDocument) (SchemaImportPlan, error) {
	var result SchemaImportPlan
	err := database.Retry(ctx, func() error {
		return s.pool.InTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead}, func(tx pgx.Tx) error {
			catalog, err := s.EnsureCurrent(ctx, tx)
			if err != nil {
				return err
			}
			result, err = s.planSchemaImport(ctx, tx, catalog, document)
			return err
		})
	}, nil)
	return result, err
}

func (s *Service) planSchemaImport(ctx context.Context, tx pgx.Tx, catalog Catalog, document SchemaDocument) (SchemaImportPlan, error) {
	if _, err := tx.Exec(ctx, "savepoint pgbase_schema_import_preview"); err != nil {
		return SchemaImportPlan{}, err
	}
	defer func() { _, _ = tx.Exec(ctx, "rollback to savepoint pgbase_schema_import_preview") }()
	if document.Format != SchemaDocumentFormat {
		return SchemaImportPlan{}, validation("format", "unsupported")
	}
	if len(document.Collections) == 0 {
		return SchemaImportPlan{}, validation("collections", "required")
	}
	document = cloneSchemaDocument(document)
	changes, err := importChanges(catalog, document)
	if err != nil {
		return SchemaImportPlan{}, err
	}
	companions, err := companionCapabilityReady(ctx, tx)
	if err != nil {
		return SchemaImportPlan{}, err
	}
	projected := cloneCatalog(catalog)
	plans := make([]Plan, 0, len(changes))
	for _, original := range changes {
		change := original
		if isSQLViewChange(change) {
			change, err = inspectSQLViewChange(ctx, tx, projected, change)
			if err != nil {
				return SchemaImportPlan{}, err
			}
		}
		plan, err := compilePlan(projected, change, companions)
		if err != nil {
			return SchemaImportPlan{}, err
		}
		if item, ok := documentCollection(document, plan.Definition.Name); ok {
			if err := applyImportedViewPresentation(&plan, item); err != nil {
				return SchemaImportPlan{}, err
			}
		}
		plans = append(plans, plan)
		for _, statement := range plan.Statements {
			if _, err := tx.Exec(ctx, statement); err != nil {
				return SchemaImportPlan{}, classifyDDLApplyError(fmt.Errorf("preview imported DDL: %w", err))
			}
		}
		projected, err = projectCatalog(projected, change, plan)
		if err != nil {
			return SchemaImportPlan{}, err
		}
	}
	if _, err := s.compileCatalog(projected); err != nil {
		return SchemaImportPlan{}, err
	}
	policyChecksum, err := schemaPolicyChecksum(ctx, tx, catalog, document)
	if err != nil {
		return SchemaImportPlan{}, err
	}
	checksum, err := schemaDocumentChecksum(catalog.Version, policyChecksum, document, plans)
	return SchemaImportPlan{BaseVersion: catalog.Version, Checksum: checksum, PolicyChecksum: policyChecksum, Changes: plans, Document: document, validationCatalog: projected}, err
}

func (s *Service) ValidateSchemaImportPlan(plan SchemaImportPlan, validate func(Collection, json.RawMessage) error) error {
	if validate == nil {
		return nil
	}
	for _, item := range plan.Document.Collections {
		collection, ok := plan.validationCatalog.Collections[item.Name]
		if !ok || validate(collection, item.Rules) != nil {
			return validation("collections."+item.Name+".rules", "invalid")
		}
	}
	return nil
}

func cloneSchemaDocument(document SchemaDocument) SchemaDocument {
	raw, _ := json.Marshal(document)
	var cloned SchemaDocument
	_ = json.Unmarshal(raw, &cloned)
	return cloned
}

func schemaDocumentChecksum(version int64, policyChecksum string, document SchemaDocument, _ []Plan) (string, error) {
	raw, err := json.Marshal(struct {
		Version        int64          `json:"version"`
		PolicyChecksum string         `json:"policyChecksum"`
		Document       SchemaDocument `json:"document"`
	}{version, policyChecksum, document})
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}

func schemaPolicyChecksum(ctx context.Context, tx pgx.Tx, catalog Catalog, document SchemaDocument) (string, error) {
	type state struct {
		Name     string          `json:"name"`
		Enabled  bool            `json:"enabled"`
		Rules    json.RawMessage `json:"rules,omitempty"`
		Revision int64           `json:"revision"`
	}
	values := make([]state, 0, len(document.Collections))
	for _, item := range document.Collections {
		collection, ok := catalog.Collections[item.Name]
		if !ok {
			continue
		}
		entry := state{Name: item.Name}
		if err := tx.QueryRow(ctx, `select coalesce((select enabled from pgbase_meta.collection_access where collection_id=$1),false)`, collection.ID).Scan(&entry.Enabled); err != nil {
			return "", err
		}
		var rules []byte
		err := tx.QueryRow(ctx, `select rules,revision from pgbase_meta.collection_rules where collection_id=$1`, collection.ID).Scan(&rules, &entry.Revision)
		if err == nil {
			entry.Rules = rules
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return "", err
		}
		values = append(values, entry)
	}
	sort.Slice(values, func(i, j int) bool { return values[i].Name < values[j].Name })
	raw, err := json.Marshal(values)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}

func importChanges(catalog Catalog, document SchemaDocument) ([]Change, error) {
	seen := map[string]struct{}{}
	base, views := []SchemaDocumentCollection{}, []SchemaDocumentCollection{}
	for i, item := range document.Collections {
		if _, ok := seen[item.Name]; ok {
			return nil, validation(fmt.Sprintf("collections.%d.name", i), "duplicate_name")
		}
		seen[item.Name] = struct{}{}
		if item.View == nil {
			base = append(base, item)
		} else {
			views = append(views, item)
		}
	}
	var err error
	views, err = orderImportedViews(views)
	if err != nil {
		return nil, err
	}
	changes := make([]Change, 0, len(document.Collections)*2)
	created := map[string]bool{}
	for _, item := range base {
		if _, exists := catalog.Collections[item.Name]; exists {
			continue
		}
		scalars := make([]FieldInput, 0, len(item.Fields))
		for _, field := range item.Fields {
			if field.Type != TypeRelation {
				scalars = append(scalars, field)
			}
		}
		changes = append(changes, NewCreateChange(CreateCollectionInput{Name: item.Name, CompatibilityName: item.CompatibilityName, Type: item.Type, System: item.System, ValidationMode: item.ValidationMode, Fields: scalars}))
		created[item.Name] = true
	}
	for _, item := range base {
		current, exists := catalog.Collections[item.Name]
		if exists && current.IsView() {
			return nil, validation("collections", "kind_conflict")
		}
		fields, err := importedFields(catalog, current, item.Fields)
		if err != nil {
			return nil, err
		}
		indexes := importedIndexes(current, item.Indexes)
		if exists && sameImportedBase(current, fields, indexes, catalog) {
			continue
		}
		typeValue, system, mode, compatibility := item.Type, item.System, item.ValidationMode, item.CompatibilityName
		changes = append(changes, NewUpdateChange(UpdateCollectionInput{Name: item.Name, CompatibilityName: &compatibility, Type: &typeValue, System: &system, ValidationMode: &mode, Fields: fields, Indexes: &indexes}))
	}
	for _, item := range views {
		if current, exists := catalog.Collections[item.Name]; exists {
			if current.IsSQLView() && item.View.SQL == current.View.SQL && sameViewPresentation(current, item.Fields) {
				continue
			}
			if !current.IsSQLView() || item.View.SQL == "" {
				if current.View != nil && item.View.SQL == "" && sameDeclarativeView(current.View, item.View) && sameViewPresentation(current, item.Fields) {
					continue
				}
				return nil, validation("collections", "view_update_unsupported")
			}
			changes = append(changes, NewUpdateChange(UpdateCollectionInput{Name: item.Name, View: item.View}))
		} else {
			changes = append(changes, NewCreateChange(CreateCollectionInput{Name: item.Name, CompatibilityName: item.CompatibilityName, Type: CollectionTypeView, System: item.System, ValidationMode: item.ValidationMode, View: item.View}))
		}
	}
	return changes, nil
}

func sameImportedBase(current Collection, fields []FieldInput, indexes []IndexInput, catalog Catalog) bool {
	if current.IsView() || len(current.Fields) != len(fields) || len(current.Indexes) != len(indexes) {
		return false
	}
	for i, old := range current.Fields {
		input := fields[i]
		if input.ID == nil || *input.ID != old.ID || input.Name != old.Name || input.Type != old.Type || input.Required != old.Required || input.Unique != old.Unique || input.Indexed != old.Indexed || input.Hidden != old.Hidden || input.Presentable != old.Presentable || input.Help != old.Help || !reflect.DeepEqual(input.Options, cloneFieldOptionsPointer(old.Options)) || string(input.Default) != string(old.Default) {
			return false
		}
		if (input.Relation == nil) != (old.Relation == nil) {
			return false
		}
		if old.Relation != nil {
			target, ok := collectionByID(catalog, old.Relation.TargetCollectionID)
			if !ok || input.Relation.TargetCollection != target.Name || input.Relation.Cardinality != old.Relation.Cardinality || pointerInt(input.Relation.MinSelect) != old.Relation.MinSelect || pointerInt(input.Relation.MaxSelect) != old.Relation.MaxSelect || input.Relation.OnDelete != old.Relation.OnDelete {
				return false
			}
		}
	}
	for i, old := range current.Indexes {
		if indexes[i].Name != old.Name || indexes[i].Unique != old.Unique || !reflect.DeepEqual(indexes[i].Fields, old.Fields) {
			return false
		}
	}
	return true
}

func pointerInt(value *int) int {
	if value == nil {
		return 0
	}
	return *value
}

func sameDeclarativeView(current *ViewSpec, imported *ViewInput) bool {
	return current.SQL == "" && current.Source == imported.Source && reflect.DeepEqual(current.Projection, imported.Projection) && reflect.DeepEqual(current.Filter, imported.Filter)
}

func sameViewPresentation(current Collection, imported []FieldInput) bool {
	if len(current.Fields) != len(imported) {
		return false
	}
	for i, field := range current.Fields {
		provided := imported[i]
		if provided.ID != nil || field.Name != provided.Name || field.Type != provided.Type || field.Required != provided.Required || field.Unique != provided.Unique || field.Indexed != provided.Indexed || string(field.Default) != string(provided.Default) || !reflect.DeepEqual(cloneFieldOptionsPointer(field.Options), provided.Options) || (field.Relation != nil) != (provided.Relation != nil) || field.Hidden != provided.Hidden || field.Presentable != provided.Presentable || field.Help != provided.Help {
			return false
		}
	}
	return true
}

func documentCollection(document SchemaDocument, name string) (SchemaDocumentCollection, bool) {
	for _, item := range document.Collections {
		if item.Name == name {
			return item, true
		}
	}
	return SchemaDocumentCollection{}, false
}

func applyImportedViewPresentation(plan *Plan, item SchemaDocumentCollection) error {
	if item.View == nil || len(item.Fields) == 0 {
		return nil
	}
	if len(item.Fields) != len(plan.Definition.Fields) {
		return validation("collections."+item.Name+".fields", "view_field_mismatch")
	}
	for i := range plan.Definition.Fields {
		provided, inferred := item.Fields[i], &plan.Definition.Fields[i]
		if provided.ID != nil || provided.Name != inferred.Name || provided.Type != inferred.Type || provided.Required != inferred.Required || provided.Unique != inferred.Unique || provided.Indexed != inferred.Indexed || string(provided.Default) != string(inferred.Default) || !reflect.DeepEqual(provided.Options, cloneFieldOptionsPointer(inferred.Options)) || (provided.Relation != nil) != (inferred.Relation != nil) {
			return validation("collections."+item.Name+".fields", "view_field_mismatch")
		}
		inferred.Hidden, inferred.Presentable, inferred.Help = provided.Hidden, provided.Presentable, provided.Help
	}
	return nil
}

func orderImportedViews(views []SchemaDocumentCollection) ([]SchemaDocumentCollection, error) {
	remaining := append([]SchemaDocumentCollection(nil), views...)
	viewNames := map[string]bool{}
	for _, view := range views {
		viewNames[view.Name] = true
	}
	ready := map[string]bool{}
	result := make([]SchemaDocumentCollection, 0, len(views))
	for len(remaining) > 0 {
		progress := false
		for i := 0; i < len(remaining); {
			blocked := false
			for _, dependency := range remaining[i].DependsOn {
				if viewNames[dependency] && !ready[dependency] {
					blocked = true
					break
				}
			}
			if blocked {
				i++
				continue
			}
			result = append(result, remaining[i])
			ready[remaining[i].Name] = true
			remaining = append(remaining[:i], remaining[i+1:]...)
			progress = true
		}
		if !progress {
			return nil, validation("collections", "cyclic_view_dependency")
		}
	}
	return result, nil
}

func importedFields(catalog Catalog, current Collection, fields []FieldInput) ([]FieldInput, error) {
	ids := map[string]uuid.UUID{}
	existing := map[string]Field{}
	for _, field := range current.Fields {
		ids[field.Name] = field.ID
		existing[field.Name] = field
	}
	result := cloneFieldInputs(fields)
	seen := map[string]bool{}
	for i := range result {
		if old, ok := existing[result[i].Name]; ok && old.Type != result[i].Type {
			return nil, validation("fields."+result[i].Name+".type", "type_conflict")
		}
		if id := ids[result[i].Name]; id != uuid.Nil {
			result[i].ID = &id
		}
		seen[result[i].Name] = true
	}
	for _, field := range current.Fields {
		if seen[field.Name] {
			continue
		}
		id := field.ID
		system := field.System
		input := FieldInput{ID: &id, Name: field.Name, Type: field.Type, Required: field.Required, Unique: field.Unique, Indexed: field.Indexed, Hidden: field.Hidden, Presentable: field.Presentable, Help: field.Help, System: &system, Default: append(json.RawMessage(nil), field.Default...), Options: cloneFieldOptionsPointer(field.Options)}
		if field.Relation != nil {
			target, ok := collectionByID(catalog, field.Relation.TargetCollectionID)
			if !ok {
				return nil, ErrCatalogInvalid
			}
			input.Relation = &RelationInput{TargetCollection: target.Name, Cardinality: field.Relation.Cardinality, MinSelect: intPointer(field.Relation.MinSelect), MaxSelect: intPointer(field.Relation.MaxSelect), OnDelete: field.Relation.OnDelete}
		}
		result = append(result, input)
	}
	return result, nil
}

func importedIndexes(current Collection, imported []IndexInput) []IndexInput {
	result := append([]IndexInput(nil), imported...)
	seen := map[string]bool{}
	for _, index := range result {
		seen[index.Name] = true
	}
	for _, index := range current.Indexes {
		if !seen[index.Name] {
			result = append(result, IndexInput{Name: index.Name, Fields: append([]string(nil), index.Fields...), Unique: index.Unique})
		}
	}
	return result
}

func (s *Service) ApplySchemaImport(ctx context.Context, intent SchemaImportIntent) (result SchemaImportResult, err error) {
	if intent.Actor == uuid.Nil || intent.OperationID == uuid.Nil || !validOperationIdempotencyKey(intent.IdempotencyKey) {
		return result, validation("actor", "required")
	}
	if decoded, decodeErr := hex.DecodeString(intent.Checksum); decodeErr != nil || len(decoded) != 32 {
		return result, ErrConflict
	}
	requestWire, err := json.Marshal(struct {
		Document    SchemaDocument `json:"document"`
		BaseVersion int64          `json:"baseVersion"`
		Checksum    string         `json:"checksum"`
	}{intent.Document, intent.BaseVersion, intent.Checksum})
	if err != nil {
		return result, err
	}
	requestSum := sha256.Sum256(requestWire)
	requestHash := requestSum[:]
	idempotencyHash := sha256.Sum256([]byte(intent.IdempotencyKey))
	claimTag, err := s.pool.Exec(ctx, `insert into pgbase_meta.schema_import_operations(operation_id,administrator_id,idempotency_hash,request_hash,base_version,status) values($1,$2,$3,$4,$5,'running') on conflict do nothing`, intent.OperationID, intent.Actor, idempotencyHash[:], requestHash, intent.BaseVersion)
	if err != nil {
		return result, err
	}
	ownsClaim := claimTag.RowsAffected() == 1
	var prepared *compiledCatalog
	var migrationBefore []byte
	var lifecycleEvents []*CollectionMutationEvent
	var lifecycleFinishes []func(error)
	finishLifecycle := func(finalErr error) {
		for _, finish := range lifecycleFinishes {
			finish(finalErr)
		}
	}
	err = s.pool.InTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead}, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `select pg_advisory_xact_lock($1)`, schemaImportLockKey(intent.OperationID)); err != nil {
			return err
		}
		var actor uuid.UUID
		var storedID, storedRequest []byte
		var status string
		var committed *int64
		var applied int
		if err := tx.QueryRow(ctx, `select administrator_id,idempotency_hash,request_hash,status,committed_version,applied from pgbase_meta.schema_import_operations where operation_id=$1 for update`, intent.OperationID).Scan(&actor, &storedID, &storedRequest, &status, &committed, &applied); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrConflict
			}
			return err
		}
		if actor != intent.Actor || string(storedID) != string(idempotencyHash[:]) || string(storedRequest) != string(requestHash) {
			return ErrConflict
		}
		if status == "committed" && committed != nil {
			result = SchemaImportResult{OperationID: intent.OperationID, Status: status, Version: *committed, Applied: applied}
			return nil
		}
		if status == "failed" {
			return ErrConflict
		}
		if _, err := tx.Exec(ctx, "select pg_advisory_xact_lock($1)", schemaAdvisoryLockKey); err != nil {
			return err
		}
		store := NewStore(tx)
		version, err := store.LockedSchemaVersion(ctx)
		if err != nil {
			return err
		}
		if version != intent.BaseVersion {
			return ErrConflict
		}
		catalog, err := store.ReadCatalog(ctx, version)
		if err != nil {
			return err
		}
		if s.migrationRecorder != nil {
			migrationBefore, err = ExportMigrationSnapshot(ctx, tx)
			if err != nil {
				return fmt.Errorf("snapshot schema before automatic migration recording: %w", err)
			}
		}
		for _, item := range intent.Document.Collections {
			if collection, ok := catalog.Collections[item.Name]; ok {
				if _, err := tx.Exec(ctx, `select id from pgbase_meta.collections where id=$1 for update`, collection.ID); err != nil {
					return err
				}
				if _, err := tx.Exec(ctx, `select 1 from pgbase_meta.collection_access where collection_id=$1 for update`, collection.ID); err != nil {
					return err
				}
				if _, err := tx.Exec(ctx, `select 1 from pgbase_meta.collection_rules where collection_id=$1 for update`, collection.ID); err != nil {
					return err
				}
			}
		}
		planned, err := s.planSchemaImport(ctx, tx, catalog, intent.Document)
		if err != nil {
			return err
		}
		if planned.Checksum != intent.Checksum {
			return ErrConflict
		}
		if err := s.refreshSchemaWriterLease(ctx, tx); err != nil {
			return err
		}
		changes, err := importChanges(catalog, planned.Document)
		if err != nil {
			return err
		}
		companions, err := companionCapabilityReady(ctx, tx)
		if err != nil {
			return err
		}
		hookedCollections := map[string]bool{}
		for _, change := range changes {
			if isSQLViewChange(change) {
				change, err = inspectSQLViewChange(ctx, tx, catalog, change)
				if err != nil {
					return err
				}
			}
			currentPlan, err := compilePlan(catalog, change, companions)
			if err != nil {
				return err
			}
			if item, ok := documentCollection(planned.Document, currentPlan.Definition.Name); ok {
				if err := applyImportedViewPresentation(&currentPlan, item); err != nil {
					return err
				}
			}
			if s.collectionLifecycle != nil && !hookedCollections[currentPlan.Definition.Name] {
				event := collectionMutationEvent(ApplyIntent{change: change, actor: intent.Actor})
				finish, hookErr := s.collectionLifecycle.BeginCollectionMutation(ctx, event)
				lifecycleEvents = append(lifecycleEvents, event)
				lifecycleFinishes = append(lifecycleFinishes, finish)
				hookedCollections[event.Name] = true
				if hookErr != nil {
					return hookErr
				}
			}
			if err := s.applyImportedChange(ctx, tx, store, catalog, change, currentPlan, intent.Actor); err != nil {
				return err
			}
			catalog, err = store.ReadCatalog(ctx, catalog.Version+1)
			if err != nil {
				return err
			}
		}
		for _, item := range planned.Document.Collections {
			if s.collectionLifecycle != nil && !hookedCollections[item.Name] {
				event := &CollectionMutationEvent{Operation: "update", Name: item.Name, ActorID: intent.Actor}
				finish, hookErr := s.collectionLifecycle.BeginCollectionMutation(ctx, event)
				lifecycleEvents = append(lifecycleEvents, event)
				lifecycleFinishes = append(lifecycleFinishes, finish)
				hookedCollections[item.Name] = true
				if hookErr != nil {
					return hookErr
				}
			}
			collection := catalog.Collections[item.Name]
			if intent.ValidateRules != nil && intent.ValidateRules(collection, item.Rules) != nil {
				return validation("collections."+item.Name+".rules", "invalid")
			}
			if _, err := tx.Exec(ctx, `insert into pgbase_meta.collection_access(collection_id,enabled) values($1,$2) on conflict(collection_id) do update set enabled=excluded.enabled`, collection.ID, item.UserAccess); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `insert into pgbase_meta.collection_rules(collection_id,rules) values($1,$2::jsonb) on conflict(collection_id) do update set rules=excluded.rules,revision=pgbase_meta.collection_rules.revision+1,updated_at=clock_timestamp()`, collection.ID, string(item.Rules)); err != nil {
				return err
			}
		}
		persisted, err := store.ReadCatalog(ctx, catalog.Version)
		if err != nil {
			return err
		}
		prepared, err = s.cache.prepare(persisted, s.catalogLimit())
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, "select pg_notify('pgbase_schema_changed',$1)", fmt.Sprint(catalog.Version)); err != nil {
			return err
		}
		if s.migrationRecorder != nil && len(changes) != 0 {
			migrationAfter, snapshotErr := ExportMigrationSnapshot(ctx, tx)
			if snapshotErr != nil {
				return fmt.Errorf("snapshot schema after automatic migration recording: %w", snapshotErr)
			}
			var committedAt time.Time
			if scanErr := tx.QueryRow(ctx, `select clock_timestamp()`).Scan(&committedAt); scanErr != nil {
				return scanErr
			}
			if recordErr := s.migrationRecorder.RecordSchemaMigration(ctx, tx, MigrationRecording{
				OperationID: intent.OperationID, Name: "schema_import", Before: migrationBefore,
				After: migrationAfter, CommittedAt: committedAt.UTC(),
			}); recordErr != nil {
				return fmt.Errorf("record automatic user migration: %w", recordErr)
			}
		}
		if _, err := tx.Exec(ctx, `update pgbase_meta.schema_import_operations set status='committed',committed_version=$2,applied=$3,updated_at=clock_timestamp(),completed_at=clock_timestamp() where operation_id=$1`, intent.OperationID, catalog.Version, len(changes)); err != nil {
			return err
		}
		result = SchemaImportResult{OperationID: intent.OperationID, Status: "committed", Version: catalog.Version, Applied: len(changes)}
		return nil
	})
	if err != nil {
		s.cache.discardPrepared(prepared)
		if ownsClaim {
			s.finalizeSchemaImportFailure(ctx, intent, idempotencyHash[:], requestHash)
		}
		finishLifecycle(err)
		return result, err
	}
	s.cache.publish(prepared)
	s.materializeSchemaMigration(ctx, intent.OperationID)
	if prepared != nil {
		for _, event := range lifecycleEvents {
			if collection, ok := prepared.catalog.Collections[event.Name]; ok {
				event.Result.Collection = collection
			}
		}
	}
	finishLifecycle(nil)
	return result, nil
}

func (s *Service) applyImportedChange(ctx context.Context, tx pgx.Tx, store *Store, catalog Catalog, change Change, plan Plan, actor uuid.UUID) error {
	now := time.Now().UTC()
	var collection Collection
	var current Collection
	var err error
	switch change.kind {
	case changeCreate:
		collection, err = newCollectionMetadata(plan.Definition)
		if err == nil {
			collection.CreatedAt = now
		}
	case changeUpdate:
		current = catalog.Collections[change.update.Name]
		if err := validateRequiredRelationAdds(ctx, tx, current, plan.Definition, true); err != nil {
			return err
		}
		fields, fieldErr := updatedFieldMetadata(current.ID, current.Fields, plan.Definition)
		if fieldErr != nil {
			return fieldErr
		}
		indexes, indexErr := updatedIndexMetadata(current.Name, current.Indexes, plan.Definition)
		if indexErr != nil {
			return indexErr
		}
		collection = Collection{ID: current.ID, Name: current.Name, TableName: current.TableName, Fields: fields, Indexes: indexes, View: resolvedViewSpec(plan.Definition.View), CreatedAt: current.CreatedAt}
	default:
		return ErrConflict
	}
	if err != nil {
		return err
	}
	if change.kind == changeUpdate {
		if err := validateChangedFieldOptions(ctx, tx, current, collection); err != nil {
			return err
		}
	}
	if err := s.executePlan(ctx, tx, plan); err != nil {
		return err
	}
	if change.kind == changeUpdate {
		if err := validateChangedRecordSizes(ctx, tx, current, collection); err != nil {
			return err
		}
	}
	if change.kind == changeCreate {
		err = store.InsertCollection(ctx, collection, plan.Definition, now)
	} else {
		err = store.UpdateCollection(ctx, collection, plan.Definition, now)
	}
	if err != nil {
		return err
	}
	operationID := uuid.New()
	checksum, _ := hex.DecodeString(plan.Checksum)
	if err := store.InsertMigration(ctx, catalog.Version+1, operationID, plan, checksum, actor, now, 0); err != nil {
		return err
	}
	version, err := store.IncrementVersion(ctx)
	if err != nil || version != catalog.Version+1 {
		return ErrConflict
	}
	return nil
}
