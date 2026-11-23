package pgbase

import (
	"errors"
	"fmt"
	"testing"

	"github.com/yakisoba0728/PgBase/internal/filestore"
)

func TestExtensionThumbnailBusyIsDistinct(t *testing.T) {
	busy := fmt.Errorf("render: %w", filestore.ErrImageBusy)
	for _, test := range []struct {
		name string
		err  error
		code string
	}{
		{name: "thumbnail busy", err: thumbnailError(busy), code: "file_busy"},
		{name: "thumbnail failed", err: thumbnailError(filestore.ErrInvalid), code: "thumbnail_failed"},
		{name: "file operation busy", err: mapExtensionError(busy), code: "file_busy"},
	} {
		t.Run(test.name, func(t *testing.T) {
			var coded *extensionOperationError
			if !errors.As(test.err, &coded) || coded.CodeValue() != test.code {
				t.Fatalf("error %v code, want %q", test.err, test.code)
			}
		})
	}
}
