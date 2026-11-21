package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/yakisoba0728/PgBase/internal/filestore"
	"github.com/yakisoba0728/PgBase/internal/identity"
	"github.com/yakisoba0728/PgBase/internal/query"
	"github.com/yakisoba0728/PgBase/internal/recordaccess"
	"github.com/yakisoba0728/PgBase/internal/recordops"
)

// admissionAccess answers the anonymous opt-in per collection name; a name it
// does not know is a missing collection.
type admissionAccess struct {
	enabled map[string]bool
	lookups int
}

func (a *admissionAccess) Get(_ context.Context, name string) (recordaccess.Policy, error) {
	a.lookups++
	enabled, ok := a.enabled[name]
	if !ok {
		return recordaccess.Policy{}, recordaccess.ErrNotFound
	}
	return recordaccess.Policy{Collection: name, Enabled: enabled}, nil
}

func (a *admissionAccess) Set(context.Context, string, bool) (recordaccess.Policy, error) {
	return recordaccess.Policy{}, errors.New("unused")
}

// admissionRecords stands in for recordops. It reports what the authoritative
// check would have returned for an anonymous caller, so the preflight result
// can be compared with the response the handler produced before it existed.
type admissionRecords struct {
	RecordService
	access  *admissionAccess
	creates int
	updates int
	batches int
}

func (s *admissionRecords) admission(name string) error {
	enabled, ok := s.access.enabled[name]
	if !ok {
		return query.ErrCollectionNotFound
	}
	if !enabled {
		return recordaccess.ErrDenied
	}
	return nil
}

func (s *admissionRecords) Create(_ context.Context, request recordops.CreateRequest) (recordops.MutationResult, error) {
	s.creates++
	if err := s.admission(request.Collection); err != nil {
		return recordops.MutationResult{}, err
	}
	return recordops.MutationResult{Record: &recordops.Record{Data: map[string]any{"id": "created"}, ETag: `"1"`}}, nil
}

func (s *admissionRecords) Update(_ context.Context, request recordops.UpdateRequest) (recordops.MutationResult, error) {
	s.updates++
	if err := s.admission(request.Collection); err != nil {
		return recordops.MutationResult{}, err
	}
	return recordops.MutationResult{Record: &recordops.Record{Data: map[string]any{"id": request.PublicID}, ETag: `"2"`}}, nil
}

func (s *admissionRecords) Batch(_ context.Context, request recordops.BatchRequest) ([]recordops.MutationResult, error) {
	s.batches++
	for _, operation := range request.Operations {
		name := ""
		switch {
		case operation.Create != nil:
			name = operation.Create.Collection
		case operation.Update != nil:
			name = operation.Update.Collection
		}
		if err := s.admission(name); err != nil {
			return nil, err
		}
	}
	return nil, errors.New("unused")
}

type admissionFiles struct {
	FileService
	stages int
}

func (f *admissionFiles) Stage(_ context.Context, _ identity.Subject, upload filestore.Upload) (filestore.StagedFile, error) {
	f.stages++
	if _, err := io.Copy(io.Discard, upload.Body); err != nil {
		return filestore.StagedFile{}, err
	}
	return filestore.StagedFile{}, nil
}

func (f *admissionFiles) Cleanup(context.Context, []filestore.StagedFile) error { return nil }

// CollectionID resolves the collections the fixture knows, as the file
// service does before the record service runs.
func (f *admissionFiles) CollectionID(_ context.Context, name string) (uuid.UUID, error) {
	if name == "open" || name == "closed" {
		return uuid.NewSHA1(uuid.NameSpaceOID, []byte(name)), nil
	}
	return uuid.Nil, filestore.ErrNotFound
}

func (f *admissionFiles) IsFileField(context.Context, uuid.UUID, string) (bool, error) {
	return false, nil
}

// countingBody records how many request body bytes a handler consumed.
type countingBody struct {
	reader io.Reader
	read   int
}

