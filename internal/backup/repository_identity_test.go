package backup

import (
	"bytes"
	"context"
	"io"
	"testing"
)

type identityRepository string

func (r identityRepository) RepositoryIdentity() string                    { return string(r) }
func (identityRepository) Publish(context.Context, Entry, io.Reader) error { return nil }
func (identityRepository) Open(context.Context, string) (io.ReadCloser, error) {
	return io.NopCloser(bytes.NewReader(nil)), nil
}
func (identityRepository) List(context.Context) ([]Entry, error)      { return nil, nil }
func (identityRepository) Get(context.Context, string) (Entry, error) { return Entry{}, ErrNotFound }
func (identityRepository) Delete(context.Context, string) error       { return nil }

func TestRepositoryIdentityPreventsNamespaceChange(t *testing.T) {
	directory := t.TempDir()
	if err := ensureRepositoryIdentity(directory, identityRepository("s3:first")); err != nil {
		t.Fatal(err)
	}
	if err := ensureRepositoryIdentity(directory, identityRepository("s3:first")); err != nil {
		t.Fatal(err)
	}
	if err := ensureRepositoryIdentity(directory, identityRepository("s3:second")); err == nil {
		t.Fatal("repository namespace change accepted")
	}
}
