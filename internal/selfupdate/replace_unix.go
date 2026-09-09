//go:build !windows

package selfupdate

import (
	"errors"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

func acquireLock(file *os.File) error {
	if err := unix.Flock(int(file.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		return ErrReplaceBusy
	}
	return nil
}

func releaseLock(file *os.File) error { return unix.Flock(int(file.Fd()), unix.LOCK_UN) }

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
