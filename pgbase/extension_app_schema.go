package pgbase

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/yakisoba0728/PgBase/internal/accessrule"
	"github.com/yakisoba0728/PgBase/internal/identity"
	"github.com/yakisoba0728/PgBase/internal/recordops"
	"github.com/yakisoba0728/PgBase/internal/schema"
)

func (control *extensionAppControl) schemaService() (*schema.Service, error) {
	service, ok := control.app.schemaOperations.(*schema.Service)
	if !ok || service == nil {
		return nil, errors.New("schema service is unavailable")
	}
	return service, nil
}

func (control *extensionAppControl) callSchemaDatabase(ctx context.Context, method string, raw json.RawMessage, actor uuid.UUID, requestID string) (json.RawMessage, error) {
	service, err := control.schemaService()
	if err != nil {
		return nil, extensionError("schema_unavailable", err)
	}
	switch method {
	case "app-control.schemaExport":
		document, err := service.ExportSchema(ctx)
		return marshalAppControl(document, err)
	case "app-control.schemaPreview":
		var request struct {
			Document schema.SchemaDocument `json:"document"`
		}
		if err := decodeExtensionInput(raw, &request); err != nil {
			return nil, err
		}
		plan, err := service.PreviewSchemaImport(ctx, request.Document)
		return marshalAppControl(plan, err)
	case "app-control.schemaApply":
		var request extensionSchemaApplyRequest
		if err := decodeExtensionInput(raw, &request); err != nil {
			return nil, err
		}
		return control.applySchema(ctx, service, request, actor, requestID)
	case "app-control.collectionSave", "app-control.collectionSaveNoValidate", "app-control.collectionValidate", "app-control.collectionDelete", "app-control.collectionSync":
		return control.mutateCollection(ctx, service, method, raw, actor, requestID)
	case "app-control.collectionTruncate":
		return control.truncateCollection(ctx, raw, actor, requestID)
	case "app-control.databaseTables", "app-control.databaseColumns", "app-control.databaseIndexes":
		return control.introspectDatabase(ctx, service, method, raw)
	case "app-control.databaseHasTable", "app-control.databaseTableInfo", "app-control.databaseDeleteTable", "app-control.databaseDeleteView", "app-control.databaseSaveView", "app-control.databaseCreateViewFields", "app-control.databaseDryRunView", "app-control.databaseFindRecordByViewFile", "app-control.databaseVacuum":
		return control.callDatabaseMetadata(ctx, method, raw)
	case "app-control.databaseTransaction":
		host := &extensionHost{bridge: &extensionBridge{database: &TrustedDatabase{pool: control.app.pool}}}
		return host.callDatabase(ctx, raw)
	default:
		return nil, extensionError("unsupported_app_schema", errors.New("unsupported schema/database operation"))
	}
}

type extensionSchemaApplyRequest struct {
	Document       schema.SchemaDocument `json:"document"`
	BaseVersion    int64                 `json:"baseVersion"`
	Checksum       string                `json:"checksum"`
	OperationID    string                `json:"operationId,omitempty"`
	IdempotencyKey string                `json:"idempotencyKey,omitempty"`
}

func (control *extensionAppControl) applySchema(ctx context.Context, service *schema.Service, request extensionSchemaApplyRequest, actor uuid.UUID, requestID string) (json.RawMessage, error) {
	operationID, err := uuid.Parse(request.OperationID)
	if request.OperationID == "" {
		operationID = uuid.New()
	} else if err != nil {
		return nil, extensionError("invalid_schema_operation", err)
	}
	if request.IdempotencyKey == "" {
		sum := sha256.Sum256([]byte(requestID + "\x00" + operationID.String()))
		request.IdempotencyKey = "js-" + hex.EncodeToString(sum[:])
	}
	result, err := service.ApplySchemaImport(ctx, schema.SchemaImportIntent{Document: request.Document, BaseVersion: request.BaseVersion, Checksum: request.Checksum, Actor: actor, OperationID: operationID, IdempotencyKey: request.IdempotencyKey, ValidateRules: validateExtensionCollectionRules})
	return marshalAppControl(result, err)
}

func validateExtensionCollectionRules(collection schema.Collection, raw json.RawMessage) error {
	rules, err := accessrule.Parse(raw)
	if err != nil {
		return err
	}
	for _, action := range []string{"list", "view", "create", "update", "delete"} {
		rule, _ := rules.Action(action)
		if _, _, err := accessrule.Compile(rule, collection, collection.TableName, identity.Subject{Kind: identity.Administrator, ID: uuid.New()}, 0, action == "create"); err != nil {
			return err
		}
	}
	return nil
}

