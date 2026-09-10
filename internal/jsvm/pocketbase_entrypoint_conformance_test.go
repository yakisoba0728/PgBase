package jsvm_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/yakisoba0728/PgBase/internal/jsvm"
)

// This inventory follows PocketBase v0.40.3 plugins/jsvm/binds.go and the
// executable expectations in plugins/jsvm/binds_test.go. It exercises the JS
// entrypoints rather than their underlying Go helpers.
func TestPocketBaseClaimedGlobalEntrypointsHaveCallableShapes(t *testing.T) {
	directory := t.TempDir()
	writeFile(t, directory, "pgbase.scripts.json", `{"format":"pgbase.scripts/v1","routes":[{"method":"GET","pattern":"/surface","access":"public","handler":{"module":"route.js","export":"run"}}]}`)
	writeFile(t, directory, "route.js", `exports.run=()=>{
const constructors=["Record","Collection","FieldsList","Field","NumberField","BoolField","TextField","URLField","EmailField","EditorField","PasswordField","DateField","AutodateField","JSONField","RelationField","SelectField","FileField","GeoPointField","MailerMessage","RequestInfo","Command","Middleware","Timezone","FormData"];
const namespaces={security:["md5","sha256","sha512","hs256","hs512","equal","randomString","randomStringByRegex","randomStringWithAlphabet","pseudorandomString","pseudorandomStringWithAlphabet","parseUnverifiedJWT","parseJWT","createJWT","encrypt","decrypt"],dbx:["exp","hashExp","not","and","or","in","notIn","like","orLike","notLike","orNotLike","exists","notExists","between","notBetween"],filesystem:["s3","local","fileFromPath","fileFromBytes","fileFromMultipart","fileFromURL"],filepath:["base","clean","dir","ext","fromSlash","glob","isAbs","join","match","rel","split","splitList","toSlash","walk","walkDir"],os:["args","exec","cmd","exit","getenv","dirFS","stat","readFile","writeFile","readDir","tempDir","truncate","getwd","mkdir","mkdirAll","rename","remove","removeAll","openRoot","openInRoot"],apis:["static","requireGuestOnly","requireAuth","requireSuperuserAuth","requireSuperuserOrOwnerAuth","skipSuccessActivityLog","gzip","bodyLimit","recordAuthResponse","enrichRecord","enrichRecords"],http:["send"],mails:["sendRecordPasswordReset","sendRecordVerification","sendRecordChangeEmail","sendRecordOTP","sendRecordAuthAlert"]};
const globals={security:$security,dbx:$dbx,filesystem:$filesystem,filepath:$filepath,os:$os,apis:$apis,http:$http,mails:$mails},missing={};
for(const n of Object.keys(namespaces)){missing[n]=namespaces[n].filter(k=>{const d=Object.getOwnPropertyDescriptor(globals[n],k);return !d||(!d.get&&typeof d.value!=="function")})}
return {constructors:constructors.filter(n=>typeof globalThis[n]!=="function"),namespaces:missing};}`)
	bundle, err := jsvm.LoadBundle(directory)
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := jsvm.NewRuntime(bundle, jsvm.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Close(context.Background()) })
	out, err := runtime.Invoke(context.Background(), jsvm.Invocation{Handler: bundle.Manifest().Routes[0].Handler})
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != `{"constructors":[],"namespaces":{"apis":[],"dbx":[],"filepath":[],"filesystem":[],"http":[],"mails":[],"os":[],"security":[]}}` {
		t.Fatalf("PocketBase public surface mismatch: %s", out)
	}
}

func TestPocketBaseDBXAndBodyLimitWrappersExecuteLikePublicAPI(t *testing.T) {
	directory := t.TempDir()
	writeFile(t, directory, "pgbase.scripts.json", `{"format":"pgbase.scripts/v1","routes":[{"method":"GET","pattern":"/shape","access":"public","handler":{"module":"route.js","export":"run"}}]}`)
	writeFile(t, directory, "route.js", `exports.run=()=>{
const inExpr=$dbx.in("id",1,2,3), likeExpr=$dbx.like("title","a","b").match(true,false);
if(typeof inExpr.build!=="function"||typeof likeExpr.build!=="function")throw new Error("dbx expression is not buildable");
const limited=$apis.bodyLimit(64)((e)=>({ok:e.body.value}));
return {inSQL:inExpr.build({},{}),likeSQL:likeExpr.build({},{}),limited:limited({body:{value:"accepted"}})};}`)
	bundle, err := jsvm.LoadBundle(directory)
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := jsvm.NewRuntime(bundle, jsvm.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Close(context.Background()) })
	if _, err = runtime.Invoke(context.Background(), jsvm.Invocation{Handler: bundle.Manifest().Routes[0].Handler}); err != nil {
		t.Fatalf("PocketBase-shaped dbx/bodyLimit call failed: %v", err)
	}
}

type mailEntryHost struct{ calls []string }

func (h *mailEntryHost) Call(_ context.Context, method string, input json.RawMessage) (json.RawMessage, error) {
	h.calls = append(h.calls, method+":"+string(input))
	return json.RawMessage(`{"sent":true}`), nil
}

func TestPocketBaseMailWrappersForwardPublicSignatures(t *testing.T) {
	directory := t.TempDir()
	writeFile(t, directory, "pgbase.scripts.json", `{"format":"pgbase.scripts/v1","routes":[{"method":"GET","pattern":"/mail","access":"public","handler":{"module":"route.js","export":"run"},"capabilities":["mail"]}]}`)
	writeFile(t, directory, "route.js", `exports.run=()=>{const c=new Collection({id:"realm-id",name:"members"}),r=new Record(c,{id:"record-id"});$mails.sendRecordPasswordReset($app,r);$mails.sendRecordVerification($app,r);$mails.sendRecordChangeEmail($app,r,"new@example.test");$mails.sendRecordOTP($app,r,"otp-id","otp-pass");$mails.sendRecordAuthAlert($app,r,{ip:"127.0.0.1"});return {ok:true}}`)
	bundle, err := jsvm.LoadBundle(directory)
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := jsvm.NewRuntime(bundle, jsvm.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Close(context.Background()) })
	host := &mailEntryHost{}
	_, err = runtime.Invoke(context.Background(), jsvm.Invocation{Handler: bundle.Manifest().Routes[0].Handler, Capabilities: []string{"mail"}, Host: host})
	if err != nil {
		t.Fatal(err)
	}
	if len(host.calls) != 5 {
		t.Fatalf("auth mail calls=%v", host.calls)
	}
	for _, call := range host.calls {
		if !strings.HasPrefix(call, "authMail.send:") || !strings.Contains(call, `"collection":"members"`) || !strings.Contains(call, `"recordId":"record-id"`) {
			t.Fatalf("invalid auth mail bridge call %s", call)
		}
	}
	if !strings.Contains(host.calls[3], `"otpId":"otp-id"`) || !strings.Contains(host.calls[3], `"otpPass":"otp-pass"`) || !strings.Contains(host.calls[4], `"alertInfo":{"ip":"127.0.0.1"}`) {
		t.Fatalf("auth mail metadata lost: %v", host.calls)
	}
}
