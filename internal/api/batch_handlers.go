package api

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/yakisoba0728/PgBase/internal/filestore"
	"github.com/yakisoba0728/PgBase/internal/identity"
	"github.com/yakisoba0728/PgBase/internal/recordops"
	"github.com/yakisoba0728/PgBase/internal/responseprojection"
	"github.com/yakisoba0728/PgBase/internal/runtimesettings"
)

const maximumBatchRequestBytes = maximumRecordRequestBytes
const maximumMultipartBatchRequestBytes = runtimesettings.MaximumBatchBodyBytes
const batchStagedCleanupTimeout = time.Second

type batchRecordService interface {
	Batch(context.Context, recordops.BatchRequest) ([]recordops.MutationResult, error)
}
type batchSettings interface {
	Get() runtimesettings.Settings
}

type batchInput struct {
	Operations []batchOperationInput `json:"operations"`
	Requests   []batchHTTPRequest    `json:"requests"`
}

type batchHTTPRequest struct {
	Method  string                     `json:"method"`
	URL     string                     `json:"url"`
	Body    map[string]json.RawMessage `json:"body"`
	Headers map[string]string          `json:"headers"`
}
type namedStagedFiles map[string]filestore.StagedFile

func (s namedStagedFiles) files() []filestore.StagedFile {
	result := make([]filestore.StagedFile, 0, len(s))
	for _, file := range s {
		result = append(result, file)
	}
	return result
}

var batchRecordURL = regexp.MustCompile(`^/api/(v1|admin)/collections/([a-z][a-z0-9_]{0,47})/records(?:/([^/?#]+))?$`)

type batchOperationInput struct {
	Operation          string                     `json:"operation"`
	Collection         string                     `json:"collection"`
	ID                 string                     `json:"id"`
	Input              map[string]json.RawMessage `json:"input"`
	IdempotencyKey     string                     `json:"idempotencyKey"`
	IfMatch            string                     `json:"ifMatch"`
	MatchFilter        string                     `json:"matchFilter"`
	IdempotencyContext []byte                     `json:"-"`
	SidecarFields      []string                   `json:"-"`
}

