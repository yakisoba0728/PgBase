package accessrule

import "context"

type requestContextKey struct{}

// WithRequestContext carries a trusted adapter's request metadata through
// service calls that authorize inside a database transaction. It does not grant
// authority: each service must still validate its principal and persisted rule.
// The adapter must provide an owned snapshot and leave it unchanged afterwards.
func WithRequestContext(ctx context.Context, request *RequestContext) context.Context {
	return context.WithValue(ctx, requestContextKey{}, request)
}

// RequestContextFromContext returns nil for embedded/background callers that
// have not supplied request metadata. Request-dependent rules remain fail-closed.
func RequestContextFromContext(ctx context.Context) *RequestContext {
	request, _ := ctx.Value(requestContextKey{}).(*RequestContext)
	return request
}
