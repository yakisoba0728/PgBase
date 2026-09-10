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

	"github.com/google/uuid"
	"github.com/yakisoba0728/PgBase/internal/api"
)

// ExtensionContext exposes PgBase domain operations for one admitted request.
// It contains no session, bearer token, database handle, or storage key.
type ExtensionContext struct {
	Records ExtensionRecords
	Mail    ExtensionMail
	Files   ExtensionFiles
}

// ExtensionsForRequest derives authority from PgBase's request middleware.
// A plain or caller-forged context is rejected.
func (a *App) ExtensionsForRequest(ctx context.Context) (*ExtensionContext, error) {
	if a == nil || a.extensions == nil {
		return nil, errors.New("extension services are unavailable")
	}
	principal, requestID, ok := api.ExtensionRequestContext(ctx)
	if !ok {
		return nil, errors.New("extension request context is not admitted")
	}
	host := &extensionHost{bridge: a.extensions, principal: principal, requestID: requestID, valid: true, authorityContext: ctx}
	return &ExtensionContext{Records: ExtensionRecords{host}, Mail: ExtensionMail{host}, Files: ExtensionFiles{host}}, nil
}

type ExtensionListOptions struct {
	Cursor string
	Limit  int
	Filter string
	Sort   string
	Expand string
}

// ExtensionRecord preserves the exact record object as JSON so integers and
// decimal values do not pass through float64.
type ExtensionRecord struct {
	Data          json.RawMessage
	ETag          string
	Version       int64
	EventID       uuid.UUID
	Replayed      bool
	Compatibility string
}

type ExtensionRecordPage struct {
	Items      []json.RawMessage
	NextCursor string
}

type ExtensionRecordWrite struct {
	Collection     string
	ID             uuid.UUID
	Input          map[string]json.RawMessage
	IdempotencyKey string
	IfMatch        string
}

type ExtensionBatchOperation struct {
	Operation      string
	Collection     string
	ID             uuid.UUID
	Input          map[string]json.RawMessage
	IdempotencyKey string
	IfMatch        string
}

type ExtensionRecords struct{ host *extensionHost }

func (records ExtensionRecords) List(ctx context.Context, collection string, options ExtensionListOptions) (ExtensionRecordPage, error) {
	var output struct {
		Items      []json.RawMessage `json:"items"`
		NextCursor string            `json:"nextCursor"`
	}
	err := extensionCall(ctx, records.host, "records.list", extensionRecordRequest{Collection: collection, Cursor: options.Cursor,
		Limit: options.Limit, Filter: options.Filter, Sort: options.Sort, Expand: options.Expand}, &output)
	return ExtensionRecordPage{Items: output.Items, NextCursor: output.NextCursor}, err
}

func (records ExtensionRecords) Get(ctx context.Context, collection string, id uuid.UUID, expand string) (ExtensionRecord, error) {
	return records.one(ctx, "records.get", extensionRecordRequest{Collection: collection, ID: canonicalExtensionUUID(id), Expand: expand})
}

func (records ExtensionRecords) Create(ctx context.Context, request ExtensionRecordWrite) (ExtensionRecord, error) {
	return records.one(ctx, "records.create", extensionRecordRequest{Collection: request.Collection, Input: cloneRawFields(request.Input), IdempotencyKey: request.IdempotencyKey})
}

func (records ExtensionRecords) Update(ctx context.Context, request ExtensionRecordWrite) (ExtensionRecord, error) {
	return records.one(ctx, "records.update", extensionRecordRequest{Collection: request.Collection, ID: canonicalExtensionUUID(request.ID),
		Input: cloneRawFields(request.Input), IfMatch: request.IfMatch})
}

func (records ExtensionRecords) Delete(ctx context.Context, collection string, id uuid.UUID, ifMatch string) (ExtensionRecord, error) {
	return records.one(ctx, "records.delete", extensionRecordRequest{Collection: collection, ID: canonicalExtensionUUID(id), IfMatch: ifMatch})
}

func (records ExtensionRecords) Batch(ctx context.Context, operations []ExtensionBatchOperation) ([]ExtensionRecord, error) {
	converted := make([]extensionBatchOperation, len(operations))
	for index, operation := range operations {
		converted[index] = extensionBatchOperation{Operation: operation.Operation, Collection: operation.Collection,
			ID: canonicalExtensionUUID(operation.ID), Input: cloneRawFields(operation.Input), IdempotencyKey: operation.IdempotencyKey, IfMatch: operation.IfMatch}
	}
	var output struct {
		Results []extensionPublicRecord `json:"results"`
	}
	if err := extensionCall(ctx, records.host, "records.batch", extensionRecordRequest{Operations: converted}, &output); err != nil {
		return nil, err
	}
	result := make([]ExtensionRecord, len(output.Results))
	for index := range output.Results {
		result[index] = output.Results[index].public()
	}
	return result, nil
}

func (records ExtensionRecords) one(ctx context.Context, method string, request extensionRecordRequest) (ExtensionRecord, error) {
	var output extensionPublicRecord
	if err := extensionCall(ctx, records.host, method, request, &output); err != nil {
		return ExtensionRecord{}, err
	}
	return output.public(), nil
}

type extensionPublicRecord struct {
	Data          json.RawMessage `json:"data"`
	ETag          string          `json:"etag"`
	Version       int64           `json:"version"`
	EventID       string          `json:"eventId"`
	Replayed      bool            `json:"replayed"`
	Compatibility string          `json:"compatibility"`
}

