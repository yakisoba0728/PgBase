package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
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
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	addFile := func(index int) {
		part, err := writer.CreateFormFile("file"+string(rune('a'+index)), "upload.bin")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := part.Write(bytes.Repeat([]byte{'z'}, 256<<10)); err != nil {
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

				access, records, files := newAdmissionFixture()
				body, contentType = multipartBatchBody(t, requests, layout.before, layout.after)
				request = admissionRequest(http.MethodPost, "/api/v1/public/collections/batch", "", "", anonymous, body)
				request.Header.Set("Content-Type", contentType)
				response := httptest.NewRecorder()
				recordEndpoints{service: records, files: files, access: access}.batch(response, request)
				status, code := admissionError(t, response)
				if status != wantStatus || code != wantCode {
					t.Fatalf("batch preflight = %d %q, want the record service's %d %q", status, code, wantStatus, wantCode)
				}
				if files.stages != 0 || records.batches != 0 {
					t.Fatalf("denied anonymous batch staged %d files and ran %d batches", files.stages, records.batches)
				}
			})
		}
	}

	// A batch naming no admissible target cannot stage first and fail later.
	for _, requests := range []string{`{"requests":[{"method":"GET","url":"/api/v1/collections/open/records"}]}`, `{"operations":[]}`} {
		access, records, files := newAdmissionFixture()
		body, contentType := multipartBatchBody(t, requests, 1, 1)
		request := admissionRequest(http.MethodPost, "/api/v1/public/collections/batch", "", "", anonymous, body)
		request.Header.Set("Content-Type", contentType)
		response := httptest.NewRecorder()
		recordEndpoints{service: records, files: files, access: access}.batch(response, request)
		if status, code := admissionError(t, response); status != http.StatusBadRequest || code != "invalid_request" || files.stages != 0 {
			t.Fatalf("%s: status=%d code=%q stages=%d", requests, status, code, files.stages)
		}
	}

	// An admitted anonymous batch stages every file, whichever side of the
	// requests part it arrived on.
	access, _, files := newAdmissionFixture()
	body, contentType := multipartBatchBody(t, `{"operations":[{"operation":"create","collection":"open","input":{"title":"a"}}]}`, 2, 1)
	request := admissionRequest(http.MethodPost, "/api/v1/public/collections/batch", "", "", anonymous, body)
	request.Header.Set("Content-Type", contentType)
	_, staged, err := recordEndpoints{files: files, access: access}.decodeBatch(request, anonymous)
	if err != nil || len(staged) != 3 || files.stages != 3 || access.lookups != 1 {
		t.Fatalf("admitted anonymous batch err=%v staged=%d stages=%d lookups=%d", err, len(staged), files.stages, access.lookups)
	}

	// Other principals stage as they arrive and never consult the preflight.
	service := identity.Subject{Kind: identity.Service, ID: uuid.New()}
	access, _, files = newAdmissionFixture()
	body, contentType = multipartBatchBody(t, `{"operations":[{"operation":"create","collection":"closed","input":{"title":"a"}}]}`, 1, 1)
	request = admissionRequest(http.MethodPost, "/api/v1/service/collections/batch", "", "", service, body)
	request.Header.Set("Content-Type", contentType)
	_, staged, err = recordEndpoints{files: files, access: access}.decodeBatch(request, service)
	if err != nil || len(staged) != 2 || files.stages != 2 || access.lookups != 0 {
		t.Fatalf("service batch err=%v staged=%d stages=%d lookups=%d", err, len(staged), files.stages, access.lookups)
	}
}
