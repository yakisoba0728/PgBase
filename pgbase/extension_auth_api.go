package pgbase

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/yakisoba0728/PgBase/internal/identity"
	"github.com/yakisoba0728/PgBase/internal/userauth"
)

type extensionAuthRecordRequest struct {
	Collection string          `json:"collection"`
	RecordID   string          `json:"recordId"`
	Expand     string          `json:"expand,omitempty"`
	AuthMethod string          `json:"authMethod,omitempty"`
	Meta       json.RawMessage `json:"meta,omitempty"`
}

func (host *extensionHost) callAuthAPI(ctx context.Context, method string, raw json.RawMessage) (json.RawMessage, error) {
	if host.bridge.authAPI == nil {
		return nil, extensionError("authentication_unavailable", nil)
	}
	if method == "apis.enrichRecords" {
		var requests []extensionAuthRecordRequest
		if err := decodeExtensionInput(raw, &requests); err != nil || len(requests) > 100 {
			return nil, extensionError("invalid_auth_record_request", err)
		}
		results := make([]json.RawMessage, len(requests))
		for i, request := range requests {
			encoded, _ := json.Marshal(request)
			result, err := host.enrichRecord(ctx, encoded)
			if err != nil {
				return nil, err
			}
			results[i] = result
		}
		return json.Marshal(results)
	}
	if method == "apis.enrichRecord" {
		return host.enrichRecord(ctx, raw)
	}
	if host.principal.Kind != identity.Administrator {
		return nil, extensionError("permission_denied", errors.New("recordAuthResponse requires administrator authority"))
	}
	var request extensionAuthRecordRequest
	if err := decodeExtensionInput(raw, &request); err != nil || request.AuthMethod == "" || len(request.AuthMethod) > 64 {
		return nil, extensionError("invalid_auth_record_request", err)
	}
	collection, id, err := host.resolveAuthRecord(ctx, request)
	if err != nil {
		return nil, err
	}
	origin := strings.TrimSpace(host.origin)
	if origin == "" {
		origin = "extension"
	}
	session, err := host.bridge.authAPI.IssueAuthResponseSession(ctx, collection.ID, id, origin)
	if err != nil {
		var challenge *userauth.MFARequiredError
		if errors.As(err, &challenge) {
			return json.Marshal(map[string]any{"status": 401, "json": map[string]any{"code": "mfa_required", "challengeToken": challenge.ChallengeToken, "expiresAt": challenge.ExpiresAt}})
		}
		return nil, mapExtensionError(err)
	}
	record, err := host.bridge.authAPI.GetVisibleAuthRecord(ctx, collection.ID, id)
	if err != nil {
		return nil, mapExtensionError(err)
	}
	return json.Marshal(map[string]any{"status": 200, "json": map[string]any{"token": session.RawToken, "record": extensionVisibleAuthRecord(record, true), "expiresAt": session.ExpiresAt, "authMethod": request.AuthMethod, "meta": request.Meta}})
}

func (host *extensionHost) enrichRecord(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	var request extensionAuthRecordRequest
	if err := decodeExtensionInput(raw, &request); err != nil {
		return nil, extensionError("invalid_auth_record_request", err)
	}
	if collection, id, err := host.resolveAuthRecord(ctx, request); err == nil {
		var record userauth.AuthRecord
		if host.principal.Kind == identity.Administrator {
			record, err = host.bridge.authAPI.GetVisibleAuthRecord(ctx, collection.ID, id)
		} else if host.principal.Kind == identity.User {
			record, err = host.bridge.authAPI.GetAuthorizedAuthRecord(ctx, collection.ID, host.principal.ID, id)
		} else {
			return nil, extensionError("permission_denied", nil)
		}
		if err != nil {
			return nil, mapExtensionError(err)
		}
		return json.Marshal(extensionVisibleAuthRecord(record, host.principal.Kind == identity.Administrator || host.principal.ID == id))
	}
	encoded, _ := json.Marshal(extensionRecordRequest{Collection: request.Collection, ID: request.RecordID, Expand: request.Expand})
	return host.callRecords(ctx, "records.get", encoded)
}

func (host *extensionHost) resolveAuthRecord(ctx context.Context, request extensionAuthRecordRequest) (userauth.AuthCollection, uuid.UUID, error) {
	if strings.TrimSpace(request.Collection) == "" {
		return userauth.AuthCollection{}, uuid.Nil, extensionError("invalid_auth_record_request", nil)
	}
	id, err := requiredUUID(request.RecordID)
	if err != nil {
		return userauth.AuthCollection{}, uuid.Nil, extensionError("invalid_auth_record_request", err)
	}
	collection, err := host.bridge.authAPI.ResolveAuthCollection(ctx, request.Collection)
	if err != nil {
		return userauth.AuthCollection{}, uuid.Nil, mapExtensionError(err)
	}
	return collection, id, nil
}

func extensionVisibleAuthRecord(record userauth.AuthRecord, showEmail bool) map[string]any {
	result := make(map[string]any, len(record.Fields)+7)
	for name, value := range record.Fields {
		result[name] = value
	}
	result["id"], result["collectionId"], result["emailVisibility"], result["verified"], result["active"] = record.ID, record.AuthCollectionID, record.EmailVisibility, record.Verified, record.Active
	result["created"], result["updated"] = record.CreatedAt.UTC().Format(time.RFC3339Nano), record.UpdatedAt.UTC().Format(time.RFC3339Nano)
	// AuthRecord has already passed service-side email visibility projection.
	// This includes owners and principals authorized by the collection's manage
	// rule, while keeping the extension's own map free of a withheld email.
	if showEmail || record.Email != "" {
		result["email"] = record.Email
	}
	return result
}
