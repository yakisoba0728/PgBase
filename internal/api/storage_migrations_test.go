package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/yakisoba0728/PgBase/internal/filestore"
)

type storageMigrationCapture struct {
	startCalls int
	startedID  uuid.UUID
}

func (*storageMigrationCapture) Preview(context.Context, filestore.StorageUpdate) (filestore.StorageMigrationStatus, error) {
	return filestore.StorageMigrationStatus{}, nil
}
func (capture *storageMigrationCapture) Start(_ context.Context, id uuid.UUID, _ filestore.StorageUpdate) (filestore.StorageMigrationStatus, error) {
	capture.startCalls++
	capture.startedID = id
	return filestore.StorageMigrationStatus{ID: id, Status: "copying"}, nil
}
func (*storageMigrationCapture) Status(context.Context, uuid.UUID) (filestore.StorageMigrationStatus, error) {
	return filestore.StorageMigrationStatus{}, nil
}
func (*storageMigrationCapture) Latest(context.Context) (filestore.StorageMigrationStatus, error) {
	return filestore.StorageMigrationStatus{}, nil
}
func (*storageMigrationCapture) Resume(context.Context, uuid.UUID) error { return nil }
func (*storageMigrationCapture) Cancel(context.Context, uuid.UUID) error { return nil }

func TestStorageMigrationStartRequiresOneNonNilOperationID(t *testing.T) {
	valid := uuid.MustParse("019551d0-e9c0-7000-8000-0000000000aa")
	for _, test := range []struct {
		name   string
		values []string
		want   int
		calls  int
	}{
		{name: "missing", want: http.StatusBadRequest},
		{name: "malformed", values: []string{"not-a-uuid"}, want: http.StatusBadRequest},
		{name: "nil", values: []string{uuid.Nil.String()}, want: http.StatusBadRequest},
		{name: "duplicate", values: []string{valid.String(), uuid.NewString()}, want: http.StatusBadRequest},
		{name: "valid", values: []string{valid.String()}, want: http.StatusAccepted, calls: 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			capture := &storageMigrationCapture{}
			request := httptest.NewRequest(http.MethodPost, "/api/admin/storage/files/migrations", strings.NewReader(`{"driver":"local"}`))
			request.Header.Set("Content-Type", "application/json")
			for _, value := range test.values {
				request.Header.Add(operationIDHeader, value)
			}
			response := httptest.NewRecorder()
			(storageMigrationEndpoints{service: capture}).start(response, request)
			if response.Code != test.want || capture.startCalls != test.calls {
				t.Fatalf("status=%d calls=%d id=%s body=%s", response.Code, capture.startCalls, capture.startedID, response.Body.String())
			}
			if test.calls == 1 && capture.startedID != valid {
				t.Fatalf("started id=%s want=%s", capture.startedID, valid)
			}
		})
	}
}
