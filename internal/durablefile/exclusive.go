// Package durablefile writes files without exposing a partial destination.
package durablefile

import "os"

var openExclusiveFile = func(path string, mode os.FileMode) (*os.File, error) {
	return os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
}

var syncExclusiveFile = func(file *os.File) error { return file.Sync() }
var closeExclusiveFile = func(file *os.File) error { return file.Close() }

// WriteExclusive creates path once, writes and durably syncs its contents, and
// removes the newly created path when any write, sync, or close step fails.
// failure receives the stage name and preserves a caller's error vocabulary.
func WriteExclusive(path string, mode os.FileMode, write func(*os.File) error, failure func(string, error) error) error {
	file, err := openExclusiveFile(path, mode)
	if err != nil {
		return failure("create", err)
	}
	completed := false
	defer func() {
		if !completed {
			_ = os.Remove(path)
		}
	}()
	if err := write(file); err != nil {
		_ = closeExclusiveFile(file)
		return failure("write", err)
	}
	if err := syncExclusiveFile(file); err != nil {
		_ = closeExclusiveFile(file)
		return failure("sync", err)
	}
	if err := closeExclusiveFile(file); err != nil {
		return failure("close", err)
	}
	completed = true
	return nil
}
