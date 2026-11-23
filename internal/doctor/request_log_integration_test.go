//go:build integration

package doctor

import (
	"context"
	"strings"
	"testing"
)

func TestRequestLogWarningsReportACleanupBacklog(t *testing.T) {
	ctx := context.Background()
	pool := newDoctorPool(t)
	codes := func(maximumRows int) []Finding {
		t.Helper()
		findings, err := RequestLogWarnings(ctx, pool, maximumRows)
		if err != nil {
			t.Fatal(err)
		}
		return findings
	}
	if got := codes(10); len(got) != 0 {
		t.Fatalf("empty request log: %+v", got)
	}
	if _, err := pool.Exec(ctx, `insert into pgbase_meta.request_logs (occurred_at,method,route,status,duration_ms)
		select timestamptz '2026-01-02T03:04:05Z'+make_interval(secs=>n),'GET','/x',200,0 from generate_series(1,20) n`); err != nil {
		t.Fatal(err)
	}
	// Exactly twice the cap is within what a lagging cleanup leaves behind.
	if got := codes(10); len(got) != 0 {
		t.Fatalf("request log at twice its cap: %+v", got)
	}
	got := codes(9)
	if len(got) != 1 || got[0].Code != requestLogCleanupBehindCode || got[0].Severity != SeverityWarning {
		t.Fatalf("request log past twice its cap: %+v", got)
	}
	for _, want := range []string{"spans 20 entries", "back to 2026-01-02T03:04:06Z", "row cap of 9"} {
		if !strings.Contains(got[0].Message, want) {
			t.Fatalf("message %q does not contain %q", got[0].Message, want)
		}
	}
	// The default cap applies when none is configured.
	if got := codes(0); len(got) != 0 {
		t.Fatalf("default cap: %+v", got)
	}
}
