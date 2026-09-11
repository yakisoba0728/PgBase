package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/yakisoba0728/PgBase/internal/adminauth"
)

type fakeAdminSelf struct {
	administrator adminauth.Administrator
	prefixes      []string
}

func (f *fakeAdminSelf) Administrator(context.Context, uuid.UUID) (adminauth.Administrator, error) {
	return f.administrator, nil
}
func (f *fakeAdminSelf) UpdateAdministratorEmail(_ context.Context, _ uuid.UUID, email string) (adminauth.Administrator, error) {
	f.administrator.Email = email
	return f.administrator, nil
}
func (f *fakeAdminSelf) AdministratorIPPrefixes(context.Context, uuid.UUID) ([]string, error) {
	return f.prefixes, nil
}
func (f *fakeAdminSelf) ReplaceAdministratorIPPrefixes(_ context.Context, _ uuid.UUID, p []string) ([]string, error) {
	f.prefixes = p
	return p, nil
}

func TestAdminProfileAndIPAllowlistWireShapes(t *testing.T) {
	id := uuid.MustParse("019551d0-e9c0-7000-8000-0000000000aa")
	now := time.Date(2026, 9, 10, 1, 2, 3, 0, time.UTC)
	service := &fakeAdminSelf{administrator: adminauth.Administrator{ID: id, Email: "admin@example.com", Active: true, CreatedAt: now, UpdatedAt: now}}
	h := adminProfileEndpoints{service: service}
	request := func(method, path, body string) *http.Request {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		return r.WithContext(context.WithValue(r.Context(), adminPrincipalKey{}, AdminPrincipal{Administrator: service.administrator}))
	}
	profile := httptest.NewRecorder()
	h.profile(profile, request(http.MethodPatch, "/api/admin/profile", `{"email":"new@example.com"}`))
	if profile.Code != 200 {
		t.Fatalf("profile status=%d body=%s", profile.Code, profile.Body.String())
	}
	var got map[string]any
	if err := json.Unmarshal(profile.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got["email"] != "new@example.com" || got["id"] != id.String() {
		t.Fatalf("profile=%v", got)
	}
	allowlist := httptest.NewRecorder()
	h.ipAllowlist(allowlist, request(http.MethodPatch, "/api/admin/profile/ip-allowlist", `{"prefixes":["192.0.2.0/24"]}`))
	if allowlist.Code != 200 || allowlist.Body.String() != "{\"prefixes\":[\"192.0.2.0/24\"]}\n" {
		t.Fatalf("allowlist status=%d body=%s", allowlist.Code, allowlist.Body.String())
	}
}
