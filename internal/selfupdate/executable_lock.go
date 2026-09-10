package selfupdate

import (
	"os"
	"path/filepath"
)

// ServeLock is held for the complete lifetime of `pgbase serve`. It is tied to
// the executable inode, not a writable sidecar path, so update apply can
// reject a running server before it stages or replaces that executable.
type ServeLock struct{ file *os.File }

// AcquireServeLock canonicalizes one executable path, opens that regular file
// read-only, and takes a nonblocking shared advisory lock. The caller keeps
// the returned value alive through App.Run and closes it after App.Close.
func AcquireServeLock(executable string) (*ServeLock, error) {
	file, err := openExecutable(executable)
	if err != nil {
		return nil, err
	}
	if err = acquireSharedExecutableLock(file); err != nil {
		_ = file.Close()
		return nil, err
	}
	return &ServeLock{file: file}, nil
}

// Close releases the server lifetime lock. It is safe to defer after a
// successful AcquireServeLock call.
func (l *ServeLock) Close() error {
	if l == nil || l.file == nil {
		return nil
	}
	file := l.file
	l.file = nil
	return closeExecutableLock(file)
}

func acquireUpdateExecutableLock(executable string) (*os.File, error) {
	file, err := openExecutable(executable)
	if err != nil {
		return nil, err
	}
	if err = acquireExclusiveExecutableLock(file); err != nil {
		_ = file.Close()
		return nil, err
	}
	return file, nil
}

func openExecutable(executable string) (*os.File, error) {
	if !filepath.IsAbs(executable) {
		return nil, ErrInvalidInput
	}
	canonical, err := filepath.EvalSymlinks(executable)
	if err != nil {
		return nil, ErrInvalidInput
	}
	if err = ensureRegular(canonical); err != nil {
		return nil, err
	}
	file, err := openExistingRegularFile(canonical, os.O_RDONLY)
	if err != nil {
		return nil, ErrReplaceFailed
	}
	return file, nil
}
