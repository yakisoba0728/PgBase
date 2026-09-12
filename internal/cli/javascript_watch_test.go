package cli

import (
	"context"
	"errors"
	"testing"
)

func TestJavaScriptWatchTreatsCanceledNilRunAsCleanShutdown(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := fmtWatchedApplicationStopped(ctx, nil); err != nil {
		t.Fatalf("canceled watch returned %v", err)
	}
}

func TestJavaScriptWatchReportsUnexpectedNilRun(t *testing.T) {
	err := fmtWatchedApplicationStopped(context.Background(), nil)
	if err == nil || !errors.Is(err, errWatchedApplicationStopped) {
		t.Fatalf("unexpected nil run error=%v", err)
	}
}

func TestJavaScriptWatchPreservesRunError(t *testing.T) {
	runErr := errors.New("serve failed")
	if err := fmtWatchedApplicationStopped(context.Background(), runErr); !errors.Is(err, runErr) {
		t.Fatalf("watch error=%v, want wrapped run error", err)
	}
}
