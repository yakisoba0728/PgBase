package api

import (
	"context"
	"net/http"
	"strings"

	"github.com/webisnoteasy/PgBase/internal/identity"
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
		principal, err := auth.Authenticate(r.Context(), parts[1])
		if err != nil {
			writeServiceCredentialError(w, r)
			return
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
func writeServiceCredentialError(w http.ResponseWriter, r *http.Request) {
	writeAPIError(w, r, APIError{Status: http.StatusUnauthorized, Code: "invalid_service_token", Message: "Invalid service token"})
}

func serviceRecordRoutes(dependencies Dependencies) []route {
	if dependencies.ServiceTokens == nil || dependencies.Records == nil {
		return nil
	}
	records := recordEndpoints{service: dependencies.Records}
	return []route{
		{method: "GET", pattern: "/api/v1/service/collections/{collection}/records", access: accessServiceSession, response: atomicJSON(maximumRecordPageResponseBytes), deadline: deadlineDefault, handler: http.HandlerFunc(records.list)},
		{method: "POST", pattern: "/api/v1/service/collections/{collection}/records", access: accessServiceSession, response: atomicJSON(maximumRecordPageResponseBytes), deadline: deadlineDefault, handler: http.HandlerFunc(records.create)},
		{method: "GET", pattern: "/api/v1/service/collections/{collection}/records/{id}", access: accessServiceSession, response: atomicJSON(maximumRecordPageResponseBytes), deadline: deadlineDefault, handler: http.HandlerFunc(records.get)},
		{method: "PATCH", pattern: "/api/v1/service/collections/{collection}/records/{id}", access: accessServiceSession, response: atomicJSON(maximumRecordPageResponseBytes), deadline: deadlineDefault, handler: http.HandlerFunc(records.update)},
		{method: "DELETE", pattern: "/api/v1/service/collections/{collection}/records/{id}", access: accessServiceSession, response: atomicJSON(maximumRecordPageResponseBytes), deadline: deadlineDefault, handler: http.HandlerFunc(records.delete)},
		{method: "POST", pattern: "/api/v1/service/collections/batch", access: accessServiceSession, response: atomicJSON(maximumRecordPageResponseBytes), deadline: deadlineDefault, maximumRequestBytes: maximumBatchRequestBytes, handler: http.HandlerFunc(records.batch)},
		{method: "GET", pattern: "/api/v1/public/collections/{collection}/records", access: accessAnonymous, response: atomicJSON(maximumRecordPageResponseBytes), deadline: deadlineDefault, handler: http.HandlerFunc(records.list)},
		{method: "POST", pattern: "/api/v1/public/collections/{collection}/records", access: accessAnonymous, response: atomicJSON(maximumRecordPageResponseBytes), deadline: deadlineDefault, handler: http.HandlerFunc(records.create)},
		{method: "GET", pattern: "/api/v1/public/collections/{collection}/records/{id}", access: accessAnonymous, response: atomicJSON(maximumRecordPageResponseBytes), deadline: deadlineDefault, handler: http.HandlerFunc(records.get)},
		{method: "PATCH", pattern: "/api/v1/public/collections/{collection}/records/{id}", access: accessAnonymous, response: atomicJSON(maximumRecordPageResponseBytes), deadline: deadlineDefault, handler: http.HandlerFunc(records.update)},
		{method: "DELETE", pattern: "/api/v1/public/collections/{collection}/records/{id}", access: accessAnonymous, response: atomicJSON(maximumRecordPageResponseBytes), deadline: deadlineDefault, handler: http.HandlerFunc(records.delete)},
		{method: "POST", pattern: "/api/v1/public/collections/batch", access: accessAnonymous, response: atomicJSON(maximumRecordPageResponseBytes), deadline: deadlineDefault, maximumRequestBytes: maximumBatchRequestBytes, handler: http.HandlerFunc(records.batch)},
	}
}
