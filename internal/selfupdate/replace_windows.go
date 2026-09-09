//go:build windows

package selfupdate

import (
	"errors"
	"os"

	"golang.org/x/sys/windows"
)

func acquireLock(file *os.File) error {
	return acquireExclusiveExecutableLock(file)
}

func releaseLock(file *os.File) error {
	return windows.UnlockFileEx(windows.Handle(file.Fd()), 0, 1, 0, &windows.Overlapped{})
}

func acquireSharedExecutableLock(file *os.File) error {
	if err := windows.LockFileEx(windows.Handle(file.Fd()), windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, &windows.Overlapped{}); err != nil {
		return ErrReplaceBusy
	}
	return nil
}

func acquireExclusiveExecutableLock(file *os.File) error {
	if err := windows.LockFileEx(windows.Handle(file.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, &windows.Overlapped{}); err != nil {
		return ErrReplaceBusy
	}
	return nil
}

func closeExecutableLock(file *os.File) error { return errors.Join(releaseLock(file), file.Close()) }

// replaceFile is used by the post-exit Windows helper. Apply always makes and
// verifies a separate original backup before calling it, so a failed move
// leaves a verified rollback source and journal.
func replaceFile(source, target string) error {
	from, err := windows.UTF16PtrFromString(source)
	if err != nil {
		return err
	}
	to, err := windows.UTF16PtrFromString(target)
	if err != nil {
		return err
	}
	return windows.MoveFileEx(from, to, windows.MOVEFILE_REPLACE_EXISTING|windows.MOVEFILE_WRITE_THROUGH)
}

func syncDirectory(string) error { return nil }
