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

func (*transformedDownloadFiles) SupportsRanges() bool { return true }

type legacyPublicDownloadFiles struct {
	FileService
	file filestore.File
	body string
}

func (files *legacyPublicDownloadFiles) OpenPublic(context.Context, uuid.UUID, filestore.Variant) (filestore.File, io.ReadCloser, error) {
	return files.file, io.NopCloser(strings.NewReader(files.body)), nil
}

func TestCustomStoreWithoutRangeCapabilityFallsBackToFullResponse(t *testing.T) {
	fileID := uuid.New()
	files := &legacyPublicDownloadFiles{file: filestore.File{ID: fileID, Filename: "legacy.bin", ContentType: "application/octet-stream", Size: 10}, body: "0123456789"}
	server := httptest.NewServer(mustRouter(t, Dependencies{Files: files, MaximumFileUploadBytes: 1024}))
	t.Cleanup(server.Close)
	request, err := http.NewRequest(http.MethodGet, server.URL+"/api/files/"+fileID.String(), nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Range", "bytes=2-5")
	response, err := server.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK || string(body) != "0123456789" || response.Header.Get("Accept-Ranges") != "" || response.Header.Get("Content-Range") != "" {
		t.Fatalf("status=%d body=%q accept-ranges=%q content-range=%q", response.StatusCode, body, response.Header.Get("Accept-Ranges"), response.Header.Get("Content-Range"))
	}
}

func TestManagedDownloadSupportsSingleByteRanges(t *testing.T) {
	fileID := uuid.New()
	files := &transformedDownloadFiles{file: filestore.File{ID: fileID, Filename: "sample.bin", ContentType: "application/octet-stream", Size: 10}, body: "0123456789"}
	for _, test := range []struct {
		name, value, body, contentRange string
		status                          int
	}{
		{"closed", "bytes=2-5", "2345", "bytes 2-5/10", http.StatusPartialContent},
		{"open-ended", "bytes=7-", "789", "bytes 7-9/10", http.StatusPartialContent},
		{"suffix", "bytes=-3", "789", "bytes 7-9/10", http.StatusPartialContent},
		{"unsatisfiable", "bytes=20-", "", "bytes */10", http.StatusRequestedRangeNotSatisfiable},
		{"malformed", "bytes=wrong", "", "bytes */10", http.StatusRequestedRangeNotSatisfiable},
		{"signed-start", "bytes=+1-3", "", "bytes */10", http.StatusRequestedRangeNotSatisfiable},
		{"signed-end", "bytes=1-+3", "", "bytes */10", http.StatusRequestedRangeNotSatisfiable},
		{"signed-suffix", "bytes=-+2", "", "bytes */10", http.StatusRequestedRangeNotSatisfiable},
		{"multiple", "bytes=0-1,4-5", "", "bytes */10", http.StatusRequestedRangeNotSatisfiable},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, "/files/"+fileID.String(), nil)
			request.SetPathValue("fileId", fileID.String())
			request.Header.Set("Range", test.value)
			response := httptest.NewRecorder()
			(fileEndpoints{service: files}).publicDownload(response, request)
			if response.Code != test.status || response.Body.String() != test.body || response.Header().Get("Content-Range") != test.contentRange {
				t.Fatalf("status=%d body=%q content-range=%q", response.Code, response.Body.String(), response.Header().Get("Content-Range"))
			}
			if response.Header().Get("Accept-Ranges") != "bytes" {
				t.Fatalf("accept-ranges=%q", response.Header().Get("Accept-Ranges"))
			}
		})
	}
}

func TestPrivateRangeErrorsRemainNoStore(t *testing.T) {
	response := httptest.NewRecorder()
	serveFileRange(response, httptest.NewRequest(http.MethodGet, "/file", nil), filestore.File{}, filestore.Original, filestore.RangeRead{Size: 10}, filestore.ErrRangeNotSatisfiable, true)
	if response.Code != http.StatusRequestedRangeNotSatisfiable || response.Header().Get("Cache-Control") != "private, no-store" {
		t.Fatalf("status=%d cache-control=%q", response.Code, response.Header().Get("Cache-Control"))
	}
}

