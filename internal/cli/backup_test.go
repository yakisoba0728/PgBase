package cli

import (
	"context"
	"errors"
	"testing"
)

type failedGateMonitor struct{ err error }

func (m failedGateMonitor) Run(context.Context) error { return m.err }

func TestRunMonitoredBackupCancelsOperationWhenGateIsLost(t *testing.T) {
	lost := errors.New("exclusive gate session lost")
	operationStopped := make(chan struct{})
	err := runMonitoredBackup(context.Background(), failedGateMonitor{lost}, func(ctx context.Context) error {
		<-ctx.Done()
		close(operationStopped)
		return ctx.Err()
	})
	if !errors.Is(err, lost) {
		t.Fatalf("runMonitoredBackup error = %v", err)
	}
	select {
	case <-operationStopped:
	default:
		t.Fatal("operation was not cancelled")
	}
}
