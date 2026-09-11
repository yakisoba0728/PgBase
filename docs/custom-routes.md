# Go application routes

An embedded PgBase application can supply `Options.CustomRoutes` before startup. Routes run inside the same router as built-in handlers: request IDs, panic recovery, CORS, request logging, metrics, body limits and application request hooks also apply. The CLI cannot load Go source dynamically; compile an embedding application such as `examples/custom-routes`.

```go
options.CustomRoutes = []pgbase.CustomRoute{
    {
        Method: "GET",
        Pattern: "/reports/{id}",
        Access: pgbase.RouteUser,
        Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
            principal, ok := pgbase.PrincipalFromContext(r.Context())
            if !ok {
                http.Error(w, "Authentication required", http.StatusUnauthorized)
                return
            }
            // Authorize the requested report against principal.ID and
            // principal.AuthCollectionID before reading application data.
            _ = json.NewEncoder(w).Encode(map[string]string{
                "requestedReport": r.PathValue("id"),
                "requestedBy": principal.ID,
            })
        }),
    },
}
```

Access must be explicit. `RoutePublic` accepts anonymous requests and does not interpret presented credentials. `RouteUser` accepts a live user bearer session, `RouteAdmin` accepts the administrator cookie, and `RouteService` accepts a live service bearer token. Cookie-authenticated reads reject a foreign Origin; administrator mutations also require the normal same-origin and `X-PgBase-CSRF` checks. User and service credentials cannot substitute for each other. Revocation applies on the next request. Authenticated responses, including admission errors, default to `Cache-Control: no-store`.

`PrincipalFromContext` exposes kind, ID, user auth collection, verification state and session expiry where available. It never includes session tokens, CSRF credentials or service secrets. This is authentication, not resource authorization: a custom handler must check ownership, permitted auth collections and its own application permissions. Service token collection scopes are enforced by built-in record operations; they do not implicitly authorize arbitrary custom application operations.

Patterns use Go ServeMux wildcard syntax. They start with a literal application namespace such as `/reports`; `/api`, `/admin`, `/health`, `/metrics` and `/auth` are reserved. Root catch-all patterns, host patterns, noncanonical paths and conflicting patterns are refused during options validation. GET also admits HEAD according to Go routing semantics. GET, HEAD, POST, PUT, PATCH and DELETE are supported. At most 64 custom routes can be installed; registration is fixed for that application instance, so changing it requires restarting. The options slice is copied at normalization; handler objects remain shared and must support concurrent calls.

Requests default to 1 MiB and responses to 256 KiB. `MaximumRequestBytes` may lower the request cap; `MaximumResponseBytes` may set a response cap up to the record-page limit (4 MiB plus 4 KiB framing). Handlers must check body read and write errors. Responses are buffered so overflow and a panic before return cannot publish a partial success body. This interface does not support streaming, websocket upgrades, removal of core middleware, or PocketBase's route-group/event API. It also does not make handler side effects transactional: use application transactions and idempotency where needed. Cancellation is cooperative and handlers must pass `r.Context()` to blocking work.

Custom route templates join the bounded HTTP metric labels. Each method-bound route contributes at most two route-method combinations (GET/HEAD); 64 routes add at most 128 combinations. With six status classes and 17 counter/histogram series per class plus one inflight series, the additional conservative HTTP ceiling is `128 × (6 × 17 + 1) = 13,184` series. Actual path values, query strings and credentials never become route labels. The built-in route budget is enforced separately at 152 routes and at most three method-agnostic routes. Its conservative route-method ceiling is `(149 × 2) + (3 × 10) + (1 unmatched × 10) = 338`; combined with extensions, the HTTP series ceiling is `(338 + 128) × 103 = 47,998`. Application-operation metrics have their separate budgets. Generated built-in OpenAPI documents do not claim to describe an embedding application's custom payloads.

The reference PocketBase v0.40.3 router wraps Go ServeMux with groups and hook middleware (`.local/upstream/pocketbase/tools/router/router.go`). PgBase supplies a standard `http.Handler` interface with explicit admission policies; this is a PgBase extension contract, not source compatibility with PocketBase.
