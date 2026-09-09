package backup

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
)

type LocalRepository struct{ directory string }

func NewLocalRepository(directory string) *LocalRepository {
	return &LocalRepository{directory: directory}
}
func (r *LocalRepository) RepositoryIdentity() string { return "local" }
func (r *LocalRepository) archive(id string) string   { return filepath.Join(r.directory, id+".tar.gz") }
func (r *LocalRepository) metadata(id string) string  { return filepath.Join(r.directory, id+".json") }

func (r *LocalRepository) Publish(ctx context.Context, entry Entry, source io.Reader) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(r.directory, ".archive-")
	if err != nil {
		return err
	}
	name := temporary.Name()
	defer func() { _ = os.Remove(name) }()
	_, copyErr := io.Copy(temporary, source)
	err = errors.Join(copyErr, temporary.Sync(), temporary.Close())
	if err != nil {
		return err
	}
	if err = os.Rename(name, r.archive(entry.ID)); err != nil {
		return err
	}
	if err = writeAtomic(r.metadata(entry.ID), entry); err != nil {
		_ = os.Remove(r.archive(entry.ID))
		return err
	}
	return nil
}
func (r *LocalRepository) Open(ctx context.Context, id string) (io.ReadCloser, error) {
	if !validID(id) {
		return nil, ErrNotFound
	}
	file, err := os.Open(r.archive(id))
	if os.IsNotExist(err) {
		return nil, ErrNotFound
	}
	return file, err
}
func (r *LocalRepository) Get(ctx context.Context, id string) (Entry, error) {
	if !validID(id) {
		return Entry{}, ErrNotFound
	}
	raw, err := os.ReadFile(r.metadata(id))
	if os.IsNotExist(err) {
		return Entry{}, ErrNotFound
	}
	if err != nil {
		return Entry{}, err
	}
	var entry Entry
	if json.Unmarshal(raw, &entry) != nil || entry.ID != id {
		return Entry{}, ErrNotFound
	}
	if _, err = os.Stat(r.archive(id)); os.IsNotExist(err) {
		return Entry{}, ErrNotFound
	}
	return entry, err
}
func (r *LocalRepository) List(ctx context.Context) ([]Entry, error) {
	paths, err := filepath.Glob(filepath.Join(r.directory, "*.json"))
	if err != nil {
		return nil, err
	}
	entries := []Entry{}
	for _, path := range paths {
		var entry Entry
		raw, readErr := os.ReadFile(path)
		if readErr != nil {
			return nil, readErr
		}
		if json.Unmarshal(raw, &entry) != nil || filepath.Base(path) != entry.ID+".json" {
			continue
		}
		if _, statErr := os.Stat(r.archive(entry.ID)); statErr == nil {
			entries = append(entries, entry)
		}
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].CreatedAt.After(entries[j].CreatedAt) })
	return entries, nil
}
func (r *LocalRepository) Delete(ctx context.Context, id string) error {
	if !validID(id) {
		return ErrNotFound
	}
	if err := os.Remove(r.archive(id)); os.IsNotExist(err) {
		return ErrNotFound
	} else if err != nil {
		return err
	}
	return os.Remove(r.metadata(id))
}
func (r *LocalRepository) Discard(_ context.Context, id string) error {
	_ = os.Remove(r.metadata(id))
	_ = os.Remove(r.archive(id))
	return nil
}
