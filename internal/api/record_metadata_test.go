package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/yakisoba0728/PgBase/internal/filestore"
	"github.com/yakisoba0728/PgBase/internal/identity"
	"github.com/yakisoba0728/PgBase/internal/recordops"
)

type cappedMetadataFiles struct {
	FileService
}

func (cappedMetadataFiles) CollectionID(context.Context, string) (uuid.UUID, error) {
	return uuid.New(), nil
}

func (cappedMetadataFiles) ListRecordMetadata(context.Context, identity.Subject, uuid.UUID, []string) (map[string]map[string][]filestore.File, error) {
	return nil, filestore.ErrRecordMetadataLimit
}

func TestFileMetadataCapKeepsTheRecordPage(t *testing.T) {
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/v1/collections/notes/records?files=true", nil)
	request.SetPathValue("collection", "notes")
	principal := identity.Subject{Kind: identity.Administrator, ID: uuid.New()}
	page := recordops.RecordPage{Items: []map[string]any{{"id": "one"}}, NextCursor: "cursor"}
	recordEndpoints{files: cappedMetadataFiles{}}.writeRecordPage(response, request, principal, page)
	body := response.Body.String()
	if response.Code != http.StatusOK || !strings.Contains(body, `"id":"one"`) || !strings.Contains(body, `"nextCursor":"cursor"`) || strings.Contains(body, "fileMetadata") {
		t.Fatalf("status=%d body=%s", response.Code, body)
	}
}