// batch is shared by administrator and public-user routes. The router applies
// each route's normal principal middleware before entering this handler.
func (e recordEndpoints) batch(w http.ResponseWriter, r *http.Request) {
	settings := runtimesettings.Default().Batch
	if e.settings != nil {
		settings = e.settings.Get().Batch
		if !settings.Enabled {
			writeAPIError(w, r, APIError{Status: http.StatusServiceUnavailable, Code: "batch_disabled", Message: "Batch requests are disabled"})
			return
		}
		if settings.TimeoutMS > 0 {
			var cancel context.CancelFunc
			r = r.WithContext(func() context.Context {
				ctx, c := context.WithTimeout(r.Context(), time.Duration(settings.TimeoutMS)*time.Millisecond)
				cancel = c
				return ctx
			}())
			defer cancel()
		}
	}
	projection, ok := compileMutationProjection(w, r)
	if !ok {
		return
	}
	service, ok := e.service.(batchRecordService)
	if !ok || service == nil {
		writeRecordError(w, r, errors.New("batch record service is unavailable"))
		return
	}
	media, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
	limit := int64(maximumBatchRequestBytes)
	if media == "multipart/form-data" {
		limit = maximumMultipartBatchRequestBytes
	}
	if e.settings != nil {
		limit = settings.MaxBodyBytes
	}
	r.Body = http.MaxBytesReader(w, r.Body, limit)
	if r.ContentLength > limit {
		writeInvalidRecordRequest(w, r)
		return
	}
	principal, ok := recordPrincipal(w, r)
	if !ok {
		return
	}
	if media == "multipart/form-data" {
		if gated, ok := e.files.(interface {
			BeginStorageOperation(context.Context) (context.Context, func(), error)
		}); ok {
			ctx, release, err := gated.BeginStorageOperation(r.Context())
			if err != nil {
				writeRecordError(w, r, err)
				return
			}
			defer release()
			r = r.WithContext(ctx)
		}
	}
	input, staged, err := e.decodeBatch(r, principal)
	var admissionErr *batchAdmissionError
	if errors.As(err, &admissionErr) {
		e.cleanupStaged(r, staged)
		if admissionErr.malformed {
			writeInvalidRecordRequest(w, r)
		} else {
			writeRecordError(w, r, admissionErr.err)
		}
		return
	}
	maximumRequests := settings.MaxRequests
	if err != nil || (len(input.Operations) == 0) == (len(input.Requests) == 0) || len(input.Operations)+len(input.Requests) > maximumRequests {
		e.cleanupStaged(r, staged)
		writeInvalidRecordRequest(w, r)
		return
	}
	operations := make([]recordops.BatchOperation, 0, len(input.Operations))
	items := input.Operations
	if len(input.Requests) > 0 {
		items = make([]batchOperationInput, 0, len(input.Requests))
		for _, request := range input.Requests {
			item, valid := batchHTTPRequestOperation(request)
			if !valid {
				e.cleanupStaged(r, staged)
				writeInvalidRecordRequest(w, r)
				return
			}
			items = append(items, item)
		}
	}
	attachments, err := extractBatchAttachments(r.Context(), e.files, items, staged)
	if err != nil {
		e.cleanupStaged(r, staged)
		writeInvalidRecordRequest(w, r)
		return
	}
	for _, item := range items {
		operation, ok := batchOperation(item)
		if !ok {
			e.cleanupStaged(r, staged)
			writeInvalidRecordRequest(w, r)
			return
		}
		operations = append(operations, operation)
	}
	var participants []recordops.BatchParticipant
	if len(attachments) > 0 {
		if e.files == nil {
			writeInvalidRecordRequest(w, r)
			return
		}
		participant := &fileBatchParticipant{service: e.files, principal: principal, attachments: attachments}
		participants = []recordops.BatchParticipant{participant}
	}
	attachmentBindings := newBatchAttachmentBindings(operations)
	identities := captureBatchIdentities(operations)
	event := &BatchRequestEvent{Actor: principal, RequestID: requestID(r.Context()), Operations: operations}
	finish, err := beginLifecycle(r.Context(), e.hooks, "record.batch", event)
	if err != nil {
		finish(err)
		e.cleanupStaged(r, staged)
		writeRecordError(w, r, err)
		return
	}
	if len(event.Operations) == 0 || len(event.Operations) > maximumRequests {
		err = errors.New("batch hook changed the request count beyond its limit")
		finish(err)
		e.cleanupStaged(r, staged)
		writeInvalidRecordRequest(w, r)
		return
	}
	if len(attachments) != 0 && !attachmentBindings.compatible(event.Operations) {
		err = errors.New("batch hook changed a multipart attachment binding")
		finish(err)
		e.cleanupStaged(r, staged)
		writeInvalidRecordRequest(w, r)
		return
	}
	if !identities.restore(event.Operations) {
		err = errors.New("batch hook changed an idempotency identity")
		finish(err)
		e.cleanupStaged(r, staged)
		writeInvalidRecordRequest(w, r)
		return
	}
	var statuses []int
	if len(input.Requests) > 0 {
		statuses = make([]int, len(event.Operations))
		for index, operation := range event.Operations {
			statuses[index] = http.StatusOK
			if operation.Create != nil {
				statuses[index] = http.StatusCreated
			} else if operation.Delete != nil {
				statuses[index] = http.StatusNoContent
			}
		}
	}
	var response []byte
	err = finishLifecycle(finish, func() error {
		results, err := service.Batch(r.Context(), recordops.BatchRequest{
			Principal: principal, RequestID: requestID(r.Context()), Operations: event.Operations, Participants: participants, RuleContext: ruleRequestContext(r),
			PrepareResponse: func(results []recordops.MutationResult) error {
				var err error
				response, err = encodeBatchResponse(results, statuses, projection, maximumBatchResponseBytes)
				return err
			},
		})
		event.Results = cloneMutationResults(results)
		return err
	})
	if err != nil {
		e.cleanupStaged(r, staged)
		if errors.Is(err, errResponseTooLarge) {
			writeAPIError(w, r, APIError{Status: http.StatusBadRequest, Code: "response_too_large", Message: "Batch response exceeds the limit"})
			return
		}
		writeBatchError(w, r, err)
		return
	}
	if len(staged) > 0 {
		e.files.CompleteStaged(staged.files(), nil)
		e.cleanupStaged(r, staged)
	}
	writeJSONHeader(w, http.StatusOK)
	_, _ = w.Write(response)
}