func (record extensionPublicRecord) public() ExtensionRecord {
	return ExtensionRecord{Data: append(json.RawMessage(nil), record.Data...), ETag: record.ETag, Version: record.Version,
		EventID: extensionUUID(record.EventID), Replayed: record.Replayed, Compatibility: record.Compatibility}
}

type ExtensionMailMessage struct {
	To      string
	Subject string
	Text    string
	HTML    string
}

type ExtensionMail struct{ host *extensionHost }

func (mail ExtensionMail) Send(ctx context.Context, message ExtensionMailMessage) error {
	var output struct {
		Sent bool `json:"sent"`
	}
	return extensionCall(ctx, mail.host, "mail.send", extensionMailRequest{To: message.To, Subject: message.Subject, Text: message.Text, HTML: message.HTML}, &output)
}

type ExtensionFile struct {
	ID          uuid.UUID
	RecordID    uuid.UUID
	Field       string
	Filename    string
	ContentType string
	Size        int64
	SHA256      [32]byte
}

type ExtensionFileUpload struct {
	Collection  string
	RecordID    uuid.UUID
	Field       string
	Filename    string
	ContentType string
	Data        []byte
}

type ExtensionFiles struct{ host *extensionHost }

func (files ExtensionFiles) List(ctx context.Context, collection string, recordID uuid.UUID, field string) ([]ExtensionFile, error) {
	var output struct {
		Items []extensionPublicFile `json:"items"`
	}
	err := extensionCall(ctx, files.host, "files.list", extensionFileRequest{Collection: collection, RecordID: canonicalExtensionUUID(recordID), Field: field}, &output)
	result := make([]ExtensionFile, len(output.Items))
	for index := range output.Items {
		result[index] = output.Items[index].public()
	}
	return result, err
}

func (files ExtensionFiles) Upload(ctx context.Context, request ExtensionFileUpload) (ExtensionFile, error) {
	digest := sha256.Sum256(request.Data)
	var output extensionPublicFile
	err := extensionCall(ctx, files.host, "files.upload", extensionFileRequest{Collection: request.Collection,
		RecordID: canonicalExtensionUUID(request.RecordID), Field: request.Field, Filename: request.Filename,
		ContentType: request.ContentType, DataBase64: base64.StdEncoding.EncodeToString(request.Data), SHA256: hex.EncodeToString(digest[:])}, &output)
	return output.public(), err
}

func (files ExtensionFiles) Delete(ctx context.Context, id uuid.UUID) error {
	var output struct {
		Deleted bool `json:"deleted"`
	}
	return extensionCall(ctx, files.host, "files.delete", extensionFileRequest{FileID: canonicalExtensionUUID(id)}, &output)
}

func (files ExtensionFiles) MintDownloadToken(ctx context.Context, id uuid.UUID, variant string) (string, error) {
	var output struct {
		Token string `json:"token"`
	}
	err := extensionCall(ctx, files.host, "files.token", extensionFileRequest{FileID: canonicalExtensionUUID(id), Variant: variant}, &output)
	return output.Token, err
}

func (files ExtensionFiles) Read(ctx context.Context, id uuid.UUID, variant, token string) (ExtensionFile, []byte, error) {
	var output extensionPublicFile
	if err := extensionCall(ctx, files.host, "files.read", extensionFileRequest{FileID: canonicalExtensionUUID(id), Variant: variant, Token: token}, &output); err != nil {
		return ExtensionFile{}, nil, err
	}
	data, err := base64.StdEncoding.DecodeString(output.DataBase64)
	return output.public(), data, err
}

type extensionPublicFile struct {
	ID          string `json:"id"`
	RecordID    string `json:"recordId"`
	Field       string `json:"field"`
	Filename    string `json:"filename"`
	ContentType string `json:"contentType"`
	Size        int64  `json:"size"`
	SHA256      string `json:"sha256"`
	DataBase64  string `json:"dataBase64"`
}

func (file extensionPublicFile) public() ExtensionFile {
	var digest [32]byte
	if decoded, err := hex.DecodeString(file.SHA256); err == nil && len(decoded) == len(digest) {
		copy(digest[:], decoded)
	}
	return ExtensionFile{ID: extensionUUID(file.ID), RecordID: extensionUUID(file.RecordID), Field: file.Field,
		Filename: file.Filename, ContentType: file.ContentType, Size: file.Size, SHA256: digest}
}

func extensionCall(ctx context.Context, host *extensionHost, method string, input, output any) error {
	if host == nil {
		return errors.New("extension context is unavailable")
	}
	raw, err := json.Marshal(input)
	if err != nil {
		return fmt.Errorf("encode extension request: %w", err)
	}
	result, err := host.Call(ctx, method, raw)
	if err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(result))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(output); err != nil {
		return fmt.Errorf("decode extension result: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return errors.New("decode extension result: trailing JSON")
	}
	return nil
}

func canonicalExtensionUUID(id uuid.UUID) string {
	if id == uuid.Nil {
		return ""
	}
	return id.String()
}

func extensionUUID(value string) uuid.UUID {
	id, _ := uuid.Parse(value)
	return id
}

func cloneRawFields(input map[string]json.RawMessage) map[string]json.RawMessage {
	if input == nil {
		return nil
	}
	result := make(map[string]json.RawMessage, len(input))
	for name, value := range input {
		result[name] = append(json.RawMessage(nil), value...)
	}
	return result
}
