package pgbase

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/uuid"
	"github.com/yakisoba0728/PgBase/internal/filestore"
)

func (host *extensionHost) callLocalCreateThumb(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	if host.bridge.compatibility == nil || host.bridge.files == nil {
		return nil, extensionError("thumbnail_unavailable", errors.New("local thumbnail service unavailable"))
	}
	var request struct {
		OriginalPath string `json:"originalPath"`
		ThumbPath    string `json:"thumbPath"`
		ThumbSize    string `json:"thumbSize"`
	}
	if err := decodeExtensionInput(raw, &request); err != nil {
		return nil, err
	}
	source, err := host.bridge.compatibility.safePath(request.OriginalPath, false)
	if err != nil {
		return nil, extensionError("thumbnail_failed", err)
	}
	destination, err := host.bridge.compatibility.safePath(request.ThumbPath, true)
	if err != nil {
		return nil, extensionError("thumbnail_failed", err)
	}
	input, err := os.Open(source)
	if err != nil {
		return nil, extensionError("thumbnail_failed", err)
	}
	defer input.Close()
	probe := make([]byte, 512)
	n, readErr := io.ReadFull(input, probe)
	if readErr != nil && !errors.Is(readErr, io.ErrUnexpectedEOF) && !errors.Is(readErr, io.EOF) {
		return nil, extensionError("thumbnail_failed", readErr)
	}
	if _, err := input.Seek(0, io.SeekStart); err != nil {
		return nil, extensionError("thumbnail_failed", err)
	}
	contentType := strings.TrimSpace(strings.Split(http.DetectContentType(probe[:n]), ";")[0])
	data, err := host.bridge.files.RenderThumbnailReader(input, contentType, request.ThumbSize)
	if err != nil {
		return nil, extensionError("thumbnail_failed", err)
	}
	temporary, err := os.CreateTemp(filepath.Dir(destination), ".pgbase-thumb-*")
	if err != nil {
		return nil, extensionError("thumbnail_failed", err)
	}
	temporaryName := temporary.Name()
	defer os.Remove(temporaryName)
	if err := temporary.Chmod(0o600); err == nil {
		var written int
		written, err = temporary.Write(data)
		if err == nil && written != len(data) {
			err = io.ErrShortWrite
		}
	}
	if closeErr := temporary.Close(); err == nil {
		err = closeErr
	}
	if err == nil {
		err = os.Rename(temporaryName, destination)
	}
	if err != nil {
		return nil, extensionError("thumbnail_failed", err)
	}
	return json.Marshal(map[string]any{"created": true, "size": len(data)})
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