// writeBatchError answers a failed batch transaction. The file participant
// attaches staged files inside that transaction, so its file-service
// refusals (an anonymous caller may not attach files, a field limit, a
// missing record) reach here as filestore sentinels; they get the status and
// code the file endpoints use instead of an internal error. Everything else
// is a record error.
func writeBatchError(w http.ResponseWriter, r *http.Request, err error) {
	for _, sentinel := range []error{filestore.ErrInvalid, filestore.ErrTooLarge, filestore.ErrTooMany, filestore.ErrRequired, filestore.ErrDenied, filestore.ErrNotFound, filestore.ErrImageBusy} {
		if errors.Is(err, sentinel) {
			writeFileError(w, r, err)
			return
		}
	}
	writeRecordError(w, r, err)
}

func encodeBatchResponse(results []recordops.MutationResult, statuses []int, projection responseprojection.Projection, maximumBytes int) ([]byte, error) {
	const suffix = "]}\n"
	var response bytes.Buffer
	response.WriteString(`{"results":[`)
	for index, result := range results {
		if index > 0 {
			response.WriteByte(',')
		}
		item := batchResult{EventID: result.EventID.String()}
		if len(statuses) > 0 {
			item.Status = statuses[index]
		}
		if result.Record != nil {
			if len(statuses) > 0 {
				item.Body, _ = projection.Apply(result.Record.Data).(map[string]any)
				item.Headers = map[string]string{"ETag": result.Record.ETag}
			} else {
				item.Record, _ = projection.Apply(result.Record.Data).(map[string]any)
			}
			item.ETag = result.Record.ETag
			item.Version = result.Record.Version
		}
		encoded, err := json.Marshal(item)
		if err != nil {
			return nil, err
		}
		if response.Len()+len(encoded)+len(suffix) > maximumBytes {
			return nil, errResponseTooLarge
		}
		response.Write(encoded)
	}
	response.WriteString(suffix)
	return response.Bytes(), nil
}

// cleanupStaged preserves request-independent staged-file hygiene while
// bounding a backend stall. Cleanup remains best effort so API results keep
// their established status and error contracts.
func (e recordEndpoints) cleanupStaged(r *http.Request, staged namedStagedFiles) {
	if len(staged) == 0 || e.files == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), batchStagedCleanupTimeout)
	defer cancel()
	_ = e.files.Cleanup(ctx, staged.files())
}

// batchAttachmentBindings freezes operation routing metadata when a multipart
// request contains uploaded files. Attachments are indexed before hooks run;
// accepting a reordered or retargeted operation would attach a staged file to
// the wrong committed record. Input remains mutable and is revalidated by the
// record service.
type batchAttachmentBindings []batchAttachmentBinding

type batchAttachmentBinding struct {
	kind               string
	collection         string
	id                 uuid.UUID
	publicID           string
	idempotencyKey     string
	idempotencyContext []byte
	ifMatch            recordops.EntityTagSet
	matchFilter        string
	sidecarFields      []string
}

func newBatchAttachmentBindings(operations []recordops.BatchOperation) batchAttachmentBindings {
	bindings := make(batchAttachmentBindings, len(operations))
	for index, operation := range operations {
		bindings[index] = batchAttachmentBindingFor(operation)
	}
	return bindings
}

func batchAttachmentBindingFor(operation recordops.BatchOperation) batchAttachmentBinding {
	if operation.Create != nil {
		return batchAttachmentBinding{kind: "create", collection: operation.Create.Collection, id: operation.Create.ID, idempotencyKey: operation.Create.IdempotencyKey, idempotencyContext: append([]byte(nil), operation.Create.IdempotencyContext...)}
	}
	if operation.Update != nil {
		return batchAttachmentBinding{kind: "update", collection: operation.Update.Collection, id: operation.Update.ID, publicID: operation.Update.PublicID, idempotencyContext: append([]byte(nil), operation.Update.SidecarContext...), ifMatch: operation.Update.IfMatch, sidecarFields: append([]string(nil), operation.Update.SidecarFields...)}
	}
	if operation.Upsert != nil {
		return batchAttachmentBinding{kind: "upsert", collection: operation.Upsert.Collection, id: operation.Upsert.ID, publicID: operation.Upsert.PublicID, idempotencyContext: append([]byte(nil), operation.Upsert.SidecarContext...), ifMatch: operation.Upsert.IfMatch, sidecarFields: append([]string(nil), operation.Upsert.SidecarFields...)}
	}
	if operation.Delete != nil {
		return batchAttachmentBinding{kind: "delete", collection: operation.Delete.Collection, id: operation.Delete.ID, publicID: operation.Delete.PublicID, ifMatch: operation.Delete.IfMatch, matchFilter: operation.Delete.MatchFilter}
	}
	return batchAttachmentBinding{}
}

