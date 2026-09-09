package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"mime"
	"net/http"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/webisnoteasy/PgBase/internal/filestore"
	"github.com/webisnoteasy/PgBase/internal/identity"
)

type FileService interface {
	CollectionID(context.Context, string) (uuid.UUID, error)
	Upload(context.Context, identity.Subject, filestore.Upload) (filestore.File, error)
	List(context.Context, identity.Subject, uuid.UUID, uuid.UUID, string) ([]filestore.File, error)
	Open(context.Context, identity.Subject, uuid.UUID, filestore.Variant, string) (filestore.File, io.ReadCloser, error)
	Delete(context.Context, identity.Subject, uuid.UUID) error
	MintDownloadToken(context.Context, identity.Subject, uuid.UUID, filestore.Variant) (string, error)
	Stage(context.Context, identity.Subject, filestore.Upload) (filestore.StagedFile, error)
	Commit(context.Context, pgx.Tx, identity.Subject, []filestore.AttachmentChange) ([]filestore.File, error)
	Cleanup(context.Context, []filestore.StagedFile) error
	CompleteStaged([]filestore.StagedFile, error)
}

func fileRoutes(dependencies Dependencies) []route {
	if dependencies.Files == nil {
		return nil
	}
	user := fileEndpoints{service: dependencies.Files}
	admin := fileEndpoints{service: dependencies.Files, administrator: true}
	routes := make([]route, 0, 10)
	for _, endpoint := range []struct {
		prefix                  string
		accessRead, accessWrite accessPolicy
		handler                 fileEndpoints
	}{
		{"/api/v1/user", accessUserSession, accessUserSession, user},
		{"/api/admin", accessAdminSameOriginRead, accessAdminSession, admin},
	} {
		base := endpoint.prefix + "/collections/{collection}/records/{id}/files/{field}"
		file := endpoint.prefix + "/files/{fileId}"
		routes = append(routes,
			route{method: "GET", pattern: base, access: endpoint.accessRead, response: atomicJSON(maximumAdminResponseBytes), deadline: deadlineDefault, handler: http.HandlerFunc(endpoint.handler.list)},
			route{method: "POST", pattern: base, access: endpoint.accessWrite, response: atomicJSON(maximumAdminResponseBytes), deadline: deadlineDefault, maximumRequestBytes: dependencies.MaximumFileUploadBytes, streamedUpload: true, handler: http.HandlerFunc(endpoint.handler.upload)},
			route{method: "GET", pattern: file, access: endpoint.accessRead, response: passthrough(), deadline: deadlineDefault, handler: http.HandlerFunc(endpoint.handler.download)},
			route{method: "DELETE", pattern: file, access: endpoint.accessWrite, response: atomicJSON(maximumAdminResponseBytes), deadline: deadlineDefault, handler: http.HandlerFunc(endpoint.handler.delete)},
			route{method: "POST", pattern: file + "/token", access: endpoint.accessWrite, response: atomicJSON(maximumAdminResponseBytes), deadline: deadlineDefault, maximumRequestBytes: maximumCredentialRequestBytes, handler: http.HandlerFunc(endpoint.handler.token)},
		)
	}
	return routes
}

type fileEndpoints struct {
	service       FileService
	administrator bool
}

func (e fileEndpoints) principal(r *http.Request) (identity.Subject, bool) {
	if e.administrator {
		admin, ok := AdminPrincipalFromContext(r.Context())
		return identity.Subject{Kind: identity.Administrator, ID: admin.Administrator.ID}, ok
	}
	user, ok := r.Context().Value(userPrincipalKey{}).(authenticatedUser)
	return identity.Subject{Kind: identity.User, ID: user.principal.User.ID}, ok
}

func (e fileEndpoints) resource(r *http.Request) (identity.Subject, uuid.UUID, uuid.UUID, string, error) {
	principal, ok := e.principal(r)
	if !ok || !principal.Valid() {
		return identity.Subject{}, uuid.Nil, uuid.Nil, "", filestore.ErrDenied
	}
	collectionID, err := e.service.CollectionID(r.Context(), r.PathValue("collection"))
	if err != nil {
		return identity.Subject{}, uuid.Nil, uuid.Nil, "", err
	}
	recordID, err := uuid.Parse(r.PathValue("id"))
	if err != nil || recordID == uuid.Nil || r.PathValue("field") == "" {
		return identity.Subject{}, uuid.Nil, uuid.Nil, "", filestore.ErrInvalid
	}
	return principal, collectionID, recordID, r.PathValue("field"), nil
}

func (e fileEndpoints) list(w http.ResponseWriter, r *http.Request) {
	principal, collectionID, recordID, field, err := e.resource(r)
	if err != nil {
		writeFileError(w, r, err)
		return
	}
	files, err := e.service.List(r.Context(), principal, collectionID, recordID, field)
	if err != nil {
		writeFileError(w, r, err)
		return
	}
	items := make([]fileResponse, 0, len(files))
	for _, file := range files {
		items = append(items, publicFile(file))
	}
	writeJSON(w, http.StatusOK, struct {
		Items []fileResponse `json:"items"`
	}{items})
}

