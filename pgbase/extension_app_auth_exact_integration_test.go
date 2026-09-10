//go:build integration

package pgbase

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/yakisoba0728/PgBase/internal/identity"
	"github.com/yakisoba0728/PgBase/internal/jsvm"
)

func TestJavaScriptExactCatalogAndAuthFamiliesUsePostgreSQL(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "pgbase.scripts.json"), []byte(`{"format":"pgbase.scripts/v1","routes":[{"method":"POST","pattern":"/exact","access":"administrator","handler":{"module":"exact.js","export":"run"},"capabilities":["app-control"]}]}`), 0600)
	script := `exports.run=(q)=>{const rec=new Record(q.realm,{id:q.recordId}),col=new Collection({id:q.realm,name:q.realm});if(q.mode==="badType"){try{$app.findAuthRecordByToken(q.token,"verifyEmail");return {accepted:true}}catch(e){return {accepted:false}}}if(q.mode==="action")return {id:$app.findAuthRecordByToken(q.actionToken,"passwordReset").id};if(q.mode==="denied")return {id:$app.findAuthRecordByEmail(new Collection({name:q.realm}),q.email).id};if(q.mode==="bulkGuard"){try{$app.deleteAllExternalAuthsByRecord(rec);return {accepted:true}}catch(e){return {accepted:false}}}const result={cq:$app.collectionQuery().where($dbx.hashExp({type:"auth"})).count(),externalRecord:$app.findAllExternalAuthsByRecord(rec).length,externalCollection:$app.findAllExternalAuthsByCollection(col).length,externalExpr:$app.findFirstExternalAuthByExpr($dbx.hashExp({provider:"github"})).provider,mfaRecord:$app.findAllMFAsByRecord(rec).length,mfaCollection:$app.findAllMFAsByCollection(col).length,mfaId:$app.findMFAById(q.mfaId).id,otpRecord:$app.findAllOTPsByRecord(rec).length,otpCollection:$app.findAllOTPsByCollection(col).length,otpId:$app.findOTPById(q.otpId).id,originRecord:$app.findAllAuthOriginsByRecord(rec).length,originCollection:$app.findAllAuthOriginsByCollection(col).length,originId:$app.findAuthOriginById(q.originId).id,originFingerprint:$app.findAuthOriginByRecordAndFingerprint(rec,q.fingerprint).id,token:$app.findAuthRecordByToken(q.token,"auth").id,email:$app.findAuthRecordByEmail(col,q.email).id};$app.deleteAllAuthOriginsByRecord(rec);$app.deleteAllMFAsByRecord(rec);$app.deleteAllOTPsByRecord(rec);$app.deleteAllExternalAuthsByRecord(rec);$app.deleteExpiredMFAs();$app.deleteExpiredOTPs();return result}`
	os.WriteFile(filepath.Join(dir, "exact.js"), []byte(script), 0600)
	mailAddress, messages := localMailSink(t)
	fixture := newPublicUsersAppOptions(t, true, func(o *Options) {
		o.JavaScript = &JavaScriptOptions{Directory: dir, Capabilities: map[string]bool{"app-control": true}}
		o.SMTP = SMTPOptions{Address: mailAddress, From: "exact@example.test", AllowInsecure: true}
	})
	cookie, csrf := setupPublicUsersAdministrator(t, fixture)
	_ = cookie
	_ = csrf
	registered := publicUsersRequest(t, fixture, "POST", "/api/v1/auth/register", map[string]any{"email": "exact@example.test", "password": publicUserOnePass}, "", nil, nil)
	assertPublicUsersStatus(t, registered, 201)
	var session publicUserSession
	decodePublicUsersJSON(t, registered, &session)
	assertPublicUsersStatus(t, publicUsersRequest(t, fixture, "POST", "/api/v1/auth/request-password-reset", map[string]any{"email": session.User.Email}, "", nil, nil), 204)
	actionToken := mailActionToken(t, messages, "reset")
	realm := uuid.MustParse("00000000-0000-0000-0000-000000000001")
	externalID, mfaID, otpID, originID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	now := time.Now().UTC()
	secret := []byte("public-users-token-secret-32-bytes-minimum")
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte("user-rate-limit\x00auth-origin\x00network\x00"))
	mac.Write([]byte("203.0.113.8"))
	fingerprintHash := mac.Sum(nil)
	tx, err := fixture.app.pool.Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, setup := range []struct {
		sql  string
		args []any
	}{
		{`insert into pgbase_meta.user_oauth_identities(id,auth_collection_id,provider,subject,user_id,created_at) values($1,$2,'github','subject',$3,$4)`, []any{externalID, realm, session.User.ID, now}},
		{`insert into pgbase_meta.user_totp_factors(id,user_id,secret_ciphertext,active,created_at,updated_at) values($1,$2,$3,true,$4,$4)`, []any{mfaID, session.User.ID, []byte("encrypted"), now}},
		{`insert into pgbase_meta.user_email_otps(id,auth_collection_id,user_id,code_hash,max_attempts,expires_at,created_at) values($1,$2,$3,$4,3,$5::timestamptz+interval '5 minutes',$5)`, []any{otpID, realm, session.User.ID, make([]byte, 32), now}},
		{`insert into pgbase_meta.user_auth_origins(id,user_id,fingerprint_hash,first_seen_at,last_seen_at) values($1,$2,$3,$4,$4)`, []any{originID, session.User.ID, fingerprintHash, now}},
	} {
		if _, err = tx.Exec(t.Context(), setup.sql, setup.args...); err != nil {
			_ = tx.Rollback(t.Context())
			t.Fatal(err)
		}
	}
	if err = tx.Commit(t.Context()); err != nil {
		t.Fatal(err)
	}
	bundle, err := jsvm.LoadBundle(dir)
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := jsvm.NewRuntime(bundle, jsvm.Limits{MaximumHostCalls: 64})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close(t.Context())
	host := &extensionHost{bridge: fixture.app.extensions, principal: identity.Subject{Kind: identity.Administrator, ID: uuid.New()}, requestID: "exact", valid: true}
	invoke := func(mode string, h jsvm.Host) (map[string]any, error) {
		input, _ := json.Marshal(map[string]any{"mode": mode, "realm": realm.String(), "recordId": session.User.ID.String(), "email": session.User.Email, "token": session.Token, "actionToken": actionToken, "mfaId": mfaID.String(), "otpId": otpID.String(), "originId": originID.String(), "fingerprint": "203.0.113.8"})
		raw, e := runtime.Invoke(t.Context(), jsvm.Invocation{Handler: bundle.Manifest().Routes[0].Handler, Capabilities: []string{"app-control"}, Input: input, Host: h})
		var out map[string]any
		if e == nil {
			e = json.Unmarshal(raw, &out)
		}
		return out, e
	}
	got, err := invoke("", host)
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"externalRecord", "externalCollection", "mfaRecord", "mfaCollection", "otpRecord", "otpCollection"} {
		if got[key].(float64) != 1 {
			t.Fatalf("%s=%v", key, got[key])
		}
	}
	for _, key := range []string{"originRecord", "originCollection"} {
		if got[key].(float64) < 1 {
			t.Fatalf("%s=%v", key, got[key])
		}
	}
	for _, key := range []string{"mfaId", "otpId", "originId", "originFingerprint", "token", "email"} {
		if got[key] == "" {
			t.Fatalf("%s=%v", key, got[key])
		}
	}
	var remaining int
	if err = fixture.app.pool.QueryRow(t.Context(), `select (select count(*) from pgbase_meta.user_oauth_identities where user_id=$1)+(select count(*) from pgbase_meta.user_totp_factors where user_id=$1)+(select count(*) from pgbase_meta.user_email_otps where user_id=$1)+(select count(*) from pgbase_meta.user_auth_origins where user_id=$1)`, session.User.ID).Scan(&remaining); err != nil || remaining != 0 {
		t.Fatalf("remaining=%d err=%v", remaining, err)
	}
	if bad, err := invoke("badType", host); err != nil || bad["accepted"] != false {
		t.Fatalf("bad token type=%v %v", bad, err)
	}
	if action, err := invoke("action", host); err != nil || action["id"] != session.User.ID.String() {
		t.Fatalf("action token=%v %v", action, err)
	}
	if _, err = fixture.app.pool.Exec(t.Context(), `update pgbase_meta.user_email_actions set expires_at=CURRENT_TIMESTAMP-interval '1 second' where token_hash is not null and user_id=$1`, session.User.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = invoke("action", host); err == nil {
		t.Fatal("expired action token accepted")
	}
	guardID := uuid.New()
	if _, err = fixture.app.pool.Exec(t.Context(), `update pgbase_meta.users set password_enabled=false where id=$1`, session.User.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = fixture.app.pool.Exec(t.Context(), `update pgbase_meta.auth_collection_policies set otp_enabled=false where auth_collection_id=$1`, realm); err != nil {
		t.Fatal(err)
	}
	if _, err = fixture.app.pool.Exec(t.Context(), `insert into pgbase_meta.user_oauth_identities(id,auth_collection_id,provider,subject,user_id,created_at) values($1,$2,'guard','guard',$3,now())`, guardID, realm, session.User.ID); err != nil {
		t.Fatal(err)
	}
	if guarded, err := invoke("bulkGuard", host); err != nil || guarded["accepted"] != false {
		t.Fatalf("guard=%v %v", guarded, err)
	}
	if err = fixture.app.pool.QueryRow(t.Context(), `select count(*) from pgbase_meta.user_oauth_identities where id=$1`, guardID).Scan(&remaining); err != nil || remaining != 1 {
		t.Fatalf("bulk guard rolled through n=%d err=%v", remaining, err)
	}
	denied := &extensionHost{bridge: fixture.app.extensions, principal: identity.Subject{Kind: identity.User, ID: session.User.ID}, requestID: "denied", valid: true}
	if _, err = invoke("denied", denied); err == nil {
		t.Fatal("non-administrator exact auth method accepted")
	}
}
