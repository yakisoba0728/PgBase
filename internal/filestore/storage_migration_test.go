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
	if _, _, _, err := copyVerified(context.Background(), source, destination, "objects/key"); err != ErrStorageConflict {
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
	size, _, _, err := copyVerified(context.Background(), source, destination, "objects/key")
	if err != nil || size != 4 {
		t.Fatalf("size=%d err=%v", size, err)
	}
}
