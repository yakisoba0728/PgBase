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

type adminAuthAuxService interface {
	AdminListAuthAuxiliary(context.Context, identity.Subject, uuid.UUID, uuid.UUID, string) ([]userauth.AuthAuxiliaryRecord, error)
	AdminDeleteAuthAuxiliary(context.Context, identity.Subject, uuid.UUID, uuid.UUID, string, uuid.UUID) error
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
		return nil, extensionError("not_found", errors.New("collection not found"))
	case "app.collection.list":
		var q struct {
			Type string `json:"type"`
		}
		if err := decodeExtensionInput(raw, &q); err != nil {
			return nil, err
		}
		out := make([]any, 0)
		for _, c := range host.bridge.appControl.app.schemaCache.Load().Collections {
			if q.Type == "" || string(c.Type) == q.Type {
				out = append(out, c)
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
		return json.RawMessage(`true`), nil
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
	}
	return nil, extensionError("unsupported_operation", nil)
}
