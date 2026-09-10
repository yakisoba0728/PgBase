package pgbase

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync"

	"github.com/google/uuid"
	"github.com/yakisoba0728/PgBase/internal/api"
	"github.com/yakisoba0728/PgBase/internal/filestore"
	"github.com/yakisoba0728/PgBase/internal/identity"
	"github.com/yakisoba0728/PgBase/internal/jsvm"
	"github.com/yakisoba0728/PgBase/internal/mailer"
	"github.com/yakisoba0728/PgBase/internal/recordops"
)

type extensionBridge struct {
	records       *recordops.Service
	files         *filestore.Service
	mailer        mailer.Sender
	logger        *slog.Logger
	hooks         *HookRegistry
	templates     *TemplateRegistry
	database      *TrustedDatabase
	objects       *ExtensionObjects
	compatibility *extensionCompatibility
}

type extensionHost struct {
	bridge           *extensionBridge
	principal        identity.Subject
	requestID        string
	valid            bool
	hookEvent        string
	authorityContext context.Context
	fileMu           sync.Mutex
	fileHandles      map[uuid.UUID]extensionFileHandle
	fileBytesRead    int64
	namespace        string
}

func (bridge *extensionBridge) forRequest(ctx context.Context) jsvm.Host {
	principal, requestID, ok := api.ExtensionRequestContext(ctx)
	return &extensionHost{bridge: bridge, principal: principal, requestID: requestID, valid: ok, authorityContext: ctx}
}

func namespacedHost(host jsvm.Host, namespace string) jsvm.Host {
	if value, ok := host.(*extensionHost); ok {
		value.namespace = namespace
	}
	return host
}

func (bridge *extensionBridge) forSubject(principal identity.Subject, requestID string) jsvm.Host {
	return &extensionHost{bridge: bridge, principal: principal, requestID: requestID, valid: principal.Valid() && requestID != ""}
}

func (bridge *extensionBridge) forHook(ctx context.Context, event string) jsvm.Host {
	host := bridge.forRequest(ctx).(*extensionHost)
	host.hookEvent = event
	return host
}

func (host *extensionHost) Call(ctx context.Context, method string, input json.RawMessage) (json.RawMessage, error) {
	operationContext, cleanup, err := host.operationContext(ctx)
	if err != nil {
		return nil, extensionError("extension_context_expired", err)
	}
	defer cleanup()
	if host != nil && host.bridge != nil && strings.HasPrefix(method, "log.") {
		return host.callLog(operationContext, method, input)
	}
	if host == nil || host.bridge == nil || !host.valid {
		return nil, extensionError("extension_context_unavailable", errors.New("request admission context is unavailable"))
	}
	if strings.HasPrefix(host.hookEvent, "record.") && (method == "records.create" || method == "records.update" || method == "records.delete" || method == "records.batch") ||
		strings.HasPrefix(host.hookEvent, "mail.") && method == "mail.send" ||
		strings.HasPrefix(host.hookEvent, "file.") && (method == "files.upload" || method == "files.delete") {
		return nil, extensionError("js_reentrant_operation", errors.New("hook cannot reenter its mutating operation family"))
	}
	switch method {
	case "records.list", "records.get", "records.create", "records.update", "records.delete", "records.batch":
		return host.callRecords(operationContext, method, input)
	case "mail.send":
		return host.callMail(operationContext, input)
	case "files.list", "files.upload", "files.delete", "files.token", "files.read", "files.open", "files.readChunk", "files.close":
		return host.callFiles(operationContext, method, input)
	case "template.render":
		if host.bridge.templates == nil {
			return nil, extensionError("template_unavailable", errors.New("template registry is unavailable"))
		}
		var request struct {
			Name string          `json:"name"`
			Data json.RawMessage `json:"data"`
		}
		if err := decodeExtensionInput(input, &request); err != nil {
			return nil, err
		}
		output, err := host.bridge.templates.renderJSON(request.Name, request.Data)
		if err != nil {
			return nil, extensionError("template_render_failed", err)
		}
		return json.Marshal(map[string]string{"output": output})
	case "database.transaction":
		return host.callDatabase(operationContext, input)
	case "objects.read", "objects.write", "objects.delete", "objects.list", "objects.copy":
		return host.callObjects(operationContext, method, input)
	case "http.send", "os.args", "os.getenv", "os.exit", "os.stat", "os.readFile", "os.writeFile", "os.readDir", "os.truncate", "os.getwd", "os.mkdir", "os.mkdirAll", "os.rename", "os.remove", "os.removeAll", "os.cmd", "filesystem.local", "filesystem.fileFromPath", "filesystem.fileFromBytes", "filesystem.fileFromURL":
		if host.bridge.compatibility == nil {
			return nil, extensionError("compatibility_unavailable", errors.New("PocketBase compatibility adapter unavailable"))
		}
		return host.bridge.compatibility.call(operationContext, method, input)
	case "filesystem.read", "filesystem.write", "filesystem.delete", "filesystem.list", "filesystem.copy":
		return host.callObjects(operationContext, "objects."+strings.TrimPrefix(method, "filesystem."), input)
	default:
		return nil, extensionError("unsupported_operation", errors.New("unsupported extension operation"))
	}
}

