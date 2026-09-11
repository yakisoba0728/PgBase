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
	input, staged, err := e.decodeBatch(r, principal)
	maximumRequests := settings.MaxRequests
	if err != nil || (len(input.Operations) == 0) == (len(input.Requests) == 0) || len(input.Operations)+len(input.Requests) > maximumRequests {
		e.cleanupStaged(r, staged)
		writeInvalidRecordRequest(w, r)
		return
	}
	operations := make([]recordops.BatchOperation, 0, len(input.Operations))
	statuses := make([]int, 0, 50)
	items := input.Operations
	if len(input.Requests) > 0 {
		items = make([]batchOperationInput, 0, len(input.Requests))
		for _, request := range input.Requests {
			item, status, valid := batchHTTPRequestOperation(request)
			if !valid {
				e.cleanupStaged(r, staged)
				writeInvalidRecordRequest(w, r)
				return
			}
			items = append(items, item)
			statuses = append(statuses, status)
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
	var results []recordops.MutationResult
	err = finishLifecycle(finish, func() error {
		results, err = service.Batch(r.Context(), recordops.BatchRequest{
			Principal: principal, RequestID: requestID(r.Context()), Operations: event.Operations, Participants: participants, RuleContext: ruleRequestContext(r),
		})
		event.Results = cloneMutationResults(results)
		return err
	})
	if err != nil {
		e.cleanupStaged(r, staged)
		writeRecordError(w, r, err)
		return
	}
	if len(staged) > 0 {
		e.files.CompleteStaged(staged.files(), nil)
		e.cleanupStaged(r, staged)
	}
	response := make([]batchResult, 0, len(results))
	for _, result := range results {
		item := batchResult{EventID: result.EventID.String()}
		if len(statuses) > 0 {
			item.Status = statuses[len(response)]
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
		response = append(response, item)
	}
	writeJSON(w, http.StatusOK, struct {
		Results []batchResult `json:"results"`
	}{response})
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
		return batchAttachmentBinding{kind: "delete", collection: operation.Delete.Collection, id: operation.Delete.ID, publicID: operation.Delete.PublicID, ifMatch: operation.Delete.IfMatch}
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
	return left.kind == right.kind && left.collection == right.collection && left.id == right.id && left.publicID == right.publicID && sameEntityTagSet(left.ifMatch, right.ifMatch) && sameStrings(left.sidecarFields, right.sidecarFields)
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
			if readErr != nil || closeErr != nil || len(data) > maximumBatchRequestBytes || part.FileName() != "" || json.Unmarshal(data, &input) != nil {
				return batchInput{}, staged, errors.New("invalid requests")
			}
			continue
		}
		if part.FileName() == "" {
			return batchInput{}, staged, errors.Join(errors.New("file name required"), part.Close())
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
		var digest [32]byte
		copy(digest[:], hash.Sum(nil))
		file, stageErr := e.files.Stage(r.Context(), principal, filestore.Upload{Filename: part.FileName(), ContentType: contentType, Size: size, SHA256: digest, Body: temporary})
		if stageErr != nil {
			_ = temporary.Close()
			_ = os.Remove(temporaryName)
			return batchInput{}, staged, stageErr
		}
		staged[name] = file
		closeErr := temporary.Close()
		removeErr := os.Remove(temporaryName)
		if closeErr != nil || removeErr != nil {
			return batchInput{}, staged, errors.Join(closeErr, removeErr)
		}
	}
	if !seen["requests"] {
		return batchInput{}, staged, errors.New("requests missing")
	}
	return input, staged, nil
}

func batchHTTPRequestOperation(request batchHTTPRequest) (batchOperationInput, int, bool) {
	if len(request.Headers) > 4 {
		return batchOperationInput{}, 0, false
	}
	for key := range request.Headers {
		if strings.EqualFold(key, "authorization") || strings.EqualFold(key, "cookie") {
			return batchOperationInput{}, 0, false
		}
	}
	m := batchRecordURL.FindStringSubmatch(request.URL)
	if m == nil {
		return batchOperationInput{}, 0, false
	}
	collection, id := m[2], m[3]
	if id != "" {
		var err error
		id, err = url.PathUnescape(id)
		if err != nil || id == "" || len(id) > 1024 {
			return batchOperationInput{}, 0, false
		}
	}
	method := strings.ToUpper(request.Method)
	ifMatch := batchHeader(request.Headers, "If-Match")
	switch method {
	case "POST":
		if id != "" {
			return batchOperationInput{}, 0, false
		}
		return batchOperationInput{Operation: "create", Collection: collection, Input: request.Body, IdempotencyKey: batchHeader(request.Headers, "Idempotency-Key")}, 201, true
	case "PATCH":
		return batchOperationInput{Operation: "update", Collection: collection, ID: id, Input: request.Body, IfMatch: ifMatch}, 200, id != ""
	case "PUT":
		return batchOperationInput{Operation: "upsert", Collection: collection, ID: id, Input: request.Body, IfMatch: ifMatch}, 200, id != ""
	case "DELETE":
		return batchOperationInput{Operation: "delete", Collection: collection, ID: id, IfMatch: ifMatch}, 204, id != "" && request.Body == nil
	default:
		return batchOperationInput{}, 0, false
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
			return recordops.BatchOperation{Delete: &recordops.DeleteRequest{Collection: input.Collection, PublicID: input.ID, IfMatch: ifMatch}}, true
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
