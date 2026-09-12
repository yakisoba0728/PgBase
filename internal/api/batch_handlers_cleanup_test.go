package api

import (
	"bytes"
	"context"
	"mime/multipart"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/yakisoba0728/PgBase/internal/filestore"
	"github.com/yakisoba0728/PgBase/internal/identity"
)

type blockingBatchCleanupFiles struct {
	FileService
	seen chan context.Context
}

func TestMultipartBatchRequestsUseStrictJSONValidation(t *testing.T) {
	for _, test := range []struct {
		name string
		body string
	}{
		{name: "unknown field", body: `{"requests":[],"unexpected":true}`},
		{name: "duplicate field", body: `{"requests":[],"requests":[{"method":"POST","url":"/api/v1/collections/posts/records","body":{}}]}`},
		{name: "trailing document", body: `{"requests":[]} {"requests":[]}`},
		{name: "invalid unicode", body: "{\"requests\":[{\"method\":\"\xff\",\"url\":\"/api/v1/collections/posts/records\",\"body\":{}}]}"},
	} {
		t.Run(test.name, func(t *testing.T) {
			var body bytes.Buffer
			writer := multipart.NewWriter(&body)
			part, err := writer.CreateFormField("requests")
			if err != nil {
				t.Fatal(err)
			}
			if _, err := part.Write([]byte(test.body)); err != nil {
				t.Fatal(err)
			}
			if err := writer.Close(); err != nil {
				t.Fatal(err)
			}
			request := httptest.NewRequest("POST", "/api/v1/collections/batch", &body)
			request.Header.Set("Content-Type", writer.FormDataContentType())

			_, staged, err := (recordEndpoints{files: &blockingBatchCleanupFiles{}}).decodeBatch(request, identity.Subject{})
			if err == nil || len(staged) != 0 {
				t.Fatalf("decodeBatch error=%v staged=%v", err, staged)
			}
		})
	}
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