func (host *extensionHost) operationContext(caller context.Context) (context.Context, func(), error) {
	if caller == nil {
		return nil, func() {}, errors.New("caller context is nil")
	}
	if host == nil || host.authorityContext == nil {
		return caller, func() {}, nil
	}
	if err := host.authorityContext.Err(); err != nil {
		return nil, func() {}, err
	}
	combined, cancel := context.WithCancelCause(caller)
	stop := context.AfterFunc(host.authorityContext, func() {
		cancel(context.Cause(host.authorityContext))
	})
	cleanup := func() {
		stop()
		cancel(nil)
	}
	if err := combined.Err(); err != nil {
		cleanup()
		return nil, func() {}, context.Cause(combined)
	}
	return combined, cleanup, nil
}

type extensionRecordRequest struct {
	Collection     string                     `json:"collection"`
	ID             string                     `json:"id,omitempty"`
	Cursor         string                     `json:"cursor,omitempty"`
	Limit          int                        `json:"limit,omitempty"`
	Filter         string                     `json:"filter,omitempty"`
	Sort           string                     `json:"sort,omitempty"`
	Expand         string                     `json:"expand,omitempty"`
	Input          map[string]json.RawMessage `json:"input,omitempty"`
	IdempotencyKey string                     `json:"idempotencyKey,omitempty"`
	IfMatch        string                     `json:"ifMatch,omitempty"`
	Operations     []extensionBatchOperation  `json:"operations,omitempty"`
}

type extensionBatchOperation struct {
	Operation      string                     `json:"operation"`
	Collection     string                     `json:"collection"`
	ID             string                     `json:"id,omitempty"`
	Input          map[string]json.RawMessage `json:"input,omitempty"`
	IdempotencyKey string                     `json:"idempotencyKey,omitempty"`
	IfMatch        string                     `json:"ifMatch,omitempty"`
}

type extensionRecordResult struct {
	Data          map[string]any          `json:"data,omitempty"`
	ETag          string                  `json:"etag,omitempty"`
	Version       int64                   `json:"version,omitempty"`
	EventID       string                  `json:"eventId,omitempty"`
	Replayed      bool                    `json:"replayed,omitempty"`
	Compatibility string                  `json:"compatibility,omitempty"`
	Items         []map[string]any        `json:"items,omitempty"`
	NextCursor    string                  `json:"nextCursor,omitempty"`
	Results       []extensionRecordResult `json:"results,omitempty"`
}

