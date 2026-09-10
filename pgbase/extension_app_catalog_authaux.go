package pgbase

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/google/uuid"
	"github.com/yakisoba0728/PgBase/internal/identity"
	"github.com/yakisoba0728/PgBase/internal/userauth"
)

type catalogCollectionDTO struct {
	ID     uuid.UUID `json:"id"`
	Name   string    `json:"name"`
	Type   string    `json:"type"`
	Fields []string  `json:"fields,omitempty"`
}

func catalogDTO(c interface{}) catalogCollectionDTO {
	switch v := c.(type) {
	case userauth.AuthCollection:
		return catalogCollectionDTO{ID: v.ID, Name: v.Name, Type: "auth"}
	default:
		_ = v
	}
	return catalogCollectionDTO{}
}

type adminAuthAuxService interface {
	AdminListAuthAuxiliary(context.Context, identity.Subject, uuid.UUID, uuid.UUID, string) ([]userauth.AuthAuxiliaryRecord, error)
	AdminDeleteAuthAuxiliary(context.Context, identity.Subject, uuid.UUID, uuid.UUID, string, uuid.UUID) error
	AdminQueryAuthAuxiliary(context.Context, identity.Subject, userauth.AuthAuxiliaryQuery) ([]userauth.AuthAuxiliaryRecord, error)
	AdminDeleteAuthAuxiliaryScope(context.Context, identity.Subject, userauth.AuthAuxiliaryQuery, bool) error
	AdminFindAuthRecordByToken(context.Context, identity.Subject, string, []string) (userauth.AuthRecord, error)
	AdminFindAuthRecordByEmail(context.Context, identity.Subject, uuid.UUID, string) (userauth.AuthRecord, error)
}

