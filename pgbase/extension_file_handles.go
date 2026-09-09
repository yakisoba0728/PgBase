package pgbase

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"

	"github.com/google/uuid"
	"github.com/webisnoteasy/PgBase/internal/filestore"
)

const (
	// A StorageController reader retains a shared transition lease until close.
	// PostgreSQL may queue a later shared lease behind a waiting exclusive
	// migration cleanup lock, so an invocation must consume one stream fully
	// before opening another one.
	maximumExtensionFileHandles = 1
	maximumExtensionFileChunk   = 64 << 10
	maximumExtensionFileBytes   = 3 << 20
)

type extensionFileHandle struct {
	file   filestore.File
	reader io.ReadCloser
}

func (host *extensionHost) hasActiveFileStream() bool {
	if host == nil {
		return false
	}
	host.fileMu.Lock()
	defer host.fileMu.Unlock()
	return len(host.fileHandles) != 0
}

func (host *extensionHost) openFileHandle(ctx context.Context, request extensionFileRequest) (json.RawMessage, error) {
	id, err := requiredUUID(request.FileID)
	if err != nil {
		return nil, extensionError("invalid_file_request", err)
	}
	handleID := uuid.New()
	host.fileMu.Lock()
	if len(host.fileHandles) >= maximumExtensionFileHandles {
		host.fileMu.Unlock()
		return nil, extensionError("file_handle_limit", errors.New("close the active file handle before opening another"))
	}
	if host.fileHandles == nil {
		host.fileHandles = make(map[uuid.UUID]extensionFileHandle)
	}
	// Reserve before touching the database or object store. This keeps a fifth
	// concurrent open from consuming backend capacity while it is guaranteed to
	// fail the per-invocation quota.
	host.fileHandles[handleID] = extensionFileHandle{}
	host.fileMu.Unlock()
	reserved := true
	defer func() {
		if reserved {
			host.fileMu.Lock()
			delete(host.fileHandles, handleID)
			host.fileMu.Unlock()
		}
	}()
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
	host.fileMu.Lock()
	if _, ok := host.fileHandles[handleID]; !ok {
		host.fileMu.Unlock()
		_ = reader.Close()
		return nil, extensionError("file_handle_not_found", errors.New("file handle was closed while opening"))
	}
	host.fileHandles[handleID] = extensionFileHandle{file: file, reader: reader}
	reserved = false
	host.fileMu.Unlock()
	result := publicFileResult(file)
	result.Handle = handleID.String()
	return json.Marshal(result)
}

func (host *extensionHost) readFileChunk(request extensionFileRequest) (json.RawMessage, error) {
	handleID, err := requiredUUID(request.Handle)
	if err != nil {
		return nil, extensionError("invalid_file_request", err)
	}
	if request.MaximumBytes < 0 || request.MaximumBytes > maximumExtensionFileChunk {
		return nil, extensionError("invalid_file_request", errors.New("maximumBytes must be between zero and 65536"))
	}
	maximum := request.MaximumBytes
	if maximum == 0 {
		maximum = maximumExtensionFileChunk
	}
	host.fileMu.Lock()
	defer host.fileMu.Unlock()
	handle, ok := host.fileHandles[handleID]
	if !ok || handle.reader == nil {
		return nil, extensionError("file_handle_not_found", errors.New("file handle is closed or unknown"))
	}
	remaining := int64(maximumExtensionFileBytes) - host.fileBytesRead
	if remaining <= 0 {
		return nil, extensionError("file_too_large", errors.New("file invocation byte limit reached"))
	}
	if int64(maximum) > remaining {
		maximum = int(remaining)
	}
	buffer := make([]byte, maximum)
	count, readErr := handle.reader.Read(buffer)
	host.fileBytesRead += int64(count)
	eof := errors.Is(readErr, io.EOF)
	if readErr != nil && !eof {
		_ = handle.reader.Close()
		delete(host.fileHandles, handleID)
		return nil, mapExtensionError(readErr)
	}
	return json.Marshal(extensionFileResult{Handle: handleID.String(), DataBase64: base64.StdEncoding.EncodeToString(buffer[:count]), EOF: eof})
}

func (host *extensionHost) closeFileHandle(request extensionFileRequest) (json.RawMessage, error) {
	handleID, err := requiredUUID(request.Handle)
	if err != nil {
		return nil, extensionError("invalid_file_request", err)
	}
	host.fileMu.Lock()
	defer host.fileMu.Unlock()
	handle, ok := host.fileHandles[handleID]
	if !ok || handle.reader == nil {
		return nil, extensionError("file_handle_not_found", errors.New("file handle is closed or unknown"))
	}
	delete(host.fileHandles, handleID)
	if err := handle.reader.Close(); err != nil {
		return nil, mapExtensionError(err)
	}
	return json.RawMessage(`{"closed":true}`), nil
}

func (host *extensionHost) Close() error {
	if host == nil {
		return nil
	}
	host.fileMu.Lock()
	defer host.fileMu.Unlock()
	var result error
	for id, handle := range host.fileHandles {
		if handle.reader != nil {
			result = errors.Join(result, handle.reader.Close())
		}
		delete(host.fileHandles, id)
	}
	return result
}