func (bindings batchAttachmentBindings) compatible(operations []recordops.BatchOperation) bool {
	if len(bindings) != len(operations) {
		return false
	}
	for index, expected := range bindings {
		actual := batchAttachmentBindingFor(operations[index])
		if !sameBatchOperationBinding(expected, actual) || expected.idempotencyKey != actual.idempotencyKey || !bytes.Equal(expected.idempotencyContext, actual.idempotencyContext) {
			return false
		}
	}
	return true
}

func sameBatchOperationBinding(left, right batchAttachmentBinding) bool {
	return left.kind == right.kind && left.collection == right.collection && left.id == right.id && left.publicID == right.publicID && left.matchFilter == right.matchFilter && sameEntityTagSet(left.ifMatch, right.ifMatch) && sameStrings(left.sidecarFields, right.sidecarFields)
}

func sameEntityTagSet(left, right recordops.EntityTagSet) bool {
	return left.Present == right.Present && left.Any == right.Any && sameStrings(left.Values, right.Values)
}

func sameStrings(left, right []string) bool {
	return len(left) == len(right) && func() bool {
		for index := range left {
			if left[index] != right[index] {
				return false
			}
		}
		return true
	}()
}

type batchIdentities []batchIdentity

type batchIdentity struct {
	key     string
	context []byte
	target  batchAttachmentBinding
	create  *recordops.CreateRequest
}

func captureBatchIdentities(operations []recordops.BatchOperation) batchIdentities {
	identities := make(batchIdentities, len(operations))
	for index, operation := range operations {
		if operation.Create != nil {
			identities[index] = batchIdentity{key: operation.Create.IdempotencyKey, context: append([]byte(nil), operation.Create.IdempotencyContext...), target: batchAttachmentBindingFor(operation), create: operation.Create}
		}
	}
	return identities
}

func (identities batchIdentities) restore(operations []recordops.BatchOperation) bool {
	if len(identities) != len(operations) {
		for _, identity := range identities {
			if identity.key != "" {
				return false
			}
		}
		return true
	}
	for index, identity := range identities {
		if identity.key == "" {
			continue
		}
		if operations[index].Create != identity.create || !sameBatchOperationBinding(identity.target, batchAttachmentBindingFor(operations[index])) {
			return false
		}
		operations[index].Create.IdempotencyKey = identity.key
		operations[index].Create.IdempotencyContext = append([]byte(nil), identity.context...)
	}
	return true
}

type batchResult struct {
	Status  int               `json:"status,omitempty"`
	Headers map[string]string `json:"headers,omitempty"`
	Body    any               `json:"body,omitempty"`
	EventID string            `json:"eventId"`
	Record  map[string]any    `json:"record,omitempty"`
	ETag    string            `json:"etag,omitempty"`
	Version int64             `json:"version,omitempty"`
}

