package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"

	"github.com/google/uuid"
	"github.com/yakisoba0728/PgBase/internal/filestore"
	"github.com/yakisoba0728/PgBase/internal/identity"
	"github.com/yakisoba0728/PgBase/internal/userauth"
)

func TestStorageAdmissionSubjectFollowsTheAdmittedCaller(t *testing.T) {
	userID := uuid.MustParse("019c5b18-8b85-7c34-8d14-3f9aaf8484b2")
	tokenID := uuid.MustParse("019c5b18-8b85-7c34-8d14-3f9aaf8484b3")
	proxy := netip.MustParsePrefix("10.0.0.0/8")
	tests := []struct {
		name      string
		remote    string
		forwarded string
		value     func(context.Context) context.Context
		want      string
	}{
		{name: "user", remote: "192.0.2.10:1000", value: func(ctx context.Context) context.Context {
			return context.WithValue(ctx, userPrincipalKey{}, authenticatedUser{principal: userauth.Principal{User: userauth.User{ID: userID}}})
		}, want: "user:" + userID.String()},
		{name: "service token", remote: "192.0.2.10:1000", value: func(ctx context.Context) context.Context {
			return context.WithValue(ctx, publicRecordPrincipalKey{}, identity.Subject{Kind: identity.Service, ID: tokenID})
		}, want: "service:" + tokenID.String()},
		{name: "anonymous IPv4", remote: "192.0.2.10:1000", value: func(ctx context.Context) context.Context {
			return context.WithValue(ctx, publicRecordPrincipalKey{}, identity.Subject{Kind: identity.Anonymous})
		}, want: "anonymous:192.0.2.10"},
		{name: "anonymous IPv6 shares its /64", remote: "[2001:db8:1:2:aaaa::1]:1000", want: "anonymous:2001:db8:1:2::/64"},
		{name: "anonymous behind a trusted proxy", remote: "10.1.2.3:1000", forwarded: "2001:db8:1:2:bbbb::9", want: "anonymous:2001:db8:1:2::/64"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var got string
			handler := storageAdmissionMiddleware(func() []netip.Prefix { return []netip.Prefix{proxy} }, http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
				got = filestore.StorageAdmissionSubject(r.Context())
			}))
			r := httptest.NewRequest(http.MethodPost, "/api/v1/public/collections/posts/records", nil)
			r.RemoteAddr = test.remote
			if test.forwarded != "" {
				r.Header.Set("X-Forwarded-For", test.forwarded)
			}
			if test.value != nil {
				r = r.WithContext(test.value(r.Context()))
			}
			handler.ServeHTTP(httptest.NewRecorder(), r)
			if got != test.want {
				t.Fatalf("subject = %q, want %q", got, test.want)
			}
		})
	}
}
