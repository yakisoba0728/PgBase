package pgbase

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/yakisoba0728/PgBase/internal/filestore"
	"github.com/yakisoba0728/PgBase/internal/recordops"
)

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
