//go:build windows

package selfupdate

import (
	"os"

	"golang.org/x/sys/windows"
)

func acquireLock(file *os.File) error {
	handle := windows.Handle(file.Fd())
	if err := windows.LockFileEx(handle, windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, &windows.Overlapped{}); err != nil {
		return ErrReplaceBusy
	}
	return nil
}

func releaseLock(file *os.File) error {
	return windows.UnlockFileEx(windows.Handle(file.Fd()), 0, 1, 0, &windows.Overlapped{})
}

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
