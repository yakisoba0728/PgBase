package pgbase

import (
	"context"
	"encoding/json"
	"github.com/google/uuid"
	"github.com/yakisoba0728/PgBase/internal/userauth"
	"testing"
)

type fakeAuthMail struct {
	realm, id uuid.UUID
	calls     []string
	otpID     string
	otpPass   string
	alertInfo string
}

func (f *fakeAuthMail) GetAuthCollection(_ context.Context, name string) (userauth.AuthCollection, error) {
	if name != "members" {
		return userauth.AuthCollection{}, userauth.ErrNotFound
	}
	return userauth.AuthCollection{ID: f.realm, Name: name}, nil
}

func (f *fakeAuthMail) GetAuthRecord(_ context.Context, r, id uuid.UUID) (userauth.AuthRecord, error) {
	if r != f.realm || id != f.id {
		return userauth.AuthRecord{}, userauth.ErrNotFound
	}
	return userauth.AuthRecord{Email: "record@example.test"}, nil
}
func (f *fakeAuthMail) AdminRequestPasswordReset(context.Context, uuid.UUID) error {
	f.calls = append(f.calls, "passwordReset")
	return nil
}
func (f *fakeAuthMail) AdminRequestVerification(context.Context, uuid.UUID) error {
	f.calls = append(f.calls, "verification")
	return nil
}
func (f *fakeAuthMail) AdminRequestEmailChange(_ context.Context, _ uuid.UUID, _ string) error {
	f.calls = append(f.calls, "changeEmail")
	return nil
}
func (f *fakeAuthMail) AdminSendRecordOTP(_ context.Context, _ uuid.UUID, _ uuid.UUID, otpID, otpPass string) error {
	f.calls = append(f.calls, "otp")
	f.otpID, f.otpPass = otpID, otpPass
	return nil
}
func (f *fakeAuthMail) AdminSendRecordAuthAlert(_ context.Context, _ uuid.UUID, alertInfo string) error {
	f.calls = append(f.calls, "authAlert")
	f.alertInfo = alertInfo
	return nil
}
func TestAuthMailDispatcherChecksRealmAndDispatchesEveryKind(t *testing.T) {
	realm, id := uuid.New(), uuid.New()
	fake := &fakeAuthMail{realm: realm, id: id}
	host := &extensionHost{bridge: &extensionBridge{authMail: fake, compatibility: &extensionCompatibility{authMailCollections: map[uuid.UUID]bool{realm: true}}}}
	requests := []map[string]any{
		{"kind": "passwordReset", "collection": realm.String(), "recordId": id.String()},
		{"kind": "verification", "collection": realm.String(), "recordId": id.String()},
		{"kind": "changeEmail", "collection": realm.String(), "recordId": id.String(), "newEmail": "new@example.test"},
		{"kind": "otp", "collection": realm.String(), "recordId": id.String(), "otpId": "otp-id", "otpPass": "otp-pass"},
		{"kind": "authAlert", "collection": realm.String(), "recordId": id.String(), "alertInfo": map[string]any{"ip": "127.0.0.1"}},
	}
	for _, request := range requests {
		raw, _ := json.Marshal(request)
		if _, err := host.callAuthMail(context.Background(), raw); err != nil {
			t.Fatalf("%s: %v", request["kind"], err)
		}
	}
	if len(fake.calls) != 5 {
		t.Fatalf("calls=%v", fake.calls)
	}
	if fake.otpID != "otp-id" || fake.otpPass != "otp-pass" || fake.alertInfo != `{"ip":"127.0.0.1"}` {
		t.Fatalf("semantic values lost: otp=(%q,%q) alert=%s", fake.otpID, fake.otpPass, fake.alertInfo)
	}
	byName, _ := json.Marshal(map[string]string{"kind": "passwordReset", "collection": "members", "recordId": id.String()})
	if _, err := host.callAuthMail(context.Background(), byName); err != nil {
		t.Fatalf("authorized collection name: %v", err)
	}
	unauthorizedName, _ := json.Marshal(map[string]string{"kind": "passwordReset", "collection": "other", "recordId": id.String()})
	if _, err := host.callAuthMail(context.Background(), unauthorizedName); err == nil {
		t.Fatal("unauthorized collection name accepted")
	}
	raw := []byte(`{"kind":"otp","collection":"` + uuid.NewString() + `","recordId":"` + id.String() + `"}`)
	if _, err := host.callAuthMail(context.Background(), raw); err == nil {
		t.Fatal("unauthorized realm accepted")
	}
	if _, err := host.callAuthMail(context.Background(), []byte(`{"kind":1}`)); err == nil {
		t.Fatal("invalid payload accepted")
	}
}

func TestAuthMailDispatcherRejectsEmptyAndUnknownFields(t *testing.T) {
	realm, id := uuid.New(), uuid.New()
	fake := &fakeAuthMail{realm: realm, id: id}
	host := &extensionHost{bridge: &extensionBridge{authMail: fake, compatibility: &extensionCompatibility{authMailCollections: map[uuid.UUID]bool{realm: true}}}}
	for _, raw := range []string{`{"kind":"changeEmail","collection":"` + realm.String() + `","recordId":"` + id.String() + `"}`, `{"kind":"otp","collection":"` + realm.String() + `","recordId":"` + id.String() + `","extra":true}`} {
		if _, err := host.callAuthMail(context.Background(), []byte(raw)); err == nil {
			t.Fatalf("accepted %s", raw)
		}
	}
	if len(fake.calls) != 0 {
		t.Fatalf("calls=%v", fake.calls)
	}
}
