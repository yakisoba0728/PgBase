//go:build !windows

package durablefile

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
)

// Rename publishes a synced file and durably records the directory entry.
// An error after rename does not imply that the destination is absent.
func Rename(source, destination string) error {
	if err := os.Rename(source, destination); err != nil {
		return err
	}
	return SyncDirectory(filepath.Dir(destination))
}

// SyncDirectory persists changes to directory entries.
func SyncDirectory(path string) error {
	directory, err := os.Open(path)
	if err != nil {
		return err
	}
	return errors.Join(directory.Sync(), directory.Close())
}

// MkdirAll persists each new directory entry before it becomes an ancestor of
// a published file. This also syncs an existing entry after a failed prior call.
func MkdirAll(path string, mode os.FileMode) error {
	info, err := os.Stat(path)
	if err == nil {
		if !info.IsDir() {
			return &os.PathError{Op: "mkdir", Path: path, Err: syscall.ENOTDIR}
		}
		return SyncDirectory(filepath.Dir(path))
	}
	if !os.IsNotExist(err) {
		return err
	}
	parent := filepath.Dir(path)
	if err := MkdirAll(parent, mode); err != nil {
		return err
	}
	if err := os.Mkdir(path, mode); err != nil && !os.IsExist(err) {
		return err
	}
	return SyncDirectory(parent)
}
