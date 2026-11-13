package backup

import (
	"bytes"
	"context"
	"errors"
	"github.com/yakisoba0728/PgBase/internal/validation"
	"io"
	"testing"
)

type deleteFailRepository struct {
	entry Entry
	body  []byte
}

func (r *deleteFailRepository) Publish(_ context.Context, entry Entry, body io.Reader) error {
	r.entry = entry
	r.body, _ = io.ReadAll(body)
	return nil
}
func (r *deleteFailRepository) Open(context.Context, string) (io.ReadCloser, error) {
	return io.NopCloser(bytes.NewReader(r.body)), nil
}
func (r *deleteFailRepository) List(context.Context) ([]Entry, error) { return []Entry{r.entry}, nil }
func (r *deleteFailRepository) Get(context.Context, string) (Entry, error) {
	return r.entry, nil
}
func (*deleteFailRepository) Delete(context.Context, string) error {
	return errors.New("delete failed")
}
func (*deleteFailRepository) Discard(context.Context, string) error { return nil }

func TestProbeRepositoryRequiresSuccessfulDelete(t *testing.T) {
	repository := &deleteFailRepository{}
	if err := probeRepository(context.Background(), repository); err == nil || err.Error() != "delete failed" {
		t.Fatalf("probe delete failure=%v", err)
	}
	if repository.entry.SHA256 == "" {
		t.Fatal("probe receipt omitted SHA-256")
	}
}

func TestBackupStorageValidationIdentifiesInvalidFields(t *testing.T) {
	for _, test := range []struct {
		config StorageConfig
		field  string
	}{
		{StorageConfig{Driver: "local", Retention: -1}, "retention"},
		{StorageConfig{Driver: "s3", Bucket: "bucket", Region: "region", Endpoint: "bad-address"}, "endpoint"},
		{StorageConfig{Driver: "s3", Bucket: "bucket", Region: "region", AccessKey: "key"}, "secretKey"},
	} {
		err := validateStorageConfig(test.config)
		var invalid *validation.Error
		if !errors.Is(err, ErrInvalidStorage) || !errors.As(err, &invalid) || invalid.Details[0].Field != test.field {
			t.Fatalf("field=%s error=%v", test.field, err)
		}
	}
}