func (host *extensionHost) callRecords(ctx context.Context, method string, raw json.RawMessage) (json.RawMessage, error) {
	if host.bridge.records == nil {
		return nil, extensionError("records_unavailable", errors.New("record operations are unavailable"))
	}
	var request extensionRecordRequest
	if err := decodeExtensionInput(raw, &request); err != nil || request.Collection == "" && method != "records.batch" {
		return nil, extensionError("invalid_record_request", err)
	}
	switch method {
	case "records.list":
		if request.Limit == 0 {
			request.Limit = 50
		}
		event := &api.RecordRequestEvent{Actor: host.principal, RequestID: host.requestID, Collection: request.Collection,
			Cursor: request.Cursor, Limit: request.Limit, Filter: request.Filter, Sort: request.Sort, Expand: request.Expand}
		finish, err := host.beginRecordLifecycle(ctx, HookRecordListRequest, event)
		if err != nil {
			finish(err)
			return nil, err
		}
		page, err := host.bridge.records.List(ctx, recordops.ListRequest{Principal: host.principal, RequestID: host.requestID,
			Collection: request.Collection, Cursor: event.Cursor, Limit: event.Limit, Filter: event.Filter, Sort: event.Sort, Expand: event.Expand})
		finish(err)
		return marshalExtensionResult(extensionRecordResult{Items: page.Items, NextCursor: page.NextCursor}, err)
	case "records.get":
		id, err := requiredUUID(request.ID)
		if err != nil {
			return nil, extensionError("invalid_record_request", err)
		}
		event := &api.RecordRequestEvent{Actor: host.principal, RequestID: host.requestID, Collection: request.Collection, RecordID: id, Expand: request.Expand}
		finish, err := host.beginRecordLifecycle(ctx, HookRecordViewRequest, event)
		if err != nil {
			finish(err)
			return nil, err
		}
		record, err := host.bridge.records.Get(ctx, recordops.GetRequest{Principal: host.principal, RequestID: host.requestID, Collection: request.Collection, ID: id, Expand: event.Expand})
		finish(err)
		return marshalExtensionResult(publicRecordResult(record, recordops.MutationResult{}), err)
	case "records.create":
		event := &api.RecordRequestEvent{Actor: host.principal, RequestID: host.requestID, Collection: request.Collection,
			IdempotencyKey: request.IdempotencyKey, Input: cloneRawFields(request.Input)}
		finish, err := host.beginRecordLifecycle(ctx, HookRecordCreateRequest, event)
		if err != nil {
			finish(err)
			return nil, err
		}
		result, err := host.bridge.records.Create(ctx, recordops.CreateRequest{Principal: host.principal, RequestID: host.requestID,
			Collection: request.Collection, Input: event.Input, IdempotencyKey: request.IdempotencyKey})
		event.Result = cloneExtensionMutationResult(result)
		finish(err)
		return marshalExtensionResult(publicMutationResult(result), err)
	case "records.update":
		id, err := requiredUUID(request.ID)
		if err != nil {
			return nil, extensionError("invalid_record_request", err)
		}
		event := &api.RecordRequestEvent{Actor: host.principal, RequestID: host.requestID, Collection: request.Collection,
			RecordID: id, Input: cloneRawFields(request.Input)}
		finish, err := host.beginRecordLifecycle(ctx, HookRecordUpdateRequest, event)
		if err != nil {
			finish(err)
			return nil, err
		}
		result, err := host.bridge.records.Update(ctx, recordops.UpdateRequest{Principal: host.principal, RequestID: host.requestID,
			Collection: request.Collection, ID: id, Input: event.Input, IfMatch: extensionIfMatch(request.IfMatch)})
		event.Result = cloneExtensionMutationResult(result)
		finish(err)
		return marshalExtensionResult(publicMutationResult(result), err)
	case "records.delete":
		id, err := requiredUUID(request.ID)
		if err != nil {
			return nil, extensionError("invalid_record_request", err)
		}
		event := &api.RecordRequestEvent{Actor: host.principal, RequestID: host.requestID, Collection: request.Collection, RecordID: id}
		finish, err := host.beginRecordLifecycle(ctx, HookRecordDeleteRequest, event)
		if err != nil {
			finish(err)
			return nil, err
		}
		result, err := host.bridge.records.Delete(ctx, recordops.DeleteRequest{Principal: host.principal, RequestID: host.requestID,
			Collection: request.Collection, ID: id, IfMatch: extensionIfMatch(request.IfMatch)})
		event.Result = cloneExtensionMutationResult(result)
		finish(err)
		return marshalExtensionResult(publicMutationResult(result), err)
	case "records.batch":
		return host.callRecordBatch(ctx, request.Operations)
	}
	return nil, extensionError("unsupported_operation", nil)
}

