package api

import (
	"context"
	"github.com/google/uuid"
	"github.com/yakisoba0728/PgBase/internal/filestore"
	"github.com/yakisoba0728/PgBase/internal/identity"
	"github.com/yakisoba0728/PgBase/internal/recordops"
	"net/http"
)

type recordFileMetadata interface {
	ListRecordMetadata(context.Context, identity.Subject, uuid.UUID, []string) (map[string]map[string][]filestore.File, error)
}

func (e recordEndpoints) writeRecordPage(w http.ResponseWriter, r *http.Request, principal identity.Subject, page recordops.RecordPage) {
	if r.URL.Query().Get("files") != "true" {
		writeJSON(w, http.StatusOK, page)
		return
	}
	if principal.Kind != identity.Administrator {
		writeFileError(w, r, filestore.ErrDenied)
		return
	}
	service, ok := e.files.(recordFileMetadata)
	if !ok {
		writeJSON(w, http.StatusOK, page)
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
	metadata, err := service.ListRecordMetadata(r.Context(), principal, id, ids)
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
	writeJSON(w, http.StatusOK, struct {
		recordops.RecordPage
		FileMetadata map[string]map[string][]fileResponse `json:"fileMetadata"`
	}{page, files})
}
