//go:build windows

package durablefile

import (
	"golang.org/x/sys/windows"
	"os"
)

// Rename durably publishes a file using the platform's write-through move.
func Rename(source, destination string) error {
	from, err := windows.UTF16PtrFromString(source)
	if err != nil {
		return err
	}
	to, err := windows.UTF16PtrFromString(destination)
	if err != nil {
		return err
	}
	return windows.MoveFileEx(from, to, windows.MOVEFILE_REPLACE_EXISTING|windows.MOVEFILE_WRITE_THROUGH)
}

// MkdirAll creates the destination directories before the write-through move.
func MkdirAll(path string, mode os.FileMode) error { return os.MkdirAll(path, mode) }
