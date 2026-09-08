//go:build integration

package filestore

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"testing"

	"github.com/google/uuid"
)

func TestS3StoreAgainstLocalMinIO(t *testing.T) {
	endpoint := os.Getenv("PGBASE_TEST_S3_ENDPOINT")
	if endpoint == "" {
		t.Skip("PGBASE_TEST_S3_ENDPOINT is not set; start tests/filestore/compose.yaml")
	}
	store, err := NewS3Store(context.Background(), S3Config{
		Bucket: "pgbase-test", Region: "us-east-1", Endpoint: endpoint,
		AccessKey: "pgbase-test-access", SecretKey: "pgbase-test-secret-0123456789",
		UsePathStyle: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	key := "objects/" + uuid.NewString()
	if err := store.Put(context.Background(), key, bytes.NewBufferString("minio object")); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Delete(context.Background(), key) })
	reader, err := store.Open(context.Background(), key)
	if err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(reader)
	_ = reader.Close()
	if err != nil || string(data) != "minio object" {
		t.Fatalf("S3 object = %q, %v", data, err)
	}
	if err := store.Delete(context.Background(), key); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Open(context.Background(), key); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Open deleted object = %v, want ErrNotFound", err)
	}
}
