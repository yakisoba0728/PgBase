//go:build integration

package pgbase

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/google/uuid"
)

func TestAuthLifecycleNamedRealmVetoAndNondisclosingRequests(t *testing.T) {
	registry := &HookRegistry{}
	blocked := errors.New("auth extension veto")
	removePassword, err := registry.On(HookAuthPassword, Hook{Before: func(_ context.Context, event *HookEvent) error {
		payload := event.Data.(*AuthEvent)
		if payload.Flow == "register" {
			return blocked
		}
		return nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	address, _ := localMailSink(t)
	fixture := newPublicUsersAppOptions(t, true, func(o *Options) {
		o.Hooks = registry
		o.SMTP = SMTPOptions{Address: address, From: "local@example.com", AllowInsecure: true}
	})
	cookie, csrf := setupPublicUsersAdministrator(t, fixture)
	realm := uuid.New()
	assertPublicUsersStatus(t, publicUsersRequest(t, fixture, http.MethodPost, "/api/admin/auth-collections", map[string]any{"id": realm, "name": "hook_realm", "registrationEnabled": true, "profileSchema": map[string]string{}}, "", cookie, map[string]string{"X-PgBase-CSRF": csrf}), http.StatusCreated)

	registerPath := "/api/v1/auth/hook_realm/register"
	rejected := publicUsersRequest(t, fixture, http.MethodPost, registerPath, map[string]any{"email": "hooked@example.test", "password": publicUserOnePass}, "", nil, nil)
	if rejected.Code < 400 {
		t.Fatal("vetoed registration succeeded")
	}
	var users int
	if err := fixture.app.pool.QueryRow(context.Background(), `select count(*) from pgbase_meta.users where auth_collection_id=$1`, realm).Scan(&users); err != nil || users != 0 {
		t.Fatalf("veto left users=%d err=%v", users, err)
	}
	removePassword()
	assertPublicUsersStatus(t, publicUsersRequest(t, fixture, http.MethodPost, registerPath, map[string]any{"email": "hooked@example.test", "password": publicUserOnePass}, "", nil, nil), http.StatusCreated)

	removeReset, err := registry.On(HookAuthPasswordResetRequest, Hook{Before: func(context.Context, *HookEvent) error { return blocked }})
	if err != nil {
		t.Fatal(err)
	}
	resetPath := "/api/v1/auth/hook_realm/request-password-reset"
	known := publicUsersRequest(t, fixture, http.MethodPost, resetPath, map[string]any{"email": "hooked@example.test"}, "", nil, nil)
	unknown := publicUsersRequest(t, fixture, http.MethodPost, resetPath, map[string]any{"email": "missing@example.test"}, "", nil, nil)
	var knownError, unknownError struct {
		Error struct{ Code, Message string } `json:"error"`
	}
	decodePublicUsersJSON(t, known, &knownError)
	decodePublicUsersJSON(t, unknown, &unknownError)
	if known.Code != unknown.Code || knownError.Error != unknownError.Error {
		t.Fatalf("veto disclosed account: known=(%d,%+v) unknown=(%d,%+v)", known.Code, knownError.Error, unknown.Code, unknownError.Error)
	}
	removeReset()
	var actions int
	if err := fixture.app.pool.QueryRow(context.Background(), `select count(*) from pgbase_meta.user_email_actions`).Scan(&actions); err != nil || actions != 0 {
		t.Fatalf("veto issued email action count=%d err=%v", actions, err)
	}

	removeOTP, err := registry.On(HookAuthOTPRequest, Hook{Before: func(context.Context, *HookEvent) error { return blocked }})
	if err != nil {
		t.Fatal(err)
	}
	assertPublicUsersStatus(t, publicUsersRequest(t, fixture, http.MethodPost, "/api/v1/auth/hook_realm/otp/request", map[string]any{"email": "hooked@example.test"}, "", nil, nil), http.StatusServiceUnavailable)
	removeOTP()
	var otps int
	if err := fixture.app.pool.QueryRow(context.Background(), `select count(*) from pgbase_meta.user_email_otps`).Scan(&otps); err != nil || otps != 0 {
		t.Fatalf("veto issued OTP count=%d err=%v", otps, err)
	}
}
