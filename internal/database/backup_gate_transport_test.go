package database

import (
	"context"
	"strings"
	"testing"
)

func TestBackupGateRejectsParsedInsecureTransportBeforeConnect(t *testing.T) {
	// Cancellation independently prevents DNS/network even if this validation regresses.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for _, query := range []string{"sslmode=disable", "sslmode=require", "sslmode=verify-full&sslmode=disable", "sslmode=verify-full&ssl=true"} {
		_, err := TryBackupGate(ctx, "postgres://fixture:fixture@network-must-not-run.invalid/db?"+query, true, false)
		if err == nil || Classify(err) != KindValidation || !strings.Contains(err.Error(), "verify-full") {
			t.Fatalf("%s: expected parsed transport rejection, got %v", query, err)
		}
	}
	// The explicit development policy allows parsed plaintext config through validation.
	if _, err := parsePoolConfig("postgres://fixture:fixture@network-must-not-run.invalid/db?sslmode=disable", true); err != nil {
		t.Fatal(err)
	}
}