func (host *extensionHost) beginRecordLifecycle(ctx context.Context, kind string, event *api.RecordRequestEvent) (func(error), error) {
	if host.bridge.hooks == nil {
		return func(error) {}, nil
	}
	return host.bridge.hooks.Begin(ctx, &HookEvent{Kind: kind, Data: event})
}

func cloneExtensionMutationResult(result recordops.MutationResult) *recordops.MutationResult {
	cloned := result
	if result.Record != nil {
		record := *result.Record
		record.Data = make(map[string]any, len(result.Record.Data))
		for name, value := range result.Record.Data {
			record.Data[name] = value
		}
		cloned.Record = &record
	}
	return &cloned
}

func (host *extensionHost) callRecordBatch(ctx context.Context, operations []extensionBatchOperation) (json.RawMessage, error) {
	if len(operations) == 0 || len(operations) > 50 {
		return nil, extensionError("invalid_record_request", errors.New("batch operation count is invalid"))
	}
	converted := make([]recordops.BatchOperation, len(operations))
	for index, operation := range operations {
		id := uuid.Nil
		var err error
		if operation.ID != "" {
			id, err = requiredUUID(operation.ID)
			if err != nil {
				return nil, extensionError("invalid_record_request", err)
			}
		}
		switch operation.Operation {
		case "create":
			converted[index].Create = &recordops.CreateRequest{Collection: operation.Collection, ID: id, Input: operation.Input, IdempotencyKey: operation.IdempotencyKey}
		case "update":
			converted[index].Update = &recordops.UpdateRequest{Collection: operation.Collection, ID: id, Input: operation.Input, IfMatch: extensionIfMatch(operation.IfMatch)}
		case "upsert":
			converted[index].Upsert = &recordops.UpdateRequest{Collection: operation.Collection, ID: id, Input: operation.Input, IfMatch: extensionIfMatch(operation.IfMatch)}
		case "delete":
			converted[index].Delete = &recordops.DeleteRequest{Collection: operation.Collection, ID: id, IfMatch: extensionIfMatch(operation.IfMatch)}
		default:
			return nil, extensionError("invalid_record_request", errors.New("batch operation is invalid"))
		}
	}
	event := &api.BatchRequestEvent{Actor: host.principal, RequestID: host.requestID, Operations: converted}
	finish := func(error) {}
	var err error
	if host.bridge.hooks != nil {
		finish, err = host.bridge.hooks.Begin(ctx, &HookEvent{Kind: HookRecordBatchRequest, Data: event})
		if err != nil {
			finish(err)
			return nil, err
		}
	}
	results, err := host.bridge.records.Batch(ctx, recordops.BatchRequest{Principal: host.principal, RequestID: host.requestID, Operations: event.Operations})
	event.Results = append([]recordops.MutationResult(nil), results...)
	finish(err)
	if err != nil {
		return nil, mapExtensionError(err)
	}
	public := make([]extensionRecordResult, len(results))
	for index, result := range results {
		public[index] = publicMutationResult(result)
	}
	return json.Marshal(extensionRecordResult{Results: public})
}

func publicRecordResult(record recordops.Record, mutation recordops.MutationResult) extensionRecordResult {
	return extensionRecordResult{Data: record.Data, ETag: record.ETag, Version: record.Version,
		EventID: emptyUUID(mutation.EventID), Replayed: mutation.Replayed, Compatibility: string(mutation.Compatibility)}
}

