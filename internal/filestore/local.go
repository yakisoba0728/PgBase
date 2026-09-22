package filestore

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
)

var objectKeyPattern = regexp.MustCompile(`^(?:(?:objects|thumbnails)/[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}|extensions/[0-9a-f]{64}/[0-9a-f]{64}(?:/[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12})?)$`)

type LocalStore struct{ root string }

func NewLocalStore(root string) (*LocalStore, error) {
	if root == "" {
		return nil, ErrInvalid
	}
	absolute, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("resolve file root: %w", err)
	}
	if err := os.MkdirAll(absolute, 0o700); err != nil {
		return nil, fmt.Errorf("create file root: %w", err)
	}
	return &LocalStore{root: absolute}, nil
}

func (s *LocalStore) path(key string) (string, error) {
	if s == nil || !objectKeyPattern.MatchString(key) {
		return "", ErrInvalid
	}
	return filepath.Join(s.root, filepath.FromSlash(key)), nil
}

func (s *LocalStore) Put(ctx context.Context, key string, source io.Reader) error {
	path, err := s.path(key)
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create object directory: %w", err)
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), ".upload-*")
	if err != nil {
		return fmt.Errorf("create temporary object: %w", err)
	}
	temporaryPath := temporary.Name()
	defer func() { _ = os.Remove(temporaryPath) }()
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return err
	}
	_, copyErr := io.Copy(temporary, &contextReader{ctx: ctx, reader: source})
	closeErr := temporary.Close()
	if copyErr != nil {
		return fmt.Errorf("write object: %w", copyErr)
	}
	if closeErr != nil {
		return fmt.Errorf("close object: %w", closeErr)
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return fmt.Errorf("publish object: %w", err)
	}
	return nil
}

func (s *LocalStore) Open(ctx context.Context, key string) (io.ReadCloser, error) {
	path, err := s.path(key)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("open object: %w", err)
	}
	return file, nil
}

func (s *LocalStore) OpenRange(ctx context.Context, key string, requested ByteRange) (RangeRead, error) {
	path, err := s.path(key)
	if err != nil {
		return RangeRead{}, err
	}
	if err := ctx.Err(); err != nil {
		return RangeRead{}, err
	}
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return RangeRead{}, ErrNotFound
	}
	if err != nil {
		return RangeRead{}, fmt.Errorf("open object range: %w", err)
	}
	info, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return RangeRead{}, fmt.Errorf("stat object range: %w", err)
	}
	start, end, err := ResolveByteRange(requested, info.Size())
	if err != nil {
		_ = file.Close()
		return RangeRead{Size: info.Size()}, err
	}
	if _, err := file.Seek(start, io.SeekStart); err != nil {
		_ = file.Close()
		return RangeRead{}, err
	}
	return RangeRead{Body: &limitedReadCloser{Reader: io.LimitReader(file, end-start+1), Closer: file}, Start: start, End: end, Size: info.Size()}, nil
}

type limitedReadCloser struct {
	io.Reader
	io.Closer
}

func (s *LocalStore) Delete(ctx context.Context, key string) error {
	path, err := s.path(key)
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("delete object: %w", err)
	}
	return nil
}

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r *contextReader) Read(buffer []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(buffer)
}