func (e fileEndpoints) upload(w http.ResponseWriter, r *http.Request) {
	principal, collectionID, recordID, field, err := e.resource(r)
	if err != nil {
		writeFileError(w, r, err)
		return
	}
	size, err := strconv.ParseInt(r.Header.Get("X-PgBase-File-Size"), 10, 64)
	var expected [sha256.Size]byte
	digest, digestErr := hex.DecodeString(r.Header.Get("X-PgBase-File-SHA256"))
	if err != nil || size < 0 || digestErr != nil || len(digest) != sha256.Size {
		writeFileError(w, r, filestore.ErrInvalid)
		return
	}
	copy(expected[:], digest)
	multipart, err := r.MultipartReader()
	if err != nil {
		writeFileError(w, r, filestore.ErrInvalid)
		return
	}
	part, err := multipart.NextPart()
	if err != nil || part.FormName() != "file" || part.FileName() == "" {
		writeFileError(w, r, filestore.ErrInvalid)
		return
	}
	contentType := strings.TrimSpace(strings.Split(part.Header.Get("Content-Type"), ";")[0])
	file, err := e.service.Upload(r.Context(), principal, filestore.Upload{CollectionID: collectionID, RecordID: recordID, Field: field, Filename: part.FileName(), ContentType: contentType, Size: size, SHA256: expected, Body: part})
	_ = part.Close()
	if err != nil {
		writeFileError(w, r, err)
		return
	}
	if extra, nextErr := multipart.NextPart(); nextErr != io.EOF {
		if extra != nil {
			_ = extra.Close()
		}
		_ = e.service.Delete(context.WithoutCancel(r.Context()), principal, file.ID)
		writeFileError(w, r, filestore.ErrInvalid)
		return
	}
	writeJSON(w, http.StatusCreated, publicFile(file))
}

func (e fileEndpoints) download(w http.ResponseWriter, r *http.Request) {
	principal, ok := e.principal(r)
	id, err := uuid.Parse(r.PathValue("fileId"))
	variant := filestore.Variant(r.URL.Query().Get("variant"))
	if variant == "" {
		variant = filestore.Original
	}
	values := r.URL.Query()
	tokens, tokenOK := values["token"]
	variants, variantOK := values["variant"]
	if !ok || err != nil || id == uuid.Nil || len(values) > 2 || !tokenOK || len(tokens) != 1 || tokens[0] == "" || (variantOK && len(variants) != 1) {
		writeFileError(w, r, filestore.ErrInvalid)
		return
	}
	file, reader, err := e.service.Open(r.Context(), principal, id, variant, values.Get("token"))
	if err != nil {
		writeFileError(w, r, err)
		return
	}
	defer func() { _ = reader.Close() }()
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("Content-Type", file.ContentType)
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": file.Filename}))
	_, _ = io.Copy(w, reader)
}

func (e fileEndpoints) delete(w http.ResponseWriter, r *http.Request) {
	principal, ok := e.principal(r)
	id, err := uuid.Parse(r.PathValue("fileId"))
	if !ok || err != nil || id == uuid.Nil {
		writeFileError(w, r, filestore.ErrInvalid)
		return
	}
	if err := e.service.Delete(r.Context(), principal, id); err != nil {
		writeFileError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (e fileEndpoints) token(w http.ResponseWriter, r *http.Request) {
	principal, ok := e.principal(r)
	id, err := uuid.Parse(r.PathValue("fileId"))
	var input struct {
		Variant filestore.Variant `json:"variant"`
	}
	if !ok || err != nil || id == uuid.Nil || decodeStrictJSON(r, &input) != nil {
		writeFileError(w, r, filestore.ErrInvalid)
		return
	}
	token, err := e.service.MintDownloadToken(r.Context(), principal, id, input.Variant)
	if err != nil {
		writeFileError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, struct {
		Token string `json:"token"`
	}{token})
}

type fileResponse struct {
	ID          string `json:"id"`
	Field       string `json:"field"`
	Filename    string `json:"filename"`
	ContentType string `json:"contentType"`
	Size        int64  `json:"size"`
	Thumbnail   bool   `json:"thumbnail"`
}

func publicFile(file filestore.File) fileResponse {
	return fileResponse{ID: file.ID.String(), Field: file.Field, Filename: file.Filename, ContentType: file.ContentType, Size: file.Size, Thumbnail: file.ThumbnailKey != ""}
}

func writeFileError(w http.ResponseWriter, r *http.Request, err error) {
	response := APIError{Status: http.StatusInternalServerError, Code: "file_error", Message: "File operation failed"}
	switch {
	case errors.Is(err, filestore.ErrInvalid):
		response = APIError{Status: 400, Code: "invalid_file", Message: "Invalid file request"}
	case errors.Is(err, filestore.ErrTooLarge):
		response = APIError{Status: 413, Code: "file_too_large", Message: "File is too large"}
	case errors.Is(err, filestore.ErrTooMany):
		response = APIError{Status: 409, Code: "file_limit_reached", Message: "File field limit reached"}
	case errors.Is(err, filestore.ErrRequired):
		response = APIError{Status: 409, Code: "file_required", Message: "At least one file is required"}
	case errors.Is(err, filestore.ErrDenied):
		response = APIError{Status: 403, Code: "file_access_denied", Message: "File access denied"}
	case errors.Is(err, filestore.ErrNotFound):
		response = APIError{Status: 404, Code: "not_found", Message: "File not found"}
	}
	writeAPIError(w, r, response)
}
