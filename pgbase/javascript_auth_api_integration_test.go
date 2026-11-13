//go:build integration

package pgbase

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/yakisoba0728/PgBase/internal/identity"
	"github.com/yakisoba0728/PgBase/internal/schema"
	"github.com/yakisoba0728/PgBase/internal/userauth"
)

func TestJavaScriptRecordAuthResponseIssuesValidatedSession(t *testing.T) {
	directory := t.TempDir()
	writeJavaScriptFixture(t, directory, `{"format":"pgbase.scripts/v1","routes":[{"method":"POST","pattern":"/custom-auth","access":"administrator","handler":{"module":"route.js","export":"run"},"capabilities":["app-control"]}]}`, `exports.run=(e)=>{const collection=new Collection({name:e.json.collection}),record=new Record(collection,{id:e.json.recordId});return $apis.recordAuthResponse(e,record,"custom",{provider:"trusted"})}`)
	fixture := newPublicUsersAppOptions(t, true, func(options *Options) {
		options.JavaScript = &JavaScriptOptions{Directory: directory, Capabilities: map[string]bool{"app-control": true}}
	})
	adminCookie, csrf := setupPublicUsersAdministrator(t, fixture)
	registered := registerPublicUser(t, fixture, "custom-auth@example.test", publicUserOnePass)
	response := publicUsersRequest(t, fixture, http.MethodPost, "/custom-auth", map[string]any{"collection": userauth.DefaultCollectionID.String(), "recordId": registered.User.ID.String()}, "", adminCookie, map[string]string{"X-PgBase-CSRF": csrf})
	assertPublicUsersStatus(t, response, http.StatusOK)
	var result struct {
		Token      string         `json:"token"`
		Record     map[string]any `json:"record"`
		AuthMethod string         `json:"authMethod"`
	}
	decodePublicUsersJSON(t, response, &result)
	if result.Token == "" || result.AuthMethod != "custom" || result.Record["id"] != registered.User.ID.String() {
		t.Fatalf("auth response=%#v", result)
	}
	service := fixture.app.extensions.authAPI.(*userauth.Service)
	principal, err := service.AuthenticateForCollection(t.Context(), userauth.DefaultCollectionID, result.Token)
	if err != nil || principal.User.ID != registered.User.ID {
		t.Fatalf("issued principal=%#v error=%v", principal, err)
	}
	if _, err = service.IssueAuthResponseSession(t.Context(), uuid.New(), registered.User.ID, "extension:test"); err == nil {
		t.Fatal("cross-realm auth response session was issued")
	}
}

func TestJavaScriptRecordAuthResponseRequiresAdminAndCapability(t *testing.T) {
	for _, test := range []struct {
		name, access string
		caps         []string
		allowed      map[string]bool
	}{
		{name: "user cannot issue", access: "user", caps: []string{"app-control"}, allowed: map[string]bool{"app-control": true}},
		{name: "missing capability", access: "administrator"},
	} {
		t.Run(test.name, func(t *testing.T) {
			directory := t.TempDir()
			manifest := `{"format":"pgbase.scripts/v1","routes":[{"method":"POST","pattern":"/custom-auth","access":"` + test.access + `","handler":{"module":"route.js","export":"run"},"capabilities":` + jsonStrings(test.caps) + `}]}`
			writeJavaScriptFixture(t, directory, manifest, `exports.run=(e)=>$apis.recordAuthResponse(e,new Record(new Collection({name:e.json.collection}),{id:e.json.recordId}),"custom",{})`)
			fixture := newPublicUsersAppOptions(t, true, func(options *Options) {
				options.JavaScript = &JavaScriptOptions{Directory: directory, Capabilities: test.allowed}
			})
			adminCookie, csrf := setupPublicUsersAdministrator(t, fixture)
			registered := registerPublicUser(t, fixture, "denied-"+uuid.NewString()+"@example.test", publicUserOnePass)
			cookie, bearer := adminCookie, ""
			if test.access == "user" {
				cookie, bearer = nil, registered.Token
			}
			response := publicUsersRequest(t, fixture, http.MethodPost, "/custom-auth", map[string]any{"collection": userauth.DefaultCollectionID.String(), "recordId": registered.User.ID.String()}, bearer, cookie, map[string]string{"X-PgBase-CSRF": csrf})
			if response.Code < 400 {
				t.Fatalf("unauthorized custom auth status=%d body=%s", response.Code, response.Body.String())
			}
		})
	}
}

