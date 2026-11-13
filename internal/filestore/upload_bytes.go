package filestore

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"errors"
	"io"
)

// writeVerifiedUpload owns byte validation for both pre-record staging and
// direct uploads. The caller owns durable cleanup and metadata publication.
func (s *Service) writeVerifiedUpload(ctx context.Context, file File, body io.Reader) error {
	counted := &countingReader{reader: body, maximum: s.options.MaximumBytes}
	hash := sha256.New()
	if err := s.objects.Put(ctx, file.ObjectKey, io.TeeReader(counted, hash)); err != nil {
		if errors.Is(counted.err, ErrTooLarge) {
			return ErrTooLarge
		}
		return err
	}
	if counted.count != file.Size || !hmac.Equal(hash.Sum(nil), file.SHA256[:]) {
		return ErrInvalid
	}
	return s.validateStoredType(ctx, file)
}