func (control *extensionAppControl) mutateCollection(ctx context.Context, service *schema.Service, method string, raw json.RawMessage, actor uuid.UUID, requestID string) (json.RawMessage, error) {
	if method == "app-control.collectionSaveNoValidate" {
		ctx = context.WithValue(ctx, skipJavaScriptSchemaHooksKey{}, true)
	}
	var request struct {
		Collection schema.SchemaDocumentCollection `json:"collection"`
	}
	if err := decodeExtensionInput(raw, &request); err != nil {
		return nil, err
	}
	document, err := service.ExportSchema(ctx)
	if err != nil {
		return nil, err
	}
	document.Format = schema.SchemaDocumentFormat
	found := -1
	for i, item := range document.Collections {
		if item.Name == request.Collection.Name {
			found = i
			break
		}
	}
	if method == "app-control.collectionDelete" {
		if found < 0 {
			return nil, extensionError("collection_not_found", schema.ErrCollectionNotFound)
		}
		document.Collections = append(document.Collections[:found], document.Collections[found+1:]...)
	} else if found >= 0 {
		document.Collections[found] = request.Collection
	} else {
		document.Collections = append(document.Collections, request.Collection)
	}
	plan, err := service.PreviewSchemaImport(ctx, document)
	if err != nil {
		return nil, extensionError("schema_preview_failed", err)
	}
	if method == "app-control.collectionValidate" {
		return json.Marshal(plan)
	}
	return control.applySchema(ctx, service, extensionSchemaApplyRequest{Document: document, BaseVersion: plan.BaseVersion, Checksum: plan.Checksum}, actor, requestID)
}

func (control *extensionAppControl) truncateCollection(ctx context.Context, raw json.RawMessage, actor uuid.UUID, requestID string) (json.RawMessage, error) {
	var request struct {
		Name string `json:"name"`
	}
	if err := decodeExtensionInput(raw, &request); err != nil {
		return nil, err
	}
	if control.app.records == nil {
		return nil, extensionError("collection_truncate_failed", errors.New("record service is unavailable"))
	}
	result, err := control.app.records.Truncate(ctx, recordops.TruncateRequest{Principal: identity.Subject{Kind: identity.Administrator, ID: actor}, RequestID: requestID, Collection: request.Name})
	return marshalAppControl(result, err)
}

func (control *extensionAppControl) introspectDatabase(ctx context.Context, service *schema.Service, method string, raw json.RawMessage) (json.RawMessage, error) {
	catalog, err := service.Catalog(ctx)
	if err != nil {
		return nil, err
	}
	if method == "app-control.databaseTables" {
		names := make([]string, 0, len(catalog.Collections))
		for name := range catalog.Collections {
			names = append(names, name)
		}
		sort.Strings(names)
		items := make([]map[string]any, 0, len(catalog.Collections))
		for _, name := range names {
			c := catalog.Collections[name]
			items = append(items, map[string]any{"name": name, "table": c.TableName, "type": c.Type})
		}
		return json.Marshal(items)
	}
	var request struct {
		Name string `json:"name"`
	}
	if err := decodeExtensionInput(raw, &request); err != nil {
		return nil, err
	}
	collection, ok := catalog.Collections[request.Name]
	table := request.Name
	if ok {
		table = collection.TableName
	}
	schemaName, tableName := appTableParts(table)
	if schemaName != "app" {
		return nil, extensionError("database_object_denied", errors.New("only app schema objects are allowed"))
	}
	if method == "app-control.databaseColumns" {
		rows, err := control.app.pool.Query(ctx, `select column_name,data_type,is_nullable,column_default from information_schema.columns where table_schema=$1 and table_name=$2 order by ordinal_position`, schemaName, tableName)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		items := []map[string]any{}
		for rows.Next() {
			var name, dataType, nullable string
			var defaultValue *string
			if err := rows.Scan(&name, &dataType, &nullable, &defaultValue); err != nil {
				return nil, err
			}
			items = append(items, map[string]any{"name": name, "dataType": dataType, "nullable": nullable == "YES", "default": defaultValue})
		}
		if err := rows.Err(); err != nil {
			return nil, err
		}
		return json.Marshal(items)
	}
	rows, err := control.app.pool.Query(ctx, `select indexname,indexdef from pg_indexes where schemaname=$1 and tablename=$2 order by indexname`, schemaName, tableName)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []map[string]string{}
	for rows.Next() {
		var name, definition string
		if err := rows.Scan(&name, &definition); err != nil {
			return nil, err
		}
		items = append(items, map[string]string{"name": name, "definition": definition})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return json.Marshal(items)
}

func marshalAppControl(value any, err error) (json.RawMessage, error) {
	if err != nil {
		return nil, extensionError("app_schema_failed", err)
	}
	return json.Marshal(value)
}

func qualifiedAppIdentifier(name string) string {
	schemaName, tableName := appTableParts(name)
	return pgx.Identifier{schemaName, tableName}.Sanitize()
}

func appTableParts(name string) (string, string) {
	parts := strings.SplitN(name, ".", 2)
	if len(parts) == 2 {
		return parts[0], parts[1]
	}
	return "app", name
}