func TestJavaScriptEnrichAuthRecordUsesRecordAuthorization(t *testing.T) {
	directory := t.TempDir()
	writeJavaScriptFixture(t, directory, `{"format":"pgbase.scripts/v1","routes":[{"method":"POST","pattern":"/enrich","access":"user","handler":{"module":"route.js","export":"run"},"capabilities":["records"]}]}`, `exports.run=(e)=>{const r=new Record(new Collection({name:e.json.collection}),{id:e.json.recordId,email:"forged@example.test",password:"forged"});$apis.enrichRecord(e,r);return {status:200,json:r.export().data}}`)
	fixture := newPublicUsersAppOptions(t, true, func(options *Options) {
		options.JavaScript = &JavaScriptOptions{Directory: directory, Capabilities: map[string]bool{"records": true}}
	})
	first := registerPublicUser(t, fixture, "enrich-first@example.test", publicUserOnePass)
	second := registerPublicUser(t, fixture, "enrich-second@example.test", publicUserTwoPass)
	response := publicUsersRequest(t, fixture, http.MethodPost, "/enrich", map[string]any{"collection": userauth.DefaultCollectionID.String(), "recordId": first.User.ID.String()}, first.Token, nil, nil)
	assertPublicUsersStatus(t, response, http.StatusOK)
	var record map[string]any
	decodePublicUsersJSON(t, response, &record)
	if record["id"] != first.User.ID.String() || record["email"] != first.User.Email {
		t.Fatalf("enriched self=%#v", record)
	}
	if _, exists := record["password"]; exists {
		t.Fatalf("enriched response retained password: %#v", record)
	}
	foreign := publicUsersRequest(t, fixture, http.MethodPost, "/enrich", map[string]any{"collection": userauth.DefaultCollectionID.String(), "recordId": second.User.ID.String()}, first.Token, nil, nil)
	if foreign.Code < 400 {
		t.Fatalf("foreign auth record enriched status=%d body=%s", foreign.Code, foreign.Body.String())
	}
}

func jsonStrings(values []string) string {
	raw, _ := json.Marshal(values)
	return string(raw)
}