func publicMutationResult(result recordops.MutationResult) extensionRecordResult {
	if result.Record == nil {
		return extensionRecordResult{EventID: emptyUUID(result.EventID), Replayed: result.Replayed, Compatibility: string(result.Compatibility)}
	}
	return publicRecordResult(*result.Record, result)
}

func emptyUUID(id uuid.UUID) string {
	if id == uuid.Nil {
		return ""
	}
	return id.String()
}

func extensionIfMatch(value string) recordops.EntityTagSet {
	if value == "" {
		return recordops.EntityTagSet{}
	}
	if value == "*" {
		return recordops.EntityTagSet{Present: true, Any: true}
	}
	return recordops.EntityTagSet{Present: true, Values: []string{value}}
}

func requiredUUID(value string) (uuid.UUID, error) {
	id, err := uuid.Parse(value)
	if err != nil || id == uuid.Nil || id.String() != strings.ToLower(value) {
		return uuid.Nil, errors.New("identifier must be a canonical UUID")
	}
	return id, nil
}

func marshalExtensionResult(value any, err error) (json.RawMessage, error) {
	if err != nil {
		return nil, mapExtensionError(err)
	}
	return json.Marshal(value)
}

type extensionMailRequest struct {
	To          string                    `json:"to"`
	Subject     string                    `json:"subject"`
	Text        string                    `json:"text"`
	HTML        string                    `json:"html,omitempty"`
	Attachments []extensionMailAttachment `json:"attachments,omitempty"`
}
type extensionMailAttachment struct {
	Filename    string `json:"filename"`
	ContentType string `json:"contentType,omitempty"`
	DataBase64  string `json:"dataBase64"`
}

func (host *extensionHost) callMail(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	if host.bridge.mailer == nil {
		return nil, extensionError("mail_unavailable", errors.New("mail transport is unavailable"))
	}
	var request extensionMailRequest
	if err := decodeExtensionInput(raw, &request); err != nil {
		return nil, extensionError("invalid_mail_request", err)
	}
	attachments := make([]mailer.Attachment, len(request.Attachments))
	for index, attachment := range request.Attachments {
		data, err := base64.StdEncoding.DecodeString(attachment.DataBase64)
		if err != nil {
			return nil, extensionError("invalid_mail_request", err)
		}
		attachments[index] = mailer.Attachment{Filename: attachment.Filename, ContentType: attachment.ContentType, Data: data}
	}
	if err := host.bridge.mailer.Send(ctx, mailer.Message{To: request.To, Subject: request.Subject, Text: request.Text, HTML: request.HTML, Attachments: attachments}); err != nil {
		return nil, mapExtensionError(err)
	}
	return json.RawMessage(`{"sent":true}`), nil
}

type extensionFileRequest struct {
	Collection   string `json:"collection,omitempty"`
	RecordID     string `json:"recordId,omitempty"`
	Field        string `json:"field,omitempty"`
	FileID       string `json:"fileId,omitempty"`
	Filename     string `json:"filename,omitempty"`
	ContentType  string `json:"contentType,omitempty"`
	DataBase64   string `json:"dataBase64,omitempty"`
	SHA256       string `json:"sha256,omitempty"`
	Variant      string `json:"variant,omitempty"`
	Token        string `json:"token,omitempty"`
	Handle       string `json:"handle,omitempty"`
	MaximumBytes int    `json:"maximumBytes,omitempty"`
}

type extensionFileResult struct {
	ID          string                `json:"id,omitempty"`
	RecordID    string                `json:"recordId,omitempty"`
	Field       string                `json:"field,omitempty"`
	Filename    string                `json:"filename,omitempty"`
	ContentType string                `json:"contentType,omitempty"`
	Size        int64                 `json:"size,omitempty"`
	SHA256      string                `json:"sha256,omitempty"`
	Token       string                `json:"token,omitempty"`
	DataBase64  string                `json:"dataBase64,omitempty"`
	Deleted     bool                  `json:"deleted,omitempty"`
	Handle      string                `json:"handle,omitempty"`
	EOF         bool                  `json:"eof,omitempty"`
	Closed      bool                  `json:"closed,omitempty"`
	Items       []extensionFileResult `json:"items,omitempty"`
}