func (e recordEndpoints) decodeBatch(r *http.Request, principal identity.Subject) (batchInput, namedStagedFiles, error) {
	media, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if media != "multipart/form-data" {
		var input batchInput
		return input, namedStagedFiles{}, decodeStrictJSON(r, &input)
	}
	if e.files == nil {
		return batchInput{}, nil, errors.New("files unavailable")
	}
	reader, err := r.MultipartReader()
	if err != nil {
		return batchInput{}, nil, err
	}
	var input batchInput
	staged := namedStagedFiles{}
	seen := map[string]bool{}
	// An anonymous batch writes nothing to disk or storage until every target
	// collection passed the admission preflight, so its requests part must come
	// before any file part. A file part ahead of it is refused rather than held:
	// holding spooled parts would keep one open temporary file per part, and
	// the part count is unbounded. Every admitted part is spooled, staged and
	// released before the next one is read.
	admitted := e.access == nil || principal.Kind != identity.Anonymous
	for {
		part, err := reader.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return batchInput{}, staged, err
		}
		name := part.FormName()
		if name == "" || seen[name] {
			return batchInput{}, staged, errors.Join(errors.New("duplicate part"), part.Close())
		}
		seen[name] = true
		if name == "requests" {
			data, readErr := io.ReadAll(io.LimitReader(part, maximumBatchRequestBytes+1))
			closeErr := part.Close()
			if readErr != nil || closeErr != nil || len(data) > maximumBatchRequestBytes || part.FileName() != "" || decodeStrictJSONBytes(data, &input) != nil {
				return batchInput{}, staged, errors.New("invalid requests")
			}
			continue
		}
		if part.FileName() == "" {
			return batchInput{}, staged, errors.Join(errors.New("file name required"), part.Close())
		}
		if !admitted {
			if !seen["requests"] {
				return batchInput{}, staged, errors.Join(errors.New("anonymous batch files must follow the requests part"), part.Close())
			}
			if err := e.anonymousBatchAdmission(r, input); err != nil {
				return batchInput{}, staged, errors.Join(err, part.Close())
			}
			admitted = true
		}
		contentType := strings.TrimSpace(strings.Split(part.Header.Get("Content-Type"), ";")[0])
		temporary, tempErr := os.CreateTemp("", "pgbase-batch-upload-*")
		if tempErr != nil {
			return batchInput{}, staged, errors.Join(tempErr, part.Close())
		}
		temporaryName := temporary.Name()
		hash := sha256.New()
		size, copyErr := io.Copy(io.MultiWriter(temporary, hash), io.LimitReader(part, (100<<20)+1))
		copyErr = errors.Join(copyErr, part.Close())
		if copyErr != nil || size > 100<<20 {
			_ = temporary.Close()
			_ = os.Remove(temporaryName)
			return batchInput{}, staged, errors.Join(errors.New("part too large"), copyErr)
		}
		if _, seekErr := temporary.Seek(0, io.SeekStart); seekErr != nil {
			_ = temporary.Close()
			_ = os.Remove(temporaryName)
			return batchInput{}, staged, seekErr
		}
		upload := pendingBatchUpload{filename: part.FileName(), contentType: contentType, size: size, temporary: temporary}
		copy(upload.digest[:], hash.Sum(nil))
		file, stageErr := upload.stage(r.Context(), e.files, principal)
		if stageErr != nil {
			return batchInput{}, staged, stageErr
		}
		staged[name] = file
	}
	if !seen["requests"] {
		return batchInput{}, staged, errors.New("requests missing")
	}
	return input, staged, nil
}

// batchAdmissionError carries an anonymous admission preflight failure out of
// multipart decoding. A malformed failure is answered as the invalid request
// the handler would have reported after staging; any other failure is the
// record service's own admission result.
type batchAdmissionError struct {
	err       error
	malformed bool
}

func (e *batchAdmissionError) Error() string { return e.err.Error() }
func (e *batchAdmissionError) Unwrap() error { return e.err }

// anonymousBatchAdmission preflights every target collection of an anonymous
// multipart batch before any file reaches storage. It mirrors the order in
// which the handler used to fail after staging: an unparseable request or a
// collection the file service cannot resolve was an invalid request (the
// attachment extraction rejects it before the record service runs), and only
// then did the record service apply admission. A batch that names no
// resolvable target is refused here too, so it cannot stage files first.
func (e recordEndpoints) anonymousBatchAdmission(r *http.Request, input batchInput) error {
	collections := make([]string, 0, len(input.Operations)+len(input.Requests))
	for _, operation := range input.Operations {
		collections = append(collections, operation.Collection)
	}
	for _, request := range input.Requests {
		item, valid := batchHTTPRequestOperation(request)
		if !valid {
			return &batchAdmissionError{err: errors.New("invalid batch request"), malformed: true}
		}
		collections = append(collections, item.Collection)
	}
	if len(collections) == 0 {
		return &batchAdmissionError{err: errors.New("empty batch"), malformed: true}
	}
	for _, name := range collections {
		if _, err := e.files.CollectionID(r.Context(), name); err != nil {
			return &batchAdmissionError{err: err, malformed: true}
		}
	}
	if err := e.anonymousWriteAdmission(r, collections...); err != nil {
		return &batchAdmissionError{err: err}
	}
	return nil
}

// pendingBatchUpload is a multipart file part already spooled to a temporary
// file and hashed, but not yet written to file storage.
type pendingBatchUpload struct {
	filename, contentType string
	size                  int64
	digest                [32]byte
	temporary             *os.File
}

// stage writes the spooled file to storage and always releases the temporary
// file.
func (u pendingBatchUpload) stage(ctx context.Context, files FileService, principal identity.Subject) (filestore.StagedFile, error) {
	file, err := files.Stage(ctx, principal, filestore.Upload{Filename: u.filename, ContentType: u.contentType, Size: u.size, SHA256: u.digest, Body: u.temporary})
	if err != nil {
		u.discard()
		return filestore.StagedFile{}, err
	}
	closeErr := u.temporary.Close()
	removeErr := os.Remove(u.temporary.Name())
	if closeErr != nil || removeErr != nil {
		return file, errors.Join(closeErr, removeErr)
	}
	return file, nil
}