func TestNativeAuthHelpersResolvePublicRecordIDsAndProjectVisibleIdentity(t *testing.T) {
	fixture := newPublicUsersAppOptions(t, false, nil)
	setupPublicUsersAdministrator(t, fixture)
	var actor uuid.UUID
	if err := fixture.app.pool.QueryRow(t.Context(), `select id from pgbase_meta.admins where email=$1`, publicAdminEmail).Scan(&actor); err != nil {
		t.Fatal(err)
	}
	service := fixture.app.extensions.authAPI.(*userauth.Service)
	for _, kind := range []string{"text", "UUID-looking text", "default UUID"} {
		t.Run(kind, func(t *testing.T) {
			realm, collection, public := userauth.DefaultCollectionID, "users", ""
			if kind != "default UUID" {
				realm = uuid.New()
				collection = "helper_" + realm.String()[:8]
				public = "AuthVisible01"
				if kind == "UUID-looking text" {
					public = uuid.NewString()
				}
				policy := schema.RecordIDPolicy{Kind: schema.RecordIDText, MinLength: 1, MaxLength: 36, Pattern: `^[A-Za-z0-9-]+$`}
				if _, err := service.CreateAuthCollection(t.Context(), userauth.CreateAuthCollectionInput{ID: realm, Name: collection, RecordID: &policy, Rules: userauth.AuthCollectionRules{View: "@request.auth.id = id", Auth: "true"}}); err != nil {
					t.Fatal(err)
				}
			}
			record, err := service.CreateAuthRecord(t.Context(), realm, userauth.CreateAuthRecordInput{ID: public, Email: uuid.NewString() + "@example.test", Password: publicUserOnePass, Active: true})
			if err != nil {
				t.Fatal(err)
			}
			public = record.PublicID
			if kind != "default UUID" && public == record.ID.String() {
				t.Fatal("text mapping precondition failed")
			}
			host := &extensionHost{bridge: fixture.app.extensions, principal: identity.Subject{Kind: identity.Administrator, ID: actor}, requestID: "native-auth-helper", valid: true, authorityContext: t.Context()}
			request := extensionAuthRecordRequest{Collection: collection, RecordID: public, AuthMethod: "custom"}
			input, _ := json.Marshal(request)
			raw, err := host.Call(t.Context(), "apis.recordAuthResponse", input)
			if err != nil {
				t.Fatalf("public ID %q auth response: %v", public, err)
			}
			var result struct {
				JSON struct {
					Token  string `json:"token"`
					Record struct {
						ID string `json:"id"`
					} `json:"record"`
				} `json:"json"`
			}
			if err := json.Unmarshal(raw, &result); err != nil || result.JSON.Record.ID != public {
				t.Fatalf("auth identity=%s err=%v", raw, err)
			}
			principal, err := service.AuthenticateForCollection(t.Context(), realm, result.JSON.Token)
			if err != nil || principal.User.ID != record.ID {
				t.Fatalf("issued session principal=%+v err=%v", principal.User, err)
			}
			for _, method := range []string{"apis.enrichRecord", "apis.enrichRecords"} {
				body := input
				if method == "apis.enrichRecords" {
					body, _ = json.Marshal([]extensionAuthRecordRequest{request})
				}
				raw, err := host.Call(t.Context(), method, body)
				if err != nil {
					t.Fatalf("admin %s: %v", method, err)
				}
				var visible map[string]any
				if method == "apis.enrichRecords" {
					var records []map[string]any
					if err := json.Unmarshal(raw, &records); err != nil || len(records) != 1 {
						t.Fatalf("records=%s err=%v", raw, err)
					}
					visible = records[0]
				} else if err := json.Unmarshal(raw, &visible); err != nil {
					t.Fatal(err)
				}
				if visible["id"] != public {
					t.Fatalf("%s public identity=%s", method, raw)
				}
			}
			host.principal = identity.Subject{Kind: identity.User, ID: record.ID}
			if _, err := host.Call(t.Context(), "apis.recordAuthResponse", input); err == nil {
				t.Fatal("ordinary user issued auth response")
			}
			raw, err = host.Call(t.Context(), "apis.enrichRecord", input)
			if err != nil {
				t.Fatalf("owner enrich=%s err=%v", raw, err)
			}
			var owner struct {
				ID string `json:"id"`
			}
			if err := json.Unmarshal(raw, &owner); err != nil || owner.ID != public {
				t.Fatalf("owner identity=%s err=%v", raw, err)
			}
			host.principal = identity.Subject{Kind: identity.User, ID: uuid.New()}
			if _, err := host.Call(t.Context(), "apis.enrichRecord", input); err == nil {
				t.Fatal("foreign user enriched private auth record")
			}
			if kind != "default UUID" {
				host.principal = identity.Subject{Kind: identity.Administrator, ID: actor}
				request.RecordID = record.ID.String()
				body, _ := json.Marshal(request)
				for _, method := range []string{"apis.recordAuthResponse", "apis.enrichRecord", "apis.enrichRecords"} {
					input := body
					if method == "apis.enrichRecords" {
						input, _ = json.Marshal([]extensionAuthRecordRequest{request})
					}
					if _, err := host.Call(t.Context(), method, input); err == nil {
						t.Fatalf("%s accepted internal UUID fallback", method)
					}
				}
			}
		})
	}
}