func (host *extensionHost) callFiles(ctx context.Context, method string, raw json.RawMessage) (result json.RawMessage, resultErr error) {
	if host.bridge.files == nil {
		return nil, extensionError("files_unavailable", errors.New("file operations are unavailable"))
	}
	var request extensionFileRequest
	if err := decodeExtensionInput(raw, &request); err != nil {
		return nil, extensionError("invalid_file_request", err)
	}
	if (method == "files.read" || method == "files.upload") && host.hasActiveFileStream() {
		return nil, extensionError("file_stream_active", errors.New("close the active file stream before starting another storage operation"))
	}
	switch method {
	case "files.list":
		collectionID, recordID, err := host.fileCoordinates(ctx, request)
		if err != nil {
			return nil, err
		}
		files, err := host.bridge.files.List(ctx, host.principal, collectionID, recordID, request.Field)
		if err != nil {
			return nil, mapExtensionError(err)
		}
		items := make([]extensionFileResult, len(files))
		for index, file := range files {
			items[index] = publicFileResult(file)
		}
		return json.Marshal(extensionFileResult{Items: items})
	case "files.upload":
		collectionID, recordID, err := host.fileCoordinates(ctx, request)
		if err != nil {
			return nil, err
		}
		body, err := base64.StdEncoding.DecodeString(request.DataBase64)
		if err != nil {
			return nil, extensionError("invalid_file_request", err)
		}
		digest := sha256.Sum256(body)
		if request.SHA256 != "" && !strings.EqualFold(request.SHA256, hex.EncodeToString(digest[:])) {
			return nil, extensionError("file_checksum_mismatch", errors.New("file checksum mismatch"))
		}
		file, err := host.bridge.files.Upload(ctx, host.principal, filestore.Upload{CollectionID: collectionID, RecordID: recordID,
			Field: request.Field, Filename: request.Filename, ContentType: request.ContentType, Size: int64(len(body)), SHA256: digest, Body: bytes.NewReader(body)})
		return marshalExtensionResult(publicFileResult(file), err)
	case "files.delete":
		id, err := requiredUUID(request.FileID)
		if err != nil {
			return nil, extensionError("invalid_file_request", err)
		}
		if err := host.bridge.files.Delete(ctx, host.principal, id); err != nil {
			return nil, mapExtensionError(err)
		}
		return json.Marshal(extensionFileResult{Deleted: true})
	case "files.token":
		id, err := requiredUUID(request.FileID)
		if err != nil {
			return nil, extensionError("invalid_file_request", err)
		}
		token, err := host.bridge.files.MintDownloadToken(ctx, host.principal, id, extensionVariant(request.Variant))
		return marshalExtensionResult(extensionFileResult{ID: id.String(), Token: token}, err)
	case "files.read":
		id, err := requiredUUID(request.FileID)
		if err != nil {
			return nil, extensionError("invalid_file_request", err)
		}
		token := request.Token
		variant := extensionVariant(request.Variant)
		if token == "" {
			token, err = host.bridge.files.MintDownloadToken(ctx, host.principal, id, variant)
			if err != nil {
				return nil, mapExtensionError(err)
			}
		}
		file, reader, err := host.bridge.files.Open(ctx, host.principal, id, variant, token)
		if err != nil {
			return nil, mapExtensionError(err)
		}
		defer func() {
			if closeErr := reader.Close(); closeErr != nil {
				resultErr = errors.Join(resultErr, mapExtensionError(closeErr))
			}
		}()
		resultData, err := io.ReadAll(io.LimitReader(reader, 3<<20+1))
		if err != nil || len(resultData) > 3<<20 {
			return nil, extensionError("file_too_large", err)
		}
		operationResult := publicFileResult(file)
		operationResult.DataBase64 = base64.StdEncoding.EncodeToString(resultData)
		return json.Marshal(operationResult)
	case "files.open":
		return host.openFileHandle(ctx, request)
	case "files.readChunk":
		return host.readFileChunk(request)
	case "files.close":
		return host.closeFileHandle(request)
	}
	return nil, extensionError("unsupported_operation", nil)
}

