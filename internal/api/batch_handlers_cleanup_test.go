package api

import (
	"context"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/yakisoba0728/PgBase/internal/filestore"
)

type blockingBatchCleanupFiles struct {
	FileService
	seen chan context.Context
}

func (f *blockingBatchCleanupFiles) Cleanup(ctx context.Context, _ []filestore.StagedFile) error {
	f.seen <- ctx
	<-ctx.Done()
	return ctx.Err()
}

func TestBatchCleanupUsesFreshBoundedContextAfterRequestCancellation(t *testing.T) {
	request := httptest.NewRequest("POST", "/api/v1/collections/widgets/records/batch", nil)
	canceled, cancel := context.WithCancel(request.Context())
	cancel()
	request = request.WithContext(canceled)
	files := &blockingBatchCleanupFiles{seen: make(chan context.Context, 1)}
	done := make(chan struct{})
	go func() {
		(recordEndpoints{files: files}).cleanupStaged(request, namedStagedFiles{"file": {}})
		close(done)
	}()
	ctx := <-files.seen
	if err := ctx.Err(); err != nil {
		t.Fatalf("cleanup inherited request cancellation: %v", err)
	}
	deadline, ok := ctx.Deadline()
	if !ok || time.Until(deadline) <= 0 || time.Until(deadline) > batchStagedCleanupTimeout+100*time.Millisecond {
		t.Fatalf("cleanup deadline = %v, present=%t", deadline, ok)
	}
	select {
	case <-done:
	case <-time.After(batchStagedCleanupTimeout + time.Second):
		t.Fatal("cleanup did not return at its bounded deadline")
	}
}
