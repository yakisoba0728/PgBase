package schema

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"
)

func TestSchemaOperationMaintenanceRetriesUnavailableFailures(t *testing.T) {
	service := &Service{maintenanceRetryMin: time.Millisecond, maintenanceRetryMax: time.Millisecond}
	attempts := 0
	err := service.runSchemaOperationMaintenance(context.Background(), func(context.Context) error {
		attempts++
		if attempts < 3 {
			return io.ErrUnexpectedEOF
		}
		return nil
	})
	if err != nil {
		t.Fatalf("runSchemaOperationMaintenance() error = %v", err)
	}
	if attempts != 3 {
		t.Fatalf("maintenance attempts = %d, want 3", attempts)
	}
}

func TestSchemaOperationMaintenanceDoesNotRetryIntegrityFailures(t *testing.T) {
	service := &Service{maintenanceRetryMin: time.Millisecond, maintenanceRetryMax: time.Millisecond}
	attempts := 0
	err := service.runSchemaOperationMaintenance(context.Background(), func(context.Context) error {
		attempts++
		return errSchemaOperationAuditIntegrity
	})
	if !errors.Is(err, errSchemaOperationAuditIntegrity) {
		t.Fatalf("runSchemaOperationMaintenance() error = %v, want audit integrity error", err)
	}
	if attempts != 1 {
		t.Fatalf("maintenance attempts = %d, want 1", attempts)
	}
}

func TestSchemaOperationMaintenanceRetryStopsOnCancellation(t *testing.T) {
	service := &Service{maintenanceRetryMin: time.Hour, maintenanceRetryMax: time.Hour}
	ctx, cancel := context.WithCancel(context.Background())
	attempted := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- service.runSchemaOperationMaintenance(ctx, func(context.Context) error {
			close(attempted)
			return io.EOF
		})
	}()
	<-attempted
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("runSchemaOperationMaintenance() error = %v, want context canceled", err)
	}
}
