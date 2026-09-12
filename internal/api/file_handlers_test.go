package api

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/yakisoba0728/PgBase/internal/filestore"
	"github.com/yakisoba0728/PgBase/internal/identity"
	"github.com/yakisoba0728/PgBase/internal/userauth"
)

type transformedDownloadFiles struct {
	FileService
	file filestore.File
	body string
}

func (files *transformedDownloadFiles) OpenPublic(context.Context, uuid.UUID, filestore.Variant) (filestore.File, io.ReadCloser, error) {
	return files.file, io.NopCloser(strings.NewReader(files.body)), nil
}

func (files *transformedDownloadFiles) Open(context.Context, identity.Subject, uuid.UUID, filestore.Variant, string) (filestore.File, io.ReadCloser, error) {
	return files.file, io.NopCloser(strings.NewReader(files.body)), nil
}

func TestTransformedDownloadsAdvertiseRenderedJPEGContentType(t *testing.T) {
	fileID := uuid.New()
	for _, original := range []string{"image/gif", "image/webp"} {
		for _, variant := range []filestore.Variant{filestore.Thumbnail, filestore.Variant("320x240")} {
			for _, audience := range []string{"public", "user", "admin"} {
				t.Run(original+"/"+string(variant)+"/"+audience, func(t *testing.T) {
					files := &transformedDownloadFiles{file: filestore.File{ID: fileID, Filename: "rendered.jpg", ContentType: original}, body: "jpeg-bytes"}
					request := httptest.NewRequest(http.MethodGet, "/files/"+fileID.String()+"?variant="+string(variant), nil)
					request.SetPathValue("fileId", fileID.String())
					response := httptest.NewRecorder()
					endpoint := fileEndpoints{service: files}
					switch audience {
					case "public":
						endpoint.publicDownload(response, request)
					case "user":
						request.URL.RawQuery += "&token=download-token"
						request = request.WithContext(context.WithValue(request.Context(), userPrincipalKey{}, authenticatedUser{principal: userauth.Principal{User: userauth.User{ID: uuid.New()}}}))
						endpoint.download(response, request)
					case "admin":
						request.URL.RawQuery += "&token=download-token"
						request = request.WithContext(context.WithValue(request.Context(), adminPrincipalKey{}, AdminPrincipal{Administrator: validSession().Administrator}))
						endpoint.administrator = true
						endpoint.download(response, request)
					}
					if response.Code != http.StatusOK || response.Header().Get("Content-Type") != "image/jpeg" || response.Body.String() != "jpeg-bytes" {
						t.Fatalf("status=%d content-type=%q body=%q", response.Code, response.Header().Get("Content-Type"), response.Body.String())
					}
				})
			}
		}
	}
}

func TestOriginalDownloadKeepsStoredContentType(t *testing.T) {
	fileID := uuid.New()
	files := &transformedDownloadFiles{file: filestore.File{ID: fileID, Filename: "image.webp", ContentType: "image/webp"}, body: "webp-bytes"}
	request := httptest.NewRequest(http.MethodGet, "/files/"+fileID.String(), nil)
	request.SetPathValue("fileId", fileID.String())
	response := httptest.NewRecorder()
	(fileEndpoints{service: files}).publicDownload(response, request)
	if response.Code != http.StatusOK || response.Header().Get("Content-Type") != "image/webp" {
		t.Fatalf("status=%d content-type=%q", response.Code, response.Header().Get("Content-Type"))
	}
}
