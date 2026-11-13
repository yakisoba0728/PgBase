package filestore

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"testing"
)

func TestUploadByteValidationRejectsInvalidStoredContent(t *testing.T) {
	for _, test := range []struct {
		name, body, contentType string
		size                    int64
		wrongHash               bool
		want                    error
	}{
		{"valid", "text", "text/plain", 4, false, nil},
		{"size", "text", "text/plain", 3, false, ErrInvalid},
		{"digest", "text", "text/plain", 4, true, ErrInvalid},
		{"type", "text", "image/png", 4, false, ErrInvalid},
		{"limit", "oversized", "text/plain", 9, false, ErrTooLarge},
	} {
		t.Run(test.name, func(t *testing.T) {
			objects := &migrationMemoryStore{values: make(map[string][]byte)}
			service := &Service{objects: objects, options: Options{MaximumBytes: 8}}
			digest := sha256.Sum256([]byte(test.body))
			if test.wrongHash {
				digest[0] ^= 1
			}
			err := service.writeVerifiedUpload(context.Background(), File{ObjectKey: "objects/test", Size: test.size, SHA256: digest, ContentType: test.contentType}, bytes.NewBufferString(test.body))
			if !errors.Is(err, test.want) {
				t.Fatalf("validation = %v, want %v", err, test.want)
			}
		})
	}
}
