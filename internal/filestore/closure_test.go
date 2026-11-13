package filestore

import (
	"context"
	"strings"
	"testing"
)

func TestStorageOperationReusesOnlyItsOwnLease(t *testing.T) {
	controller := &StorageController{}
	ctx := context.WithValue(t.Context(), storageLeaseContextKey{}, storageLease{controller: controller})
	reused, release, err := controller.BeginStorageOperation(ctx)
	if err != nil || reused != ctx {
		t.Fatalf("context=%v err=%v", reused, err)
	}
	release()
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, _, err := controller.BeginStorageOperation(canceled); err != context.Canceled {
		t.Fatalf("canceled lease: %v", err)
	}
}

func TestAuthFileFieldNameLength(t *testing.T) {
	if !fieldPattern.MatchString("f"+strings.Repeat("x", 63)) || fieldPattern.MatchString("f"+strings.Repeat("x", 64)) {
		t.Fatal("file field bounds differ from auth schema")
	}
}
