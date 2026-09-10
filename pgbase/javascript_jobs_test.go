package pgbase

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestInstallJavaScriptJobsRequiresServiceSubjectAndInvokesHandler(t *testing.T) {
	directory := t.TempDir()
	writeJavaScriptFixture(t, directory, `{
 "format":"pgbase.scripts/v1",
 "jobs":[{"name":"cleanup","schedule":"0 3 * * *","timezone":"Asia/Seoul","handler":{"module":"route.js","export":"run"}}]
}`, `exports.run = function (run) {
  if (run.name !== "cleanup" || run.runKey.indexOf(run.jobId + ":") !== 0 ||
      run.occurrenceKey.indexOf(run.jobId + "-") !== 0 || run.scheduledAt !== "2026-09-10T06:00:00Z") throw new Error("job metadata");
  return {completed:true};
};`)
	options, err := (JavaScriptOptions{Directory: directory}).normalized()
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := prepareJavaScript(options)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Close(context.Background()) })
	appOptions := Options{JobsEnabled: true}
	if err := installJavaScriptJobs(&appOptions, runtime); err == nil || !strings.Contains(err.Error(), "JobServiceSubjects") {
		t.Fatalf("missing service subject error = %v", err)
	}

	runtime.options.JobServiceSubjects = map[string]uuid.UUID{"cleanup": uuid.New()}
	if err := installJavaScriptJobs(&appOptions, runtime); err != nil {
		t.Fatal(err)
	}
	if len(appOptions.Jobs) != 1 || appOptions.Jobs[0].Timezone != "Asia/Seoul" {
		t.Fatalf("jobs = %#v", appOptions.Jobs)
	}
	jobID := uuid.New()
	if err := appOptions.Jobs[0].Handler(context.Background(), JobRun{JobID: jobID, Name: "cleanup", FenceGeneration: 7, ScheduledAt: time.Date(2026, 9, 10, 6, 0, 0, 0, time.UTC)}); err != nil {
		t.Fatal(err)
	}
}