func (b *countingBody) Read(p []byte) (int, error) {
	n, err := b.reader.Read(p)
	b.read += n
	return n, err
}

func (b *countingBody) Close() error { return nil }

func newAdmissionFixture() (*admissionAccess, *admissionRecords, *admissionFiles) {
	access := &admissionAccess{enabled: map[string]bool{"open": true, "closed": false}}
	return access, &admissionRecords{access: access}, &admissionFiles{}
}

func admissionRequest(method, path, collection, id string, principal identity.Subject, body io.Reader) *http.Request {
	request := httptest.NewRequest(method, path, body)
	request.Header.Set("Content-Type", "application/json")
	request.SetPathValue("collection", collection)
	if id != "" {
		request.SetPathValue("id", id)
	}
	return request.WithContext(context.WithValue(request.Context(), publicRecordPrincipalKey{}, principal))
}

func admissionError(t *testing.T, response *httptest.ResponseRecorder) (int, string) {
	t.Helper()
	var envelope struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	_ = json.Unmarshal(response.Body.Bytes(), &envelope)
	return response.Code, envelope.Error.Code
}

func largeRecordBody() []byte {
	return []byte(`{"title":"` + strings.Repeat("x", 4<<20) + `"}`)
}

func TestAnonymousRecordWritesRejectWithoutReadingTheBody(t *testing.T) {
	anonymous := identity.Subject{Kind: identity.Anonymous}
	for _, test := range []struct {
		name, method, id string
		handler          func(recordEndpoints) http.HandlerFunc
	}{
		{name: "create", method: http.MethodPost, handler: func(e recordEndpoints) http.HandlerFunc { return e.create }},
		{name: "update", method: http.MethodPatch, id: "record-1", handler: func(e recordEndpoints) http.HandlerFunc { return e.update }},
	} {
		for _, collection := range []string{"closed", "missing"} {
			t.Run(test.name+"/"+collection, func(t *testing.T) {
				// The response the record service produces without the preflight.
				_, baselineRecords, _ := newAdmissionFixture()
				baseline := httptest.NewRecorder()
				test.handler(recordEndpoints{service: baselineRecords})(baseline, admissionRequest(test.method, "/", collection, test.id, anonymous, bytes.NewReader([]byte(`{"title":"small"}`))))
				wantStatus, wantCode := admissionError(t, baseline)

				access, records, _ := newAdmissionFixture()
				body := &countingBody{reader: bytes.NewReader(largeRecordBody())}
				response := httptest.NewRecorder()
				test.handler(recordEndpoints{service: records, access: access})(response, admissionRequest(test.method, "/", collection, test.id, anonymous, body))
				status, code := admissionError(t, response)
				if status != wantStatus || code != wantCode {
					t.Fatalf("preflight response = %d %q, want the record service's %d %q", status, code, wantStatus, wantCode)
				}
				if body.read != 0 || records.creates+records.updates != 0 {
					t.Fatalf("denied anonymous write read %d body bytes and made %d service calls", body.read, records.creates+records.updates)
				}
			})
		}
		t.Run(test.name+"/open", func(t *testing.T) {
			access, records, _ := newAdmissionFixture()
			payload := largeRecordBody()
			body := &countingBody{reader: bytes.NewReader(payload)}
			response := httptest.NewRecorder()
			test.handler(recordEndpoints{service: records, access: access})(response, admissionRequest(test.method, "/", "open", test.id, anonymous, body))
			if response.Code >= 300 || body.read < len(payload) || records.creates+records.updates != 1 {
				t.Fatalf("admitted anonymous write status=%d read=%d calls=%d body=%s", response.Code, body.read, records.creates+records.updates, response.Body.String())
			}
		})
		// Authenticated principals keep their established path: no preflight
		// lookup, and the service still owns every admission decision.
		for _, principal := range []identity.Subject{{Kind: identity.Service, ID: uuid.New()}, {Kind: identity.User, ID: uuid.New()}} {
			t.Run(test.name+"/"+string(principal.Kind), func(t *testing.T) {
				access, records, _ := newAdmissionFixture()
				body := &countingBody{reader: bytes.NewReader([]byte(`{"title":"small"}`))}
				response := httptest.NewRecorder()
				test.handler(recordEndpoints{service: records, access: access})(response, admissionRequest(test.method, "/", "closed", test.id, principal, body))
				if access.lookups != 0 || body.read == 0 || records.creates+records.updates != 1 {
					t.Fatalf("%s write lookups=%d read=%d calls=%d", principal.Kind, access.lookups, body.read, records.creates+records.updates)
				}
			})
		}
	}
}

