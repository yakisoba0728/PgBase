//go:build windows

package selfupdate

import (
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/sys/windows"
)

// WindowsHelperRequest is serialized only by a future CLI handoff after the
// parent updater exits. It deliberately carries basenames resolved by Apply,
// never a manifest URL or a key.
type WindowsHelperRequest struct {
	ParentPID uint32
	StateDir  string
	Target    string
	Candidate string
}

// RunWindowsHelper waits for the parent process, reacquires the installation
// lock, and publishes the already-verified candidate. It is actual supported
// Windows code; native execution remains unavailable on this Darwin host.
func RunWindowsHelper(request WindowsHelperRequest) error {
	if request.ParentPID == 0 || !filepath.IsAbs(request.StateDir) || !filepath.IsAbs(request.Target) || filepath.Base(request.Candidate) != request.Candidate {
		return ErrInvalidInput
	}
	process, err := windows.OpenProcess(windows.SYNCHRONIZE, false, request.ParentPID)
	if err == nil {
		defer windows.CloseHandle(process)
		if status, waitErr := windows.WaitForSingleObject(process, windows.INFINITE); waitErr != nil || status != windows.WAIT_OBJECT_0 {
			return fmt.Errorf("%w: wait parent", ErrReplaceFailed)
		}
	}
	lock, err := os.OpenFile(filepath.Join(request.StateDir, "lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return ErrReplaceFailed
	}
	defer lock.Close()
	if err = acquireLock(lock); err != nil {
		return err
	}
	defer releaseLock(lock)
	return replaceFile(filepath.Join(filepath.Dir(request.Target), request.Candidate), request.Target)
}