func (host *extensionHost) requireAppAdministrator() error {
	if host.principal.Kind != identity.Administrator || host.principal.ID == uuid.Nil || host.bridge.appControl == nil || host.bridge.appControl.app == nil {
		return extensionError("app_control_denied", errors.New("administrator authority is required"))
	}
	return nil
}
func (host *extensionHost) callAppCatalogAuthAux(ctx context.Context, method string, raw json.RawMessage) (json.RawMessage, error) {
	if err := host.requireAppAdministrator(); err != nil {
		return nil, err
	}
	switch method {
	case "app.collection.get", "app.collection.cachedGet":
		var q struct {
			Name string `json:"name"`
		}
		if err := decodeExtensionInput(raw, &q); err != nil {
			return nil, err
		}
		for _, c := range host.bridge.appControl.app.schemaCache.Load().Collections {
			if c.Name == q.Name || c.ID.String() == q.Name {
				return json.Marshal(c)
			}
		}
		if lookup, ok := host.bridge.authMail.(authMailCollectionLookup); ok {
			if c, err := lookup.GetAuthCollection(ctx, q.Name); err == nil {
				return json.Marshal(map[string]any{"id": c.ID, "name": c.Name, "type": "auth", "fields": c.Fields})
			}
		}
		if service, ok := host.bridge.authMail.(interface {
			ListAuthCollections(context.Context) ([]userauth.AuthCollection, error)
		}); ok {
			if values, e := service.ListAuthCollections(ctx); e == nil {
				for _, c := range values {
					if c.ID.String() == q.Name {
						return json.Marshal(map[string]any{"id": c.ID, "name": c.Name, "type": "auth", "fields": c.Fields})
					}
				}
			}
		}
		return nil, extensionError("not_found", errors.New("collection not found"))
	case "app.collection.list":
		var q struct {
			Type string `json:"type"`
		}
		if err := decodeExtensionInput(raw, &q); err != nil {
			return nil, err
		}
		out := make([]map[string]any, 0)
		for _, c := range host.bridge.appControl.app.schemaCache.Load().Collections {
			if q.Type == "" || string(c.Type) == q.Type {
				fields := make([]string, 0, len(c.Fields))
				for _, f := range c.Fields {
					fields = append(fields, f.Name)
				}
				out = append(out, map[string]any{"id": c.ID, "name": c.Name, "type": c.Type, "fields": fields})
			}
		}
		if q.Type == "" || q.Type == "auth" {
			if service, ok := host.bridge.authMail.(interface {
				ListAuthCollections(context.Context) ([]userauth.AuthCollection, error)
			}); ok {
				if values, e := service.ListAuthCollections(ctx); e == nil {
					for _, c := range values {
						out = append(out, map[string]any{"id": c.ID, "name": c.Name, "type": "auth"})
					}
				}
			}
		}
		return json.Marshal(map[string]any{"items": out})
	case "app.collection.nameUnique":
		var q struct{ Name, ExcludeID string }
		if err := decodeExtensionInput(raw, &q); err != nil {
			return nil, err
		}
		for _, c := range host.bridge.appControl.app.schemaCache.Load().Collections {
			if strings.EqualFold(c.Name, q.Name) && c.ID.String() != q.ExcludeID {
				return json.RawMessage(`false`), nil
			}
		}
		if lookup, ok := host.bridge.authMail.(authMailCollectionLookup); ok {
			if c, err := lookup.GetAuthCollection(ctx, q.Name); err == nil && c.ID.String() != q.ExcludeID {
				return json.RawMessage(`false`), nil
			}
		}
		return json.RawMessage(`true`), nil
	case "app.collection.references":
		var q struct {
			Name string `json:"name"`
		}
		if err := decodeExtensionInput(raw, &q); err != nil {
			return nil, err
		}
		var target uuid.UUID
		for _, c := range host.bridge.appControl.app.schemaCache.Load().Collections {
			if c.Name == q.Name || c.ID.String() == q.Name {
				target = c.ID
			}
		}
		if target == uuid.Nil {
			return nil, extensionError("not_found", errors.New("collection not found"))
		}
		out := make([]map[string]any, 0)
		for _, c := range host.bridge.appControl.app.schemaCache.Load().Collections {
			for _, f := range c.Fields {
				if f.Relation != nil && f.Relation.TargetCollectionID == target {
					out = append(out, map[string]any{"collectionId": c.ID, "collectionName": c.Name, "field": f.Name})
				}
			}
		}
		return json.Marshal(out)
	case "app.authAux.list":
		var q struct{ Collection, RecordID, Kind string }
		if err := decodeExtensionInput(raw, &q); err != nil {
			return nil, err
		}
		realm, e := uuid.Parse(q.Collection)
		if e != nil {
			return nil, extensionError("invalid_auth_aux", e)
		}
		id, e := uuid.Parse(q.RecordID)
		if e != nil {
			return nil, extensionError("invalid_auth_aux", e)
		}
		s, ok := host.bridge.authMail.(adminAuthAuxService)
		if !ok {
			return nil, extensionError("auth_aux_unavailable", nil)
		}
		out, e := s.AdminListAuthAuxiliary(ctx, host.principal, realm, id, q.Kind)
		return marshalExtensionResult(map[string]any{"items": out}, e)
	case "app.authAux.delete":
		var q struct{ Collection, RecordID, Kind, ID string }
		if err := decodeExtensionInput(raw, &q); err != nil {
			return nil, err
		}
		realm, e := uuid.Parse(q.Collection)
		if e != nil {
			return nil, extensionError("invalid_auth_aux", e)
		}
		record, e := uuid.Parse(q.RecordID)
		if e != nil {
			return nil, extensionError("invalid_auth_aux", e)
		}
		id, e := uuid.Parse(q.ID)
		if e != nil {
			return nil, extensionError("invalid_auth_aux", e)
		}
		s, ok := host.bridge.authMail.(adminAuthAuxService)
		if !ok {
			return nil, extensionError("auth_aux_unavailable", nil)
		}
		e = s.AdminDeleteAuthAuxiliary(ctx, host.principal, realm, record, q.Kind, id)
		return marshalExtensionResult(map[string]bool{"deleted": true}, e)
	case "app.authAux.query":
		var q struct {
			Kind, Collection, RecordID, ID, Provider, Fingerprint string
			Expr                                                  json.RawMessage
		}
		if err := decodeExtensionInput(raw, &q); err != nil {
			return nil, err
		}
		if len(q.Expr) > 0 && string(q.Expr) != "null" {
			var expr struct {
				Op   string            `json:"op"`
				Args []json.RawMessage `json:"args"`
			}
			if json.Unmarshal(q.Expr, &expr) != nil || expr.Op != "hashExp" || len(expr.Args) != 1 {
				return nil, extensionError("invalid_auth_aux_expression", errors.New("only parameterized hashExp is supported"))
			}
			var values map[string]json.RawMessage
			if json.Unmarshal(expr.Args[0], &values) != nil || len(values) == 0 || len(values) > 4 {
				return nil, extensionError("invalid_auth_aux_expression", nil)
			}
			for key, value := range values {
				var text string
				if json.Unmarshal(value, &text) != nil {
					return nil, extensionError("invalid_auth_aux_expression", nil)
				}
				switch key {
				case "id":
					q.ID = text
				case "provider":
					q.Provider = text
				case "recordId", "userId":
					q.RecordID = text
				case "collection", "collectionId":
					q.Collection = text
				default:
					return nil, extensionError("invalid_auth_aux_expression", errors.New("unsupported expression field"))
				}
			}
		}
		request := userauth.AuthAuxiliaryQuery{Kind: q.Kind, Provider: q.Provider, Fingerprint: q.Fingerprint}
		var e error
		if q.Collection != "" {
			request.CollectionID, e = host.resolveAuthMailCollection(ctx, q.Collection)
			if e != nil {
				return nil, extensionError("invalid_auth_aux", e)
			}
		}
		if q.RecordID != "" {
			request.UserID, e = uuid.Parse(q.RecordID)
			if e != nil {
				return nil, extensionError("invalid_auth_aux", e)
			}
		}
		if q.ID != "" {
			request.ID, e = uuid.Parse(q.ID)
			if e != nil {
				return nil, extensionError("invalid_auth_aux", e)
			}
		}
		s, ok := host.bridge.authMail.(adminAuthAuxService)
		if !ok {
			return nil, extensionError("auth_aux_unavailable", nil)
		}
		out, e := s.AdminQueryAuthAuxiliary(ctx, host.principal, request)
		return marshalExtensionResult(map[string]any{"items": out}, e)
	case "app.authAux.deleteScope":
		var q struct {
			Kind, Collection, RecordID string
			Expired                    bool
		}
		if err := decodeExtensionInput(raw, &q); err != nil {
			return nil, err
		}
		request := userauth.AuthAuxiliaryQuery{Kind: q.Kind}
		var e error
		if q.Collection != "" {
			request.CollectionID, e = host.resolveAuthMailCollection(ctx, q.Collection)
			if e != nil {
				return nil, extensionError("invalid_auth_aux", e)
			}
		}
		if q.RecordID != "" {
			request.UserID, e = uuid.Parse(q.RecordID)
			if e != nil {
				return nil, extensionError("invalid_auth_aux", e)
			}
		}
		s, ok := host.bridge.authMail.(adminAuthAuxService)
		if !ok {
			return nil, extensionError("auth_aux_unavailable", nil)
		}
		e = s.AdminDeleteAuthAuxiliaryScope(ctx, host.principal, request, q.Expired)
		return marshalExtensionResult(map[string]bool{"deleted": true}, e)
	case "app.authRecord.token":
		var q struct {
			Token      string
			ValidTypes []string
		}
		if err := decodeExtensionInput(raw, &q); err != nil {
			return nil, err
		}
		s, ok := host.bridge.authMail.(adminAuthAuxService)
		if !ok {
			return nil, extensionError("auth_aux_unavailable", nil)
		}
		out, e := s.AdminFindAuthRecordByToken(ctx, host.principal, q.Token, q.ValidTypes)
		allowFile := len(q.ValidTypes) == 0
		for _, kind := range q.ValidTypes {
			if kind == "file" {
				allowFile = true
			}
		}
		if e != nil && allowFile && host.bridge.files != nil {
			if subject, fileErr := host.bridge.files.ResolveDownloadToken(ctx, q.Token); fileErr == nil {
				var realm uuid.UUID
				if host.bridge.appControl.app.pool.QueryRow(ctx, `select auth_collection_id from pgbase_meta.users where id=$1 and active`, subject.ID).Scan(&realm) == nil {
					out, e = host.bridge.authMail.GetAuthRecord(ctx, realm, subject.ID)
				}
			}
		}
		return marshalExtensionResult(out, e)
	case "app.authRecord.email":
		var q struct{ Collection, Email string }
		if err := decodeExtensionInput(raw, &q); err != nil {
			return nil, err
		}
		realm, e := host.resolveAuthMailCollection(ctx, q.Collection)
		if e != nil {
			return nil, extensionError("invalid_auth_record", e)
		}
		s, ok := host.bridge.authMail.(adminAuthAuxService)
		if !ok {
			return nil, extensionError("auth_aux_unavailable", nil)
		}
		out, e := s.AdminFindAuthRecordByEmail(ctx, host.principal, realm, q.Email)
		return marshalExtensionResult(out, e)
	}
	return nil, extensionError("unsupported_operation", nil)
}
