//go:build !windows

package selfupdate

import (
	"errors"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

func acquireLock(file *os.File) error {
	return acquireExclusiveExecutableLock(file)
}

func acquireServeExecutableLock(path string) (*os.File, error) {
	file, err := openExistingRegularFile(path, os.O_RDONLY)
	if err != nil {
		return nil, ErrReplaceFailed
	}
	if err = acquireSharedExecutableLock(file); err != nil {
		_ = file.Close()
		return nil, err
	}
	return file, nil
}

func acquireUpdateExecutableLockFile(path string) (*os.File, error) {
	file, err := openExistingRegularFile(path, os.O_RDONLY)
	if err != nil {
		return nil, ErrReplaceFailed
	}
	if err = acquireExclusiveExecutableLock(file); err != nil {
		_ = file.Close()
		return nil, err
	}
	return file, nil
}

func releaseLock(file *os.File) error { return unix.Flock(int(file.Fd()), unix.LOCK_UN) }

func acquireSharedExecutableLock(file *os.File) error {
	if err := unix.Flock(int(file.Fd()), unix.LOCK_SH|unix.LOCK_NB); err != nil {
		return ErrReplaceBusy
	}
	return nil
}

func acquireExclusiveExecutableLock(file *os.File) error {
	if err := unix.Flock(int(file.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		return ErrReplaceBusy
	}
	return nil
}

func closeExecutableLock(file *os.File) error {
	return errors.Join(releaseLock(file), file.Close())
}

func replaceFile(source, target string) error {
	if err := os.Rename(source, target); err != nil {
		return err
	}
	return syncDirectory(filepath.Dir(target))
}

func syncDirectory(directory string) error {
	file, err := os.Open(directory)
	if err != nil {
		return err
	}
	return errors.Join(file.Sync(), file.Close())
}
