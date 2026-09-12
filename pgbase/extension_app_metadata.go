package pgbase

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/jackc/pgx/v5"
)

func (control *extensionAppControl) callDatabaseMetadata(ctx context.Context, method string, raw json.RawMessage) (json.RawMessage, error) {
	var request struct {
		Name, Query, Field, Filename string
		SampleSize                   int
	}
	if err := decodeExtensionInput(raw, &request); err != nil {
		return nil, err
	}
	schemaName, objectName := appTableParts(request.Name)
	if method != "app-control.databaseVacuum" && (schemaName != "app" || strings.TrimSpace(objectName) == "") {
		return nil, errors.New("only app schema objects are allowed")
	}
	switch method {
	case "app-control.databaseHasTable":
		var exists bool
		err := control.app.pool.QueryRow(ctx, `select exists(select 1 from information_schema.tables where table_schema=$1 and lower(table_name)=lower($2))`, schemaName, objectName).Scan(&exists)
		return marshalAppControl(exists, err)
	case "app-control.databaseTableInfo":
		var kind string
		err := control.app.pool.QueryRow(ctx, `select table_type from information_schema.tables where table_schema=$1 and table_name=$2`, schemaName, objectName).Scan(&kind)
		if errors.Is(err, pgx.ErrNoRows) {
			return json.RawMessage(`null`), nil
		}
		return marshalAppControl(map[string]any{"name": request.Name, "type": kind}, err)
	case "app-control.databaseDeleteTable":
		_, err := control.app.pool.Exec(ctx, "drop table if exists "+qualifiedAppIdentifier(request.Name))
		return marshalAppControl(map[string]bool{"deleted": err == nil}, err)
	case "app-control.databaseDeleteView":
		_, err := control.app.pool.Exec(ctx, "drop view if exists "+qualifiedAppIdentifier(request.Name))
		return marshalAppControl(map[string]bool{"deleted": err == nil}, err)
	case "app-control.databaseVacuum":
		_, err := control.app.pool.Exec(ctx, "vacuum analyze")
		return marshalAppControl(map[string]bool{"vacuumed": err == nil}, err)
	case "app-control.databaseSaveView":
		query, err := validatedViewQuery(request.Query)
		if err != nil {
			return nil, err
		}
		_, err = control.app.pool.Exec(ctx, "create or replace view "+qualifiedAppIdentifier(request.Name)+" as "+query)
		return marshalAppControl(map[string]bool{"saved": err == nil}, err)
	case "app-control.databaseCreateViewFields", "app-control.databaseDryRunView":
		query, err := validatedViewQuery(request.Query)
		if err != nil {
			return nil, err
		}
		rows, err := control.app.pool.Query(ctx, "select * from ("+query+") as pgbase_view_probe limit 0")
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		fields := make([]map[string]any, 0, len(rows.FieldDescriptions()))
		hasID := false
		for _, field := range rows.FieldDescriptions() {
			if field.Name == "id" {
				hasID = true
			}
			fields = append(fields, map[string]any{"name": string(field.Name), "dataTypeOID": field.DataTypeOID})
		}
		if !hasID {
			return nil, errors.New("view query must select an id column")
		}
		if method == "app-control.databaseCreateViewFields" {
			return json.Marshal(fields)
		}
		limit := request.SampleSize
		if limit <= 0 {
			limit = 1
		}
		if limit > 100 {
			return nil, errors.New("view sample limit exceeds 100")
		}
		sampleRows, err := control.app.pool.Query(ctx, "select * from ("+query+") as pgbase_view_sample limit $1", limit)
		if err != nil {
			return nil, err
		}
		defer sampleRows.Close()
		items := []map[string]any{}
		descriptions := sampleRows.FieldDescriptions()
		for sampleRows.Next() {
			values, err := sampleRows.Values()
			if err != nil {
				return nil, err
			}
			item := map[string]any{}
			for i, value := range values {
				item[string(descriptions[i].Name)] = value
			}
			items = append(items, item)
		}
		return json.Marshal(map[string]any{"fields": fields, "items": items})
	case "app-control.databaseFindRecordByViewFile":
		if strings.TrimSpace(request.Field) == "" || strings.TrimSpace(request.Filename) == "" {
			return nil, errors.New("view file field and filename are required")
		}
		field := pgx.Identifier{request.Field}.Sanitize()
		row := control.app.pool.QueryRow(ctx, "select row_to_json(v) from "+qualifiedAppIdentifier(request.Name)+" v where "+field+"=$1 limit 1", request.Filename)
		var value json.RawMessage
		if err := row.Scan(&value); err != nil {
			return nil, err
		}
		return value, nil
	}
	return nil, errors.New("unsupported database metadata operation")
}

func validatedViewQuery(query string) (string, error) {
	query = strings.TrimSpace(query)
	if !strings.HasPrefix(strings.ToLower(query), "select ") || strings.Contains(query, ";") {
		return "", errors.New("view query must be a single SELECT statement")
	}
	return query, nil
}
