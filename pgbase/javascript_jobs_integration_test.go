//go:build integration

package pgbase

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestJavaScriptDurableJobRetriesWithStableTaskKeyAndPreservesSchedule(t *testing.T) {
	bootstrap := newPublicUsersApp(t, false)
	adminCookie, csrf := setupPublicUsersAdministrator(t, bootstrap)
	createPublicUsersCollection(t, bootstrap, adminCookie, csrf, "job_tasks")
	var collectionID uuid.UUID
	if err := bootstrap.app.pool.QueryRow(context.Background(), `select id from pgbase_meta.collections where name='job_tasks'`).Scan(&collectionID); err != nil {
		t.Fatal(err)
	}
	issued := publicUsersRequest(t, bootstrap, http.MethodPost, "/api/admin/service-tokens", map[string]any{
		"name": "n6 job", "ttlSeconds": 3600, "scopes": []map[string]any{{"collectionId": collectionID.String(), "action": "create"}},
	}, "", adminCookie, map[string]string{"X-PgBase-CSRF": csrf})
	assertPublicUsersStatus(t, issued, http.StatusCreated)
	var token struct {
		ID uuid.UUID `json:"id"`
	}
	decodePublicUsersJSON(t, issued, &token)
	databaseURL := bootstrap.app.options.DatabaseURL
	if err := bootstrap.app.Close(context.Background()); err != nil {
		t.Fatal(err)
	}

	directory := t.TempDir()
	manifest := `{
 "format":"pgbase.scripts/v1",
 "jobs":[
  {"name":"retry_task","schedule":"@every 1h","handler":{"module":"job.js","export":"run"},"capabilities":["records"]},
  {"name":"calendar_task","schedule":"15 4 * * *","timezone":"Asia/Seoul","handler":{"module":"job.js","export":"calendar"}}
 ]
}`
	source := `const pg=require("pgbase");
exports.run=function(run){
 const result=pg.records.create({collection:"job_tasks",input:{title:run.jobId},idempotencyKey:"job-"+run.jobId});
 if(String(run.fenceGeneration)==="1") throw new Error("fail after record commit");
 if(!result.replayed) throw new Error("retry did not replay stable task");
 return {completed:true};
};
exports.calendar=function(){return {completed:true};};`
	if err := os.WriteFile(filepath.Join(directory, "pgbase.scripts.json"), []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "job.js"), []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	options := bootstrap.app.options
	options.AutoMigrate = false
	options.HTTPAddress = unusedAddress(t)
	options.JobsEnabled = true
	options.Jobs = nil
	options.JavaScript = &JavaScriptOptions{Directory: directory, Capabilities: map[string]bool{"records": true},
		JobServiceSubjects: map[string]uuid.UUID{"retry_task": token.ID, "calendar_task": token.ID}}
	options.DatabaseURL = databaseURL
	app, err := New(context.Background(), options)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	if err := app.Start(ctx); err != nil {
		cancel()
		_ = app.Close(context.Background())
		t.Fatal(err)
	}
	deadline := time.Now().Add(8 * time.Second)
	for {
		var attempts, records int
		var fence int64
		var lastError *string
		err := app.pool.QueryRow(context.Background(), `select attempts,fence_generation,last_error,(select count(*) from app.job_tasks) from pgbase_meta.scheduled_jobs where name='retry_task'`).Scan(&attempts, &fence, &lastError, &records)
		if err == nil && fence >= 2 && attempts == 0 && lastError == nil && records == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("job did not complete idempotent retry: attempts=%d fence=%d lastError=%v records=%d error=%v", attempts, fence, lastError, records, err)
		}
		time.Sleep(100 * time.Millisecond)
	}
	var calendarTimezone string
	if err := app.pool.QueryRow(context.Background(), `select timezone from pgbase_meta.scheduled_jobs where name='calendar_task'`).Scan(&calendarTimezone); err != nil || calendarTimezone != "Asia/Seoul" {
		t.Fatalf("calendar timezone=%q error=%v", calendarTimezone, err)
	}
	if _, err := app.pool.Exec(context.Background(), `update pgbase_meta.scheduled_jobs set schedule='30 5 * * *',timezone='UTC',enabled=false where name='calendar_task'`); err != nil {
		t.Fatal(err)
	}
	cancel()
	if err := app.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	restarted, err := New(context.Background(), options)
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Close(context.Background())
	var schedule, timezone string
	var enabled bool
	if err := restarted.pool.QueryRow(context.Background(), `select schedule,timezone,enabled from pgbase_meta.scheduled_jobs where name='calendar_task'`).Scan(&schedule, &timezone, &enabled); err != nil {
		t.Fatal(err)
	}
	if schedule != "30 5 * * *" || timezone != "UTC" || enabled {
		t.Fatalf("administrator schedule was overwritten: schedule=%q timezone=%q enabled=%t", schedule, timezone, enabled)
	}
}
