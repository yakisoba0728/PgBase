package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/yakisoba0728/PgBase/internal/adminauth"
)

type fakeAdminSelf struct {
	administrator adminauth.Administrator
	prefixes      []string
	address       string
	credentialErr error
}

func (f *fakeAdminSelf) Administrator(context.Context, uuid.UUID) (adminauth.Administrator, error) {
	return f.administrator, nil
}
func (f *fakeAdminSelf) UpdateAdministratorCredentials(_ context.Context, _ uuid.UUID, address, _, email, _ string) (adminauth.Administrator, error) {
	f.address = address
	if f.credentialErr != nil {
		return adminauth.Administrator{}, f.credentialErr
	}
	f.administrator.Email = email
	return f.administrator, nil
}
func (f *fakeAdminSelf) AdministratorIPPrefixes(context.Context, uuid.UUID) ([]string, error) {
	return f.prefixes, nil
}
func (f *fakeAdminSelf) ReplaceAdministratorIPPrefixes(_ context.Context, _ uuid.UUID, address string, p []string) ([]string, error) {
	f.address = address
	f.prefixes = p
	return p, nil
}

func TestAdminProfileAndIPAllowlistWireShapes(t *testing.T) {
	id := uuid.MustParse("019551d0-e9c0-7000-8000-0000000000aa")
	now := time.Date(2026, 9, 10, 1, 2, 3, 0, time.UTC)
	service := &fakeAdminSelf{administrator: adminauth.Administrator{ID: id, Email: "admin@example.com", Active: true, CreatedAt: now, UpdatedAt: now}}
	h := adminProfileEndpoints{service: service, trustedProxies: func() []netip.Prefix { return []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8")} }}
	request := func(method, path, body, remote, forwarded string) *http.Request {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		r.RemoteAddr = remote
		if forwarded != "" {
			r.Header.Set("X-Forwarded-For", forwarded)
		}
		return r.WithContext(context.WithValue(r.Context(), adminPrincipalKey{}, AdminPrincipal{Administrator: service.administrator}))
	}
	profile := httptest.NewRecorder()
	h.profile(profile, request(http.MethodPatch, "/api/admin/profile", `{"currentPassword":"secret","email":"new@example.com"}`, "192.0.2.1:1234", ""))
	if profile.Code != 200 || service.address != "192.0.2.1" {
		t.Fatalf("profile status=%d body=%s address=%s", profile.Code, profile.Body.String(), service.address)
	}
	var got map[string]any
	if err := json.Unmarshal(profile.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got["email"] != "new@example.com" || got["id"] != id.String() {
		t.Fatalf("profile=%v", got)
	}
	for _, tc := range []struct {
		err    error
		status int
		code   string
	}{
		{adminauth.ErrInvalidCredentials, 403, "current_password_invalid"},
		{adminauth.ErrRateLimited, 429, "rate_limited"},
		{adminauth.ErrValidation, 400, "invalid_profile"},
	} {
		service.credentialErr = tc.err
		w := httptest.NewRecorder()
		h.profile(w, request(http.MethodPatch, "/api/admin/profile", `{"currentPassword":"wrong","newPassword":"replacement password"}`, "192.0.2.1:1234", ""))
		if w.Code != tc.status || !strings.Contains(w.Body.String(), `"`+tc.code+`"`) {
			t.Errorf("%v: status=%d body=%s", tc.err, w.Code, w.Body.String())
		}
	}
	for _, tc := range []struct{ name, remote, forwarded, want string }{
		{"direct", "192.0.2.1:1234", "", "192.0.2.1"},
		{"direct IPv6", "[2001:db8::7]:1234", "", "2001:db8::7"},
		{"trusted proxy", "10.1.2.3:1234", "203.0.113.9, 10.0.0.2", "203.0.113.9"},
		{"untrusted proxy header ignored", "198.51.100.4:1234", "203.0.113.9", "198.51.100.4"},
	} {
		allowlist := httptest.NewRecorder()
		h.ipAllowlist(allowlist, request(http.MethodPatch, "/api/admin/profile/ip-allowlist", `{"prefixes":["192.0.2.0/24"]}`, tc.remote, tc.forwarded))
		want := `{"prefixes":["192.0.2.0/24"],"clientAddress":"` + tc.want + "\"}\n"
		if allowlist.Code != 200 || allowlist.Body.String() != want || service.address != tc.want {
			t.Errorf("%s: status=%d body=%s address=%s", tc.name, allowlist.Code, allowlist.Body.String(), service.address)
		}
		read := httptest.NewRecorder()
		h.ipAllowlist(read, request(http.MethodGet, "/api/admin/profile/ip-allowlist", "", tc.remote, tc.forwarded))
		if read.Body.String() != want {
			t.Errorf("%s GET body=%s", tc.name, read.Body.String())
		}
	}
	excluded := httptest.NewRecorder()
	writeAdminProfileError(excluded, request(http.MethodPatch, "/api/admin/profile/ip-allowlist", "", "192.0.2.1:1", ""), adminauth.ErrIPRestricted)
	if excluded.Code != 409 || !strings.Contains(excluded.Body.String(), `"ip_allowlist_excludes_caller"`) {
		t.Fatalf("excluded status=%d body=%s", excluded.Code, excluded.Body.String())
	}
}
