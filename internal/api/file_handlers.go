package api

import (
	"bufio"
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
	"github.com/yakisoba0728/PgBase/internal/filestore"
	"github.com/yakisoba0728/PgBase/internal/identity"
)

type FileService interface {
	CollectionID(context.Context, string) (uuid.UUID, error)
	IsFileField(context.Context, uuid.UUID, string) (bool, error)
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

// publicFileService is kept separate from FileService so applications that
// supply a test or extension file service do not accidentally expose bytes.
type publicFileService interface {
	OpenPublic(context.Context, uuid.UUID, filestore.Variant) (filestore.File, io.ReadCloser, error)
}

type rangedPublicFileService interface {
	OpenPublicRange(context.Context, uuid.UUID, filestore.Variant, filestore.ByteRange) (filestore.File, filestore.RangeRead, error)
}

type rangedFileService interface {
	OpenRange(context.Context, identity.Subject, uuid.UUID, filestore.Variant, string, filestore.ByteRange) (filestore.File, filestore.RangeRead, error)
}

type rangeCapability interface{ SupportsRanges() bool }

func supportsFileRanges(service any) bool {
	capability, ok := service.(rangeCapability)
	return ok && capability.SupportsRanges()
}

type fileRecordIDResolver interface {
	ResolveRecordID(context.Context, string, string) (uuid.UUID, uuid.UUID, error)
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
	if _, ok := dependencies.Files.(publicFileService); ok {
		routes = append(routes, route{method: "GET", pattern: "/api/files/{fileId}", access: accessPublic, response: passthrough(), deadline: deadlineDefault, handler: http.HandlerFunc(user.publicDownload)})
	}
	return routes
}

func (e fileEndpoints) publicDownload(w http.ResponseWriter, r *http.Request) {
	service, ok := e.service.(publicFileService)
	if !ok {
		writeFileError(w, r, filestore.ErrNotFound)
		return
	}
	id, err := uuid.Parse(r.PathValue("fileId"))
	variant := filestore.Variant(r.URL.Query().Get("variant"))
	if variant == "" {
		variant = filestore.Original
	}
	values := r.URL.Query()
	variants, variantOK := values["variant"]
	if err != nil || id == uuid.Nil || len(values) > 1 || (variantOK && len(variants) != 1) {
		writeFileError(w, r, filestore.ErrInvalid)
		return
	}
	if raw := r.Header.Get("Range"); raw != "" && r.Header.Get("If-Range") == "" {
		if ranged, ok := e.service.(rangedPublicFileService); ok && supportsFileRanges(e.service) {
			requested, parseErr := parseSingleByteRange(raw)
			if parseErr != nil {
				requested = filestore.ByteRange{Start: 0, End: -2}
			}
			file, result, openErr := ranged.OpenPublicRange(r.Context(), id, variant, requested)
			serveFileRange(w, r, file, variant, result, openErr, false)
			return
		}
	}
	file, reader, err := service.OpenPublic(r.Context(), id, variant)
	if err != nil {
		writeFileError(w, r, err)
		return
	}
	defer func() { _ = reader.Close() }()
	if _, ok := e.service.(rangedPublicFileService); ok && supportsFileRanges(e.service) {
		w.Header().Set("Accept-Ranges", "bytes")
	}
	w.Header().Set("Cache-Control", "no-store")
	reader = setDownloadContentType(w, reader, file, variant)
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": file.Filename}))
	_, _ = io.Copy(w, reader)
}

func downloadContentType(file filestore.File, variant filestore.Variant) string {
	if variant != filestore.Original && (file.ContentType == "image/gif" || file.ContentType == "image/webp") {
		return "image/jpeg"
	}
	return file.ContentType
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
	var collectionID, recordID uuid.UUID
	var err error
	if resolver, ok := e.service.(fileRecordIDResolver); ok {
		collectionID, recordID, err = resolver.ResolveRecordID(r.Context(), r.PathValue("collection"), r.PathValue("id"))
	} else {
		collectionID, err = e.service.CollectionID(r.Context(), r.PathValue("collection"))
		if err == nil {
			recordID, err = uuid.Parse(r.PathValue("id"))
		}
	}
	if err != nil {
		return identity.Subject{}, uuid.Nil, uuid.Nil, "", err
	}
	if recordID == uuid.Nil || r.PathValue("field") == "" {
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
	if raw := r.Header.Get("Range"); raw != "" && r.Header.Get("If-Range") == "" {
		if ranged, ok := e.service.(rangedFileService); ok && supportsFileRanges(e.service) {
			requested, parseErr := parseSingleByteRange(raw)
			if parseErr != nil {
				requested = filestore.ByteRange{Start: 0, End: -2}
			}
			file, result, openErr := ranged.OpenRange(r.Context(), principal, id, variant, values.Get("token"), requested)
			serveFileRange(w, r, file, variant, result, openErr, true)
			return
		}
	}
	file, reader, err := e.service.Open(r.Context(), principal, id, variant, values.Get("token"))
	if err != nil {
		writeFileError(w, r, err)
		return
	}
	defer func() { _ = reader.Close() }()
	if _, ok := e.service.(rangedFileService); ok && supportsFileRanges(e.service) {
		w.Header().Set("Accept-Ranges", "bytes")
	}
	w.Header().Set("Cache-Control", "private, no-store")
	reader = setDownloadContentType(w, reader, file, variant)
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": file.Filename}))
	_, _ = io.Copy(w, reader)
}

func parseSingleByteRange(raw string) (filestore.ByteRange, error) {
	if !strings.HasPrefix(raw, "bytes=") || strings.Contains(raw, ",") {
		return filestore.ByteRange{}, filestore.ErrRangeNotSatisfiable
	}
	parts := strings.Split(strings.TrimPrefix(raw, "bytes="), "-")
	if len(parts) != 2 || parts[0] == "" && parts[1] == "" {
		return filestore.ByteRange{}, filestore.ErrRangeNotSatisfiable
	}
	if parts[0] == "" {
		n, err := parseUnsignedRangeDecimal(parts[1])
		if err != nil || n <= 0 {
			return filestore.ByteRange{}, filestore.ErrRangeNotSatisfiable
		}
		return filestore.ByteRange{Start: -n, End: -1}, nil
	}
	start, err := parseUnsignedRangeDecimal(parts[0])
	if err != nil || start < 0 {
		return filestore.ByteRange{}, filestore.ErrRangeNotSatisfiable
	}
	end := int64(-1)
	if parts[1] != "" {
		end, err = parseUnsignedRangeDecimal(parts[1])
		if err != nil || end < start {
			return filestore.ByteRange{}, filestore.ErrRangeNotSatisfiable
		}
	}
	return filestore.ByteRange{Start: start, End: end}, nil
}

func parseUnsignedRangeDecimal(value string) (int64, error) {
	if value == "" {
		return 0, filestore.ErrRangeNotSatisfiable
	}
	for _, digit := range value {
		if digit < '0' || digit > '9' {
			return 0, filestore.ErrRangeNotSatisfiable
		}
	}
	return strconv.ParseInt(value, 10, 64)
}

func serveFileRange(w http.ResponseWriter, r *http.Request, file filestore.File, variant filestore.Variant, result filestore.RangeRead, err error, private bool) {
	w.Header().Set("Accept-Ranges", "bytes")
	cache := "no-store"
	if private {
		cache = "private, no-store"
	}
	w.Header().Set("Cache-Control", cache)
	if errors.Is(err, filestore.ErrRangeNotSatisfiable) {
		writeUnsatisfiableRange(w, result.Size)
		return
	}
	if err != nil {
		writeFileError(w, r, err)
		return
	}
	defer func() { _ = result.Body.Close() }()
	contentType := downloadContentType(file, variant)
	if variant != filestore.Original && len(result.Prefix) > 0 {
		contentType = http.DetectContentType(result.Prefix)
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": file.Filename}))
	w.Header().Set("Content-Range", "bytes "+strconv.FormatInt(result.Start, 10)+"-"+strconv.FormatInt(result.End, 10)+"/"+strconv.FormatInt(result.Size, 10))
	w.Header().Set("Content-Length", strconv.FormatInt(result.End-result.Start+1, 10))
	w.WriteHeader(http.StatusPartialContent)
	_, _ = io.Copy(w, result.Body)
}

func setDownloadContentType(w http.ResponseWriter, reader io.ReadCloser, file filestore.File, variant filestore.Variant) io.ReadCloser {
	if variant == filestore.Original {
		w.Header().Set("Content-Type", file.ContentType)
		return reader
	}
	buffered := bufio.NewReader(reader)
	prefix, _ := buffered.Peek(512)
	w.Header().Set("Content-Type", http.DetectContentType(prefix))
	return struct {
		io.Reader
		io.Closer
	}{Reader: buffered, Closer: reader}
}

func writeUnsatisfiableRange(w http.ResponseWriter, size int64) {
	w.Header().Set("Accept-Ranges", "bytes")
	w.Header().Set("Content-Range", "bytes */"+strconv.FormatInt(max(size, 0), 10))
	w.WriteHeader(http.StatusRequestedRangeNotSatisfiable)
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
	Protected   bool   `json:"protected"`
	PublicURL   string `json:"publicUrl,omitempty"`
}

func publicFile(file filestore.File) fileResponse {
	result := fileResponse{ID: file.ID.String(), Field: file.Field, Filename: file.Filename, ContentType: file.ContentType, Size: file.Size, Thumbnail: file.ThumbnailKey != "", Protected: !file.Public}
	if file.Public {
		result.PublicURL = "/api/files/" + file.ID.String()
	}
	return result
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
