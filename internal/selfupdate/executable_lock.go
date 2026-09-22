package selfupdate

import (
	"os"
	"path/filepath"
)

// ServeLock is held for the complete lifetime of `pgbase serve`. Unix ties it
// to the executable inode. Windows uses the executable until an updater
// atomically establishes the adjacent stable coordination file needed across
// executable rename.
type ServeLock struct{ file *os.File }

// AcquireServeLock canonicalizes one executable path and takes a nonblocking
// shared advisory lock. The caller keeps the returned value alive through
// App.Run and closes it after App.Close.
func AcquireServeLock(executable string) (*ServeLock, error) {
	canonical, err := validatedExecutable(executable)
	if err != nil {
		return nil, err
	}
	file, err := acquireServeExecutableLock(canonical)
	if err != nil {
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
	canonical, err := validatedExecutable(executable)
	if err != nil {
		return nil, err
	}
	return acquireUpdateExecutableLockFile(canonical)
}

func validatedExecutable(executable string) (string, error) {
	if !filepath.IsAbs(executable) {
		return "", ErrInvalidInput
	}
	canonical, err := filepath.EvalSymlinks(executable)
	if err != nil {
		return "", ErrInvalidInput
	}
	if err = ensureRegular(canonical); err != nil {
		return "", err
	}
	return canonical, nil
}
