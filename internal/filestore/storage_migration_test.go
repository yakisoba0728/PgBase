package filestore

import (
	"bytes"
	"context"
	"io"
	"sync"
	"testing"
)

type migrationMemoryStore struct {
	mu     sync.Mutex
	values map[string][]byte
}

func (s *migrationMemoryStore) Put(_ context.Context, key string, body io.Reader) error {
	value, err := io.ReadAll(body)
	if err == nil {
		s.mu.Lock()
		s.values[key] = value
		s.mu.Unlock()
	}
	return err
}
func (s *migrationMemoryStore) Open(_ context.Context, key string) (io.ReadCloser, error) {
	s.mu.Lock()
	value, ok := s.values[key]
	s.mu.Unlock()
	if !ok {
		return nil, ErrNotFound
	}
	return io.NopCloser(bytes.NewReader(value)), nil
}
func (s *migrationMemoryStore) Delete(_ context.Context, key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.values[key]; !ok {
		return ErrNotFound
	}
	delete(s.values, key)
	return nil
}

func TestCopyVerifiedPreservesConflictingDestinationObject(t *testing.T) {
	source := &migrationMemoryStore{values: map[string][]byte{"objects/key": []byte("source")}}
	destination := &migrationMemoryStore{values: map[string][]byte{"objects/key": []byte("destination")}}
	if _, _, _, err := copyVerifiedWithIntent(context.Background(), source, destination, "objects/key", nil); err != ErrStorageConflict {
		t.Fatalf("copy error=%v", err)
	}
	reader, err := destination.Open(context.Background(), "objects/key")
	if err != nil {
		t.Fatal(err)
	}
	got, _ := io.ReadAll(reader)
	_ = reader.Close()
	if string(got) != "destination" {
		t.Fatalf("destination overwritten: %q", got)
	}
}

func TestCopyVerifiedReusesIdenticalDestinationObject(t *testing.T) {
	source := &migrationMemoryStore{values: map[string][]byte{"objects/key": []byte("same")}}
	destination := &migrationMemoryStore{values: map[string][]byte{"objects/key": []byte("same")}}
	size, _, _, err := copyVerifiedWithIntent(context.Background(), source, destination, "objects/key", nil)
	if err != nil || size != 4 {
		t.Fatalf("size=%d err=%v", size, err)
	}
}

func TestStorageNamespaceIgnoresTransportOptions(t *testing.T) {
	local := storageSnapshot{Driver: "local", LocalDirectory: "/files"}
	changed := local
	changed.Bucket, changed.Region, changed.Endpoint, changed.UsePathStyle = "unused", "unused", "https://unused", true
	if snapshotLocation(local) != snapshotLocation(changed) {
		t.Fatal("unused S3 options changed local namespace")
	}
	s3 := storageSnapshot{Driver: "s3", Bucket: "bucket", Endpoint: "https://storage"}
	changed = s3
	changed.Region, changed.LocalDirectory, changed.UsePathStyle, changed.Endpoint = "other-region", "/unused", true, "https://storage/"
	if snapshotLocation(s3) != snapshotLocation(changed) {
		t.Fatal("transport options changed S3 namespace")
	}
	changed.Bucket = "other-bucket"
	if snapshotLocation(s3) == snapshotLocation(changed) {
		t.Fatal("different S3 buckets share namespace")
	}
}