func (u pendingBatchUpload) discard() {
	_ = u.temporary.Close()
	_ = os.Remove(u.temporary.Name())
}

func batchHTTPRequestOperation(request batchHTTPRequest) (batchOperationInput, bool) {
	if len(request.Headers) > 4 {
		return batchOperationInput{}, false
	}
	for key := range request.Headers {
		if strings.EqualFold(key, "authorization") || strings.EqualFold(key, "cookie") {
			return batchOperationInput{}, false
		}
	}
	m := batchRecordURL.FindStringSubmatch(request.URL)
	if m == nil {
		return batchOperationInput{}, false
	}
	collection, id := m[2], m[3]
	if id != "" {
		var err error
		id, err = url.PathUnescape(id)
		if err != nil || id == "" || len(id) > 1024 {
			return batchOperationInput{}, false
		}
	}
	method := strings.ToUpper(request.Method)
	ifMatch := batchHeader(request.Headers, "If-Match")
	switch method {
	case "POST":
		if id != "" {
			return batchOperationInput{}, false
		}
		return batchOperationInput{Operation: "create", Collection: collection, Input: request.Body, IdempotencyKey: batchHeader(request.Headers, "Idempotency-Key")}, true
	case "PATCH":
		return batchOperationInput{Operation: "update", Collection: collection, ID: id, Input: request.Body, IfMatch: ifMatch}, id != ""
	case "PUT":
		return batchOperationInput{Operation: "upsert", Collection: collection, ID: id, Input: request.Body, IfMatch: ifMatch}, id != ""
	case "DELETE":
		return batchOperationInput{Operation: "delete", Collection: collection, ID: id, IfMatch: ifMatch}, id != "" && request.Body == nil
	default:
		return batchOperationInput{}, false
	}
}

func batchHeader(headers map[string]string, name string) string {
	for key, value := range headers {
		if strings.EqualFold(key, name) {
			return value
		}
	}
	return ""
}

func batchOperation(input batchOperationInput) (recordops.BatchOperation, bool) {
	if input.MatchFilter != "" && input.Operation != "delete" {
		return recordops.BatchOperation{}, false
	}
	switch input.Operation {
	case "create":
		if input.ID != "" || input.IfMatch != "" {
			return recordops.BatchOperation{}, false
		}
		return recordops.BatchOperation{Create: &recordops.CreateRequest{Collection: input.Collection, Input: input.Input, IdempotencyKey: input.IdempotencyKey, IdempotencyContext: input.IdempotencyContext}}, input.Collection != "" && input.Input != nil
	case "update", "delete", "upsert":
		if input.ID == "" || len(input.ID) > 1024 || input.Collection == "" {
			return recordops.BatchOperation{}, false
		}
		ifMatch, ok := parseIfMatch(http.Header{"If-Match": []string{input.IfMatch}})
		if input.IfMatch == "" {
			ifMatch = recordops.EntityTagSet{}
			ok = true
		}
		if !ok {
			return recordops.BatchOperation{}, false
		}
		if input.Operation == "delete" {
			if input.Input != nil || input.IdempotencyKey != "" {
				return recordops.BatchOperation{}, false
			}
			return recordops.BatchOperation{Delete: &recordops.DeleteRequest{Collection: input.Collection, PublicID: input.ID, IfMatch: ifMatch, MatchFilter: input.MatchFilter}}, true
		}
		if input.IdempotencyKey != "" {
			return recordops.BatchOperation{}, false
		}
		if input.Operation == "upsert" {
			return recordops.BatchOperation{Upsert: &recordops.UpdateRequest{Collection: input.Collection, PublicID: input.ID, Input: input.Input, IfMatch: ifMatch, SidecarFields: input.SidecarFields, SidecarContext: input.IdempotencyContext}}, input.Input != nil
		}
		return recordops.BatchOperation{Update: &recordops.UpdateRequest{Collection: input.Collection, PublicID: input.ID, Input: input.Input, IfMatch: ifMatch, SidecarFields: input.SidecarFields, SidecarContext: input.IdempotencyContext}}, input.Input != nil
	default:
		return recordops.BatchOperation{}, false
	}
}