func (host *extensionHost) fileCoordinates(ctx context.Context, request extensionFileRequest) (uuid.UUID, uuid.UUID, error) {
	collectionID, err := host.bridge.files.CollectionID(ctx, request.Collection)
	if err != nil {
		return uuid.Nil, uuid.Nil, mapExtensionError(err)
	}
	recordID, err := requiredUUID(request.RecordID)
	if err != nil || request.Field == "" {
		return uuid.Nil, uuid.Nil, extensionError("invalid_file_request", err)
	}
	return collectionID, recordID, nil
}

func extensionVariant(value string) filestore.Variant {
	if value == string(filestore.Thumbnail) {
		return filestore.Thumbnail
	}
	return filestore.Original
}

func publicFileResult(file filestore.File) extensionFileResult {
	return extensionFileResult{ID: emptyUUID(file.ID), RecordID: emptyUUID(file.RecordID), Field: file.Field,
		Filename: file.Filename, ContentType: file.ContentType, Size: file.Size, SHA256: hex.EncodeToString(file.SHA256[:])}
}

type extensionLogRequest struct {
	Message string            `json:"message"`
	Fields  map[string]string `json:"fields,omitempty"`
}

func (host *extensionHost) callLog(ctx context.Context, method string, raw json.RawMessage) (json.RawMessage, error) {
	var request extensionLogRequest
	if err := decodeExtensionInput(raw, &request); err != nil || request.Message == "" || len(request.Message) > 1024 || len(request.Fields) > 16 {
		return nil, extensionError("invalid_log_request", err)
	}
	attributes := []any{"request_id", host.requestID}
	for name, value := range request.Fields {
		if len(name) > 64 || len(value) > 256 || strings.ContainsAny(name+value, "\r\n") {
			return nil, extensionError("invalid_log_request", errors.New("log field is invalid"))
		}
		attributes = append(attributes, name, value)
	}
	logger := host.bridge.logger
	if logger != nil {
		switch method {
		case "log.debug":
			logger.DebugContext(ctx, request.Message, attributes...)
		case "log.warn":
			logger.WarnContext(ctx, request.Message, attributes...)
		case "log.error":
			logger.ErrorContext(ctx, request.Message, attributes...)
		default:
			logger.InfoContext(ctx, request.Message, attributes...)
		}
	}
	return json.RawMessage(`{"logged":true}`), nil
}

func decodeExtensionInput(raw json.RawMessage, destination any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return errors.New("unexpected trailing JSON")
	}
	return nil
}

type extensionOperationError struct {
	code  string
	cause error
}

func (err *extensionOperationError) Error() string     { return err.code }
func (err *extensionOperationError) Unwrap() error     { return err.cause }
func (err *extensionOperationError) CodeValue() string { return err.code }

func extensionError(code string, cause error) error {
	return &extensionOperationError{code: code, cause: cause}
}

func mapExtensionError(err error) error {
	if err == nil {
		return nil
	}
	var operationError *recordops.Error
	if errors.As(err, &operationError) {
		return extensionError(string(operationError.Code), err)
	}
	switch {
	case errors.Is(err, context.Canceled):
		return extensionError("canceled", err)
	case errors.Is(err, context.DeadlineExceeded):
		return extensionError("timeout", err)
	case errors.Is(err, filestore.ErrDenied):
		return extensionError("permission_denied", err)
	case errors.Is(err, filestore.ErrNotFound):
		return extensionError("not_found", err)
	case errors.Is(err, filestore.ErrTooLarge):
		return extensionError("file_too_large", err)
	case errors.Is(err, filestore.ErrInvalid):
		return extensionError("invalid_file_request", err)
	default:
		return extensionError("operation_failed", fmt.Errorf("extension operation: %w", err))
	}
}
