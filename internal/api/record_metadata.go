package api

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/google/uuid"
	"github.com/yakisoba0728/PgBase/internal/filestore"
	"github.com/yakisoba0728/PgBase/internal/identity"
	"github.com/yakisoba0728/PgBase/internal/recordops"
)

type recordFileMetadata interface {
	ListRecordMetadata(context.Context, identity.Subject, uuid.UUID, []string) (map[string]map[string][]filestore.File, error)
}

type recordFileMetadataFields interface {
	ListRecordMetadataFields(context.Context, identity.Subject, uuid.UUID, []string, []string) (map[string]map[string][]filestore.File, error)
}

func (e recordEndpoints) writeRecordPage(w http.ResponseWriter, r *http.Request, principal identity.Subject, page recordops.RecordPage) {
	if r.URL.Query().Get("files") != "true" {
		writeRecordReadJSON(w, r, page, "")
		return
	}
	if principal.Kind != identity.Administrator {
		writeFileError(w, r, filestore.ErrDenied)
		return
	}
	service, ok := e.files.(recordFileMetadata)
	if !ok {
		writeRecordReadJSON(w, r, page, "")
		return
	}
	id, err := e.files.CollectionID(r.Context(), r.PathValue("collection"))
	if err != nil {
		writeFileError(w, r, err)
		return
	}
	ids := make([]string, 0, len(page.Items))
	for _, item := range page.Items {
		if id, ok := item["id"].(string); ok {
			ids = append(ids, id)
		}
	}
	var metadata map[string]map[string][]filestore.File
	if selected, ok := e.files.(recordFileMetadataFields); ok && r.URL.Query().Get("fileFields") != "" {
		metadata, err = selected.ListRecordMetadataFields(r.Context(), principal, id, ids, strings.Split(r.URL.Query().Get("fileFields"), ","))
	} else {
		metadata, err = service.ListRecordMetadata(r.Context(), principal, id, ids)
	}
	if errors.Is(err, filestore.ErrRecordMetadataLimit) {
		writeRecordReadJSON(w, r, page, "")
		return
	}
	if err != nil {
		writeFileError(w, r, err)
		return
	}
	files := map[string]map[string][]fileResponse{}
	for id, fields := range metadata {
		files[id] = map[string][]fileResponse{}
		for field, items := range fields {
			values := make([]fileResponse, 0, len(items))
			for _, file := range items {
				values = append(values, publicFile(file))
			}
			files[id][field] = values
		}
	}
	writeRecordReadJSONWithFallback(w, r, struct {
		recordops.RecordPage
		FileMetadata map[string]map[string][]fileResponse `json:"fileMetadata"`
	}{page, files}, "", page)
}
