package pgbase

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/yakisoba0728/PgBase/internal/mailer"
)

func TestJavaScriptBootstrapClosuresDriveRouteHookAndJobAdapters(t *testing.T) {
	directory := t.TempDir()
	manifest := `{"format":"pgbase.scripts/v1","bootstrap":"bootstrap.js","routes":[{"method":"GET","pattern":"/boot","access":"public","handler":{"module":"route.js","export":"placeholder"}}],"hooks":[{"event":"mail.send","before":{"module":"route.js","export":"placeholder"}}],"jobs":[{"name":"boot-job","schedule":"0 * * * *","handler":{"module":"route.js","export":"placeholder"}}]}`
	writeJavaScriptFixture(t, directory, manifest, `exports.placeholder=()=>({})`)
	if err := os.WriteFile(filepath.Join(directory, "bootstrap.js"), []byte(`const marker="closure";routerAdd("GET","/boot",()=>({status:200,json:{marker}}));onMailerSend((event)=>({patch:{to:event.data.to,subject:marker,text:event.data.text}}));cronAdd("boot-job","0 * * * *",(run)=>{if(run.name!=="boot-job")throw new Error("job")})`), 0600); err != nil {
		t.Fatal(err)
	}
	options, err := (JavaScriptOptions{Directory: directory, JobServiceSubjects: map[string]uuid.UUID{"boot-job": uuid.New()}}).normalized()
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := prepareJavaScript(options)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Close(context.Background()) })
	route := runtime.routes()[0]
	response := httptest.NewRecorder()
	route.Handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/boot", nil))
	if response.Code != 200 || response.Body.String() != "{\"marker\":\"closure\"}" {
		t.Fatalf("route=%d %q", response.Code, response.Body.String())
	}
	registry := &HookRegistry{}
	if err := runtime.installHooks(registry); err != nil {
		t.Fatal(err)
	}
	message := &mailer.Message{To: "a@example.test", Subject: "before", Text: "body"}
	if err := registry.Run(context.Background(), &HookEvent{Kind: HookMailSend, Data: message}, func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	if message.Subject != "closure" {
		t.Fatalf("subject=%q", message.Subject)
	}
	appOptions := Options{JobsEnabled: true}
	if err := installJavaScriptJobs(&appOptions, runtime); err != nil {
		t.Fatal(err)
	}
	if err := appOptions.Jobs[0].Handler(context.Background(), JobRun{JobID: uuid.New(), Name: "boot-job", FenceGeneration: 1, ScheduledAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
}
