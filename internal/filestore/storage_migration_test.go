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
	changed.Region, changed.LocalDirectory, changed.UsePathStyle, changed.Endpoint = "other-region", "/unused", true, "https://STORAGE.:443/"
	if snapshotLocation(s3) != snapshotLocation(changed) {
		t.Fatal("transport options changed S3 namespace")
	}
	changed.Bucket = "other-bucket"
	if snapshotLocation(s3) == snapshotLocation(changed) {
		t.Fatal("different S3 buckets share namespace")
	}
}

func TestStorageNamespacesMayOverlap(t *testing.T) {
	source := storageSnapshot{Driver: "s3", Bucket: "retired-files", Endpoint: "https://storage.example.invalid"}
	for _, test := range []struct {
		name string
		peer storageSnapshot
		want bool
	}{
		{"same bucket through equivalent address", storageSnapshot{Driver: "s3", Bucket: "retired-files", Endpoint: "https://STORAGE.EXAMPLE.INVALID:443/"}, true},
		{"same bucket through unrelated address", storageSnapshot{Driver: "s3", Bucket: "retired-files", Endpoint: "https://other.example.invalid"}, true},
		{"different bucket through unrelated address", storageSnapshot{Driver: "s3", Bucket: "other-files", Endpoint: "https://other.example.invalid"}, true},
		{"default endpoint may alias explicit address", storageSnapshot{Driver: "s3", Bucket: "other-files"}, true},
		{"different bucket within equivalent endpoint", storageSnapshot{Driver: "s3", Bucket: "other-files", Endpoint: "https://STORAGE.EXAMPLE.INVALID:443/"}, false},
		{"alternate casing of bucket name", storageSnapshot{Driver: "s3", Bucket: "RETIRED-FILES", Endpoint: source.Endpoint}, true},
		{"access point alias at the same endpoint", storageSnapshot{Driver: "s3", Bucket: "archive-s3alias", Endpoint: source.Endpoint}, true},
		{"access point ARN at the same endpoint", storageSnapshot{Driver: "s3", Bucket: "arn:aws:s3:us-east-1:000000000000:accesspoint/archive", Endpoint: source.Endpoint}, true},
		{"local storage is separate", storageSnapshot{Driver: "local", LocalDirectory: "/files"}, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := storageNamespacesMayOverlap(source, test.peer); got != test.want {
				t.Fatalf("storageNamespacesMayOverlap = %v, want %v", got, test.want)
			}
		})
	}
}