func multipartBatchBody(t *testing.T, requests string, filesBefore, filesAfter int) (*bytes.Buffer, string) {
	t.Helper()
	return multipartBatchBodySized(t, requests, filesBefore, filesAfter, 256<<10)
}

func multipartBatchBodySized(t *testing.T, requests string, filesBefore, filesAfter, fileBytes int) (*bytes.Buffer, string) {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	addFile := func(index int) {
		part, err := writer.CreateFormFile("file"+strconv.Itoa(index), "upload.bin")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := part.Write(bytes.Repeat([]byte{'z'}, fileBytes)); err != nil {
			t.Fatal(err)
		}
	}
	file := 0
	for ; file < filesBefore; file++ {
		addFile(file)
	}
	part, err := writer.CreateFormField("requests")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write([]byte(requests)); err != nil {
		t.Fatal(err)
	}
	for ; file < filesBefore+filesAfter; file++ {
		addFile(file)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return &body, writer.FormDataContentType()
}

// tempDirProbe points the process temporary directory at an empty directory
// and reports the largest number of entries it held whenever sample ran.
type tempDirProbe struct {
	t   *testing.T
	dir string
	max int
}

func newTempDirProbe(t *testing.T) *tempDirProbe {
	t.Helper()
	dir := t.TempDir()
	for _, name := range []string{"TMPDIR", "TMP", "TEMP"} {
		t.Setenv(name, dir)
	}
	if os.TempDir() != dir {
		t.Fatalf("temporary directory override not honored: %s", os.TempDir())
	}
	return &tempDirProbe{t: t, dir: dir}
}

func (p *tempDirProbe) sample() {
	entries, err := os.ReadDir(p.dir)
	if err != nil {
		p.t.Fatal(err)
	}
	p.max = max(p.max, len(entries))
}

func (p *tempDirProbe) left() int {
	entries, err := os.ReadDir(p.dir)
	if err != nil {
		p.t.Fatal(err)
	}
	return len(entries)
}

// probedBody samples the temporary directory on every body read, so a file
// held open while later parts are read is counted.
type probedBody struct {
	reader io.Reader
	probe  *tempDirProbe
}

func (b *probedBody) Read(p []byte) (int, error) {
	b.probe.sample()
	return b.reader.Read(p)
}

func (b *probedBody) Close() error { return nil }

// probedFiles samples the temporary directory while each part is staged.
type probedFiles struct {
	*admissionFiles
	probe *tempDirProbe
}

func (f probedFiles) Stage(ctx context.Context, principal identity.Subject, upload filestore.Upload) (filestore.StagedFile, error) {
	f.probe.sample()
	return f.admissionFiles.Stage(ctx, principal, upload)
}

func TestAnonymousMultipartBatchStagesNothingBeforeAdmission(t *testing.T) {
	anonymous := identity.Subject{Kind: identity.Anonymous}
	mixed := `{"operations":[{"operation":"create","collection":"open","input":{"title":"a"}},{"operation":"update","collection":"%s","id":"r1","input":{"title":"b"}}]}`
	for _, denied := range []string{"closed", "missing"} {
		for _, layout := range []struct {
			name          string
			before, after int
		}{{"files-first", 2, 0}, {"files-last", 0, 2}, {"files-around", 1, 1}} {
			t.Run(denied+"/"+layout.name, func(t *testing.T) {
				requests := strings.Replace(mixed, "%s", denied, 1)
				// What recordops reports for the same batch without the preflight.
				_, baselineRecords, baselineFiles := newAdmissionFixture()
				body, contentType := multipartBatchBody(t, requests, 0, 0)
				request := admissionRequest(http.MethodPost, "/api/v1/public/collections/batch", "", "", anonymous, body)
				request.Header.Set("Content-Type", contentType)
				baseline := httptest.NewRecorder()
				recordEndpoints{service: baselineRecords, files: baselineFiles}.batch(baseline, request)
				wantStatus, wantCode := admissionError(t, baseline)
				// A file part ahead of the requests part is refused outright.
				if layout.before > 0 {
					wantStatus, wantCode = http.StatusBadRequest, "invalid_request"
				}

				probe := newTempDirProbe(t)
				access, records, files := newAdmissionFixture()
				body, contentType = multipartBatchBody(t, requests, layout.before, layout.after)
				request = admissionRequest(http.MethodPost, "/api/v1/public/collections/batch", "", "", anonymous, body)
				request.Header.Set("Content-Type", contentType)
				request.Body = &probedBody{reader: body, probe: probe}
				response := httptest.NewRecorder()
				recordEndpoints{service: records, files: probedFiles{files, probe}, access: access}.batch(response, request)
				status, code := admissionError(t, response)
				if status != wantStatus || code != wantCode {
					t.Fatalf("batch preflight = %d %q, want %d %q", status, code, wantStatus, wantCode)
				}
				if files.stages != 0 || records.batches != 0 || probe.max != 0 || probe.left() != 0 {
					t.Fatalf("denied anonymous batch staged %d files, ran %d batches, spooled up to %d temporary files, left %d", files.stages, records.batches, probe.max, probe.left())
				}
			})
		}
	}

	// A batch naming no admissible target cannot stage first and fail later.
	for _, requests := range []string{`{"requests":[{"method":"GET","url":"/api/v1/collections/open/records"}]}`, `{"operations":[]}`} {
		access, records, files := newAdmissionFixture()
		body, contentType := multipartBatchBody(t, requests, 0, 2)
		request := admissionRequest(http.MethodPost, "/api/v1/public/collections/batch", "", "", anonymous, body)
		request.Header.Set("Content-Type", contentType)
		response := httptest.NewRecorder()
		recordEndpoints{service: records, files: files, access: access}.batch(response, request)
		if status, code := admissionError(t, response); status != http.StatusBadRequest || code != "invalid_request" || files.stages != 0 {
			t.Fatalf("%s: status=%d code=%q stages=%d", requests, status, code, files.stages)
		}
	}

	// An admitted anonymous batch stages every file that follows the requests
	// part, after a single preflight.
	access, _, files := newAdmissionFixture()
	body, contentType := multipartBatchBody(t, `{"operations":[{"operation":"create","collection":"open","input":{"title":"a"}}]}`, 0, 3)
	request := admissionRequest(http.MethodPost, "/api/v1/public/collections/batch", "", "", anonymous, body)
	request.Header.Set("Content-Type", contentType)
	_, staged, err := recordEndpoints{files: files, access: access}.decodeBatch(request, anonymous)
	if err != nil || len(staged) != 3 || files.stages != 3 || access.lookups != 1 {
		t.Fatalf("admitted anonymous batch err=%v staged=%d stages=%d lookups=%d", err, len(staged), files.stages, access.lookups)
	}

	// Other principals may still send files first; they stage as they arrive
	// and never consult the preflight.
	for _, principal := range []identity.Subject{{Kind: identity.Service, ID: uuid.New()}, {Kind: identity.User, ID: uuid.New()}} {
		access, _, files = newAdmissionFixture()
		body, contentType = multipartBatchBody(t, `{"operations":[{"operation":"create","collection":"closed","input":{"title":"a"}}]}`, 2, 1)
		request = admissionRequest(http.MethodPost, "/api/v1/user/collections/batch", "", "", principal, body)
		request.Header.Set("Content-Type", contentType)
		_, staged, err = recordEndpoints{files: files, access: access}.decodeBatch(request, principal)
		if err != nil || len(staged) != 3 || files.stages != 3 || access.lookups != 0 {
			t.Fatalf("%s batch err=%v staged=%d stages=%d lookups=%d", principal.Kind, err, len(staged), files.stages, access.lookups)
		}
	}
}

// TestMultipartBatchKeepsOneTemporaryFileOpen sends thousands of one-byte file
// parts and checks that at most one spooled temporary file exists at any point
// while the body is read or a part is staged, whoever the caller is and
// whichever side of the requests part the files are on.
func TestMultipartBatchKeepsOneTemporaryFileOpen(t *testing.T) {
	const parts = 3000
	requests := `{"operations":[{"operation":"create","collection":"open","input":{"title":"a"}}]}`
	for _, test := range []struct {
		name          string
		principal     identity.Subject
		before, after int
		wantStages    int
		wantErr       bool
	}{
		{name: "anonymous/files-first", principal: identity.Subject{Kind: identity.Anonymous}, before: parts, wantErr: true},
		{name: "anonymous/files-last", principal: identity.Subject{Kind: identity.Anonymous}, after: parts, wantStages: parts},
		{name: "user/files-first", principal: identity.Subject{Kind: identity.User, ID: uuid.New()}, before: parts, wantStages: parts},
	} {
		t.Run(test.name, func(t *testing.T) {
			probe := newTempDirProbe(t)
			access, _, files := newAdmissionFixture()
			body, contentType := multipartBatchBodySized(t, requests, test.before, test.after, 1)
			request := admissionRequest(http.MethodPost, "/api/v1/public/collections/batch", "", "", test.principal, nil)
			request.Header.Set("Content-Type", contentType)
			request.Body = &probedBody{reader: body, probe: probe}
			_, staged, err := recordEndpoints{files: probedFiles{files, probe}, access: access}.decodeBatch(request, test.principal)
			if (err != nil) != test.wantErr || len(staged) != test.wantStages || files.stages != test.wantStages {
				t.Fatalf("err=%v staged=%d stages=%d", err, len(staged), files.stages)
			}
			wantMax := 1
			if test.wantErr {
				wantMax = 0
			}
			if probe.max != wantMax || probe.left() != 0 {
				t.Fatalf("temporary files: at most %d at once (want %d), %d left", probe.max, wantMax, probe.left())
			}
		})
	}
}

// TestBatchFileServiceErrorsUseFileResponses checks that a file-service
// refusal raised while a batch attaches its files is answered as the file
// endpoints answer it, not as an internal error.
func TestBatchFileServiceErrorsUseFileResponses(t *testing.T) {
	for _, test := range []struct {
		err    error
		status int
		code   string
	}{
		{fmt.Errorf("commit: %w", filestore.ErrDenied), http.StatusForbidden, "file_access_denied"},
		{filestore.ErrInvalid, http.StatusBadRequest, "invalid_file"},
		{filestore.ErrTooLarge, http.StatusRequestEntityTooLarge, "file_too_large"},
		{filestore.ErrTooMany, http.StatusConflict, "file_limit_reached"},
		{filestore.ErrRequired, http.StatusConflict, "file_required"},
		{filestore.ErrNotFound, http.StatusNotFound, "not_found"},
		{filestore.ErrImageBusy, http.StatusServiceUnavailable, "file_busy"},
		{recordaccess.ErrDenied, http.StatusForbidden, "access_denied"},
		{errors.New("unexpected"), http.StatusInternalServerError, "internal_error"},
	} {
		response := httptest.NewRecorder()
		writeBatchError(response, httptest.NewRequest(http.MethodPost, "/api/v1/public/collections/batch", nil), test.err)
		if status, code := admissionError(t, response); status != test.status || code != test.code {
			t.Errorf("%v: %d %q, want %d %q", test.err, status, code, test.status, test.code)
		}
	}
}
