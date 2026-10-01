//go:build windows

package selfupdate

import (
	"errors"
	"os"
	"path/filepath"
	"unsafe"

	"golang.org/x/sys/windows"
)

func executableCoordinationPath(target string) string {
	return filepath.Join(filepath.Dir(target), "."+filepath.Base(target)+".pgbase-lock")
}

func openCoordinationFile(path string, create bool) (*os.File, error) {
	flags := os.O_RDONLY
	if create {
		flags = os.O_CREATE | os.O_RDWR
	}
	file, err := os.OpenFile(path, flags, 0600)
	if err != nil {
		return nil, err
	}
	opened, statErr := file.Stat()
	leaf, leafErr := os.Lstat(path)
	if statErr != nil || leafErr != nil || !opened.Mode().IsRegular() || leaf.Mode()&os.ModeSymlink != 0 || !os.SameFile(opened, leaf) || !validPrivateStateFile(file) {
		_ = file.Close()
		return nil, ErrInvalidInput
	}
	return file, nil
}

func acquireServeExecutableLock(target string) (*os.File, error) {
	coordination := executableCoordinationPath(target)
	if file, err := openCoordinationFile(coordination, false); err == nil {
		if err = acquireSharedExecutableLock(file); err != nil {
			_ = file.Close()
			return nil, err
		}
		return file, nil
	} else if !os.IsNotExist(err) {
		return nil, ErrInvalidInput
	}
	file, err := openExistingRegularFile(target, os.O_RDONLY)
	if err != nil {
		return nil, ErrReplaceFailed
	}
	if err = acquireSharedExecutableLock(file); err != nil {
		_ = file.Close()
		return nil, err
	}
	if _, err = os.Lstat(coordination); err == nil {
		_ = closeExecutableLock(file)
		return acquireServeExecutableLock(target)
	} else if !os.IsNotExist(err) {
		_ = closeExecutableLock(file)
		return nil, ErrInvalidInput
	}
	return file, nil
}

func acquireUpdateExecutableLockFile(target string) (*os.File, error) {
	targetLock, err := openExistingRegularFile(target, os.O_RDONLY)
	if err != nil {
		return nil, ErrReplaceFailed
	}
	if err = acquireExclusiveExecutableLock(targetLock); err != nil {
		_ = targetLock.Close()
		return nil, err
	}
	coordination, err := openCoordinationFile(executableCoordinationPath(target), true)
	if err != nil {
		_ = closeExecutableLock(targetLock)
		return nil, ErrReplaceFailed
	}
	if err = acquireExclusiveExecutableLock(coordination); err != nil {
		_ = coordination.Close()
		_ = closeExecutableLock(targetLock)
		return nil, err
	}
	if err = closeExecutableLock(targetLock); err != nil {
		_ = closeExecutableLock(coordination)
		return nil, ErrReplaceFailed
	}
	return coordination, nil
}

func acquireLock(file *os.File) error {
	return acquireExclusiveExecutableLock(file)
}

func releaseLock(file *os.File) error {
	return windows.UnlockFileEx(windows.Handle(file.Fd()), 0, 1, 0, executableLockOverlapped())
}

func acquireSharedExecutableLock(file *os.File) error {
	if err := windows.LockFileEx(windows.Handle(file.Fd()), windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, executableLockOverlapped()); err != nil {
		return ErrReplaceBusy
	}
	return nil
}

func acquireExclusiveExecutableLock(file *os.File) error {
	if err := windows.LockFileEx(windows.Handle(file.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, executableLockOverlapped()); err != nil {
		return ErrReplaceBusy
	}
	return nil
}

// Lock a byte far beyond any executable image. Windows loaders read the image
// itself, so locking byte zero prevents ProbeBinary and normal process startup.
// LockFileEx still arbitrates this shared/exclusive range between PgBase
// processes even though the range is beyond EOF.
func executableLockOverlapped() *windows.Overlapped {
	return &windows.Overlapped{Offset: ^uint32(0), OffsetHigh: ^uint32(0)}
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
	result, _, callErr := windows.NewLazySystemDLL("kernel32.dll").NewProc("ReplaceFileW").Call(
		uintptr(unsafe.Pointer(to)), uintptr(unsafe.Pointer(from)), 0, 0, 0, 0,
	)
	if result == 0 {
		// ReplaceFileW can also reject a valid replacement because of the
		// destination filesystem or its metadata. MoveFileEx provides the same
		// atomic name publication used for a first write and works on NTFS.
		if err := windows.MoveFileEx(from, to, windows.MOVEFILE_REPLACE_EXISTING|windows.MOVEFILE_WRITE_THROUGH); err == nil {
			return nil
		}
		return callErr
	}
	return nil
}

func syncDirectory(string) error { return nil }
