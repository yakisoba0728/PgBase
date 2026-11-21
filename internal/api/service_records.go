package api

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/yakisoba0728/PgBase/internal/identity"
	"github.com/yakisoba0728/PgBase/internal/recordaccess"
	"github.com/yakisoba0728/PgBase/internal/serviceauth"
)

type publicRecordPrincipalKey struct{}

func serviceSessionMiddleware(auth ServiceTokens, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		values := r.Header.Values("Authorization")
		if len(values) != 1 || len(values[0]) > 256 {
			writeServiceCredentialError(w, r)
			return
		}
		parts := strings.Fields(values[0])
		if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
			writeServiceCredentialError(w, r)
			return
		}
		if auth == nil {
			writeServiceCredentialError(w, r)
			return
		}
		principal, err := auth.Authenticate(r.Context(), parts[1])
		if err != nil && !errors.Is(err, serviceauth.ErrInvalidToken) {
			writeAPIError(w, r, APIError{Status: http.StatusServiceUnavailable, Code: "authentication_unavailable", Message: "Service token authentication is unavailable"})
			return
		}
		if err != nil || principal.Subject.Kind != identity.Service || !principal.Subject.Valid() {
			writeServiceCredentialError(w, r)
			return
		}
		if metadata, ok := r.Context().Value(requestLogContextKey{}).(*requestLogContext); ok && metadata.includeAuth {
			metadata.authKind, metadata.authID = "service", principal.Subject.ID.String()
		}
		ctx := context.WithValue(r.Context(), publicRecordPrincipalKey{}, principal.Subject)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
func anonymousSessionMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if len(r.Header.Values("Authorization")) != 0 {
			writeServiceCredentialError(w, r)
			return
		}
		subject := identity.Subject{Kind: identity.Anonymous}
		ctx := context.WithValue(r.Context(), publicRecordPrincipalKey{}, subject)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// anonymousWriteAdmission is a body-free preflight of the anonymous admission
// gate. Public write routes accept large bodies (and multipart batches stage
// files), but anonymous admission is a per-collection opt-in that recordops
// only checks after the body has been read. Checking the opt-in first keeps an
// unauthenticated caller from making the server buffer or stage a body for a
// collection it can never write. It returns nil for every other principal, so
// authenticated paths are untouched, and recordops keeps the authoritative
// check under its transaction locks. A missing collection fails exactly as the
// record service reports it (404 not_found), and a collection without the
// opt-in as recordaccess.ErrDenied (403 access_denied).
func (e recordEndpoints) anonymousWriteAdmission(r *http.Request, collections ...string) error {
	if e.access == nil {
		return nil
	}
	principal, ok := r.Context().Value(publicRecordPrincipalKey{}).(identity.Subject)
	if !ok || principal.Kind != identity.Anonymous {
		return nil
	}
	checked := make(map[string]bool, len(collections))
	for _, name := range collections {
		if name == "" || checked[name] {
			continue
		}
		checked[name] = true
		policy, err := e.access.Get(r.Context(), name)
		if err != nil {
			return err
		}
		if !policy.Enabled {
			return recordaccess.ErrDenied
		}
	}
	return nil
}

// admitAnonymousWrite writes the preflight failure and reports whether the
// handler may go on to read the request body.
func (e recordEndpoints) admitAnonymousWrite(w http.ResponseWriter, r *http.Request) bool {
	if err := e.anonymousWriteAdmission(r, r.PathValue("collection")); err != nil {
		writeRecordError(w, r, err)
		return false
	}
	return true
}

func writeServiceCredentialError(w http.ResponseWriter, r *http.Request) {
	writeAPIError(w, r, APIError{Status: http.StatusUnauthorized, Code: "invalid_service_token", Message: "Invalid service token"})
}

func serviceRecordRoutes(dependencies Dependencies) []route {
	if dependencies.ServiceTokens == nil || dependencies.Records == nil {
		return nil
	}
	records := recordEndpoints{service: dependencies.Records, files: dependencies.Files, hooks: dependencies.LifecycleHooks, access: dependencies.RecordAccess}
	if dependencies.RuntimeSettings != nil {
		records.settings = dependencies.RuntimeSettings
	}
	return []route{
		{method: "GET", pattern: "/api/v1/service/collections/{collection}/records", access: accessServiceSession, response: atomicJSON(maximumRecordPageResponseBytes), deadline: deadlineDefault, handler: http.HandlerFunc(records.list)},
		{method: "POST", pattern: "/api/v1/service/collections/{collection}/records", access: accessServiceSession, response: atomicJSON(maximumRecordPageResponseBytes), deadline: deadlineDefault, maximumRequestBytes: maximumRecordRequestBytes, recordDocument: true, handler: http.HandlerFunc(records.create)},
		{method: "GET", pattern: "/api/v1/service/collections/{collection}/records/{id}", access: accessServiceSession, response: atomicJSON(maximumRecordPageResponseBytes), deadline: deadlineDefault, handler: http.HandlerFunc(records.get)},
		{method: "PATCH", pattern: "/api/v1/service/collections/{collection}/records/{id}", access: accessServiceSession, response: atomicJSON(maximumRecordPageResponseBytes), deadline: deadlineDefault, maximumRequestBytes: maximumRecordRequestBytes, recordDocument: true, handler: http.HandlerFunc(records.update)},
		{method: "DELETE", pattern: "/api/v1/service/collections/{collection}/records/{id}", access: accessServiceSession, response: atomicJSON(maximumRecordPageResponseBytes), deadline: deadlineDefault, handler: http.HandlerFunc(records.delete)},
		{method: "POST", pattern: "/api/v1/service/collections/batch", access: accessServiceSession, response: atomicJSON(maximumBatchResponseBytes), deadline: deadlineDefault, maximumRequestBytes: maximumMultipartBatchRequestBytes, streamedUpload: true, handler: http.HandlerFunc(records.batch)},
		{method: "GET", pattern: "/api/v1/public/collections/{collection}/records", access: accessAnonymous, response: atomicJSON(maximumRecordPageResponseBytes), deadline: deadlineDefault, handler: http.HandlerFunc(records.list)},
		{method: "POST", pattern: "/api/v1/public/collections/{collection}/records", access: accessAnonymous, response: atomicJSON(maximumRecordPageResponseBytes), deadline: deadlineDefault, maximumRequestBytes: maximumRecordRequestBytes, recordDocument: true, handler: http.HandlerFunc(records.create)},
		{method: "GET", pattern: "/api/v1/public/collections/{collection}/records/{id}", access: accessAnonymous, response: atomicJSON(maximumRecordPageResponseBytes), deadline: deadlineDefault, handler: http.HandlerFunc(records.get)},
		{method: "PATCH", pattern: "/api/v1/public/collections/{collection}/records/{id}", access: accessAnonymous, response: atomicJSON(maximumRecordPageResponseBytes), deadline: deadlineDefault, maximumRequestBytes: maximumRecordRequestBytes, recordDocument: true, handler: http.HandlerFunc(records.update)},
		{method: "DELETE", pattern: "/api/v1/public/collections/{collection}/records/{id}", access: accessAnonymous, response: atomicJSON(maximumRecordPageResponseBytes), deadline: deadlineDefault, handler: http.HandlerFunc(records.delete)},
		{method: "POST", pattern: "/api/v1/public/collections/batch", access: accessAnonymous, response: atomicJSON(maximumBatchResponseBytes), deadline: deadlineDefault, maximumRequestBytes: maximumMultipartBatchRequestBytes, streamedUpload: true, handler: http.HandlerFunc(records.batch)},
	}
}