func TestManagedDownloadIgnoresRangeWhenIfRangeIsPresent(t *testing.T) {
	fileID := uuid.New()
	files := &transformedDownloadFiles{file: filestore.File{ID: fileID, Filename: "sample.bin", ContentType: "application/octet-stream", Size: 10}, body: "0123456789"}
	request := httptest.NewRequest(http.MethodGet, "/files/"+fileID.String(), nil)
	request.SetPathValue("fileId", fileID.String())
	request.Header.Set("Range", "bytes=2-5")
	request.Header.Set("If-Range", `"unsupported-validator"`)
	response := httptest.NewRecorder()
	(fileEndpoints{service: files}).publicDownload(response, request)
	if response.Code != http.StatusOK || response.Body.String() != "0123456789" || response.Header().Get("Content-Range") != "" {
		t.Fatalf("status=%d body=%q content-range=%q", response.Code, response.Body.String(), response.Header().Get("Content-Range"))
	}
}

func TestManagedDownloadHEADUsesGETRangeContractWithoutBody(t *testing.T) {
	fileID := uuid.New()
	files := &transformedDownloadFiles{file: filestore.File{ID: fileID, Filename: "sample.bin", ContentType: "application/octet-stream", Size: 10}, body: "0123456789"}
	server := httptest.NewServer(mustRouter(t, Dependencies{Files: files, MaximumFileUploadBytes: 1024}))
	t.Cleanup(server.Close)
	request, err := http.NewRequest(http.MethodHead, server.URL+"/api/files/"+fileID.String(), nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Range", "bytes=2-5")
	response, err := server.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusPartialContent || len(body) != 0 || response.Header.Get("Content-Range") != "bytes 2-5/10" || response.ContentLength != 4 {
		t.Fatalf("status=%d body=%q range=%q length=%d", response.StatusCode, body, response.Header.Get("Content-Range"), response.ContentLength)
	}
}

func (files *transformedDownloadFiles) OpenPublic(context.Context, uuid.UUID, filestore.Variant) (filestore.File, io.ReadCloser, error) {
	return files.file, io.NopCloser(strings.NewReader(files.body)), nil
}

func (files *transformedDownloadFiles) Open(context.Context, identity.Subject, uuid.UUID, filestore.Variant, string) (filestore.File, io.ReadCloser, error) {
	return files.file, io.NopCloser(strings.NewReader(files.body)), nil
}

func (files *transformedDownloadFiles) OpenPublicRange(_ context.Context, _ uuid.UUID, _ filestore.Variant, requested filestore.ByteRange) (filestore.File, filestore.RangeRead, error) {
	start, end, err := filestore.ResolveByteRange(requested, int64(len(files.body)))
	result := filestore.RangeRead{Size: int64(len(files.body))}
	if err != nil {
		return files.file, result, err
	}
	result.Start, result.End = start, end
	result.Body = io.NopCloser(strings.NewReader(files.body[start : end+1]))
	result.Prefix = []byte(files.body)
	return files.file, result, nil
}

func TestTransformedDownloadsAdvertiseRenderedJPEGContentType(t *testing.T) {
	fileID := uuid.New()
	for _, original := range []string{"image/gif", "image/webp"} {
		for _, variant := range []filestore.Variant{filestore.Thumbnail, filestore.Variant("320x240")} {
			for _, audience := range []string{"public", "user", "admin"} {
				t.Run(original+"/"+string(variant)+"/"+audience, func(t *testing.T) {
					jpeg := "\xff\xd8\xff\xe0\x00\x10JFIF\x00rendered"
					files := &transformedDownloadFiles{file: filestore.File{ID: fileID, Filename: "rendered.jpg", ContentType: original}, body: jpeg}
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
					if response.Code != http.StatusOK || response.Header().Get("Content-Type") != "image/jpeg" || response.Body.String() != jpeg {
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

func TestTransformedDownloadSniffsPNGForFullAndRangedResponses(t *testing.T) {
	fileID := uuid.New()
	png := "\x89PNG\r\n\x1a\ntransformed-pixels"
	for _, original := range []string{"image/gif", "image/webp"} {
		files := &transformedDownloadFiles{file: filestore.File{ID: fileID, Filename: "rendered.png", ContentType: original}, body: png}
		for _, rangeValue := range []string{"", "bytes=8-"} {
			request := httptest.NewRequest(http.MethodGet, "/files/"+fileID.String()+"?variant=thumbnail", nil)
			request.SetPathValue("fileId", fileID.String())
			request.Header.Set("Range", rangeValue)
			response := httptest.NewRecorder()
			(fileEndpoints{service: files}).publicDownload(response, request)
			if response.Header().Get("Content-Type") != "image/png" {
				t.Fatalf("original=%s range=%q content-type=%q", original, rangeValue, response.Header().Get("Content-Type"))
			}
		}
	}
}
