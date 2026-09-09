//go:build windows

package selfupdate

import (
	"context"
	"fmt"
	"path/filepath"

	"golang.org/x/sys/windows"
)

// RunWindowsHelper waits for the parent process, reacquires the installation
// and executable locks, then finishes the already journalled replacement.
// Native Windows execution remains unavailable on this Darwin host.
func RunWindowsHelper(request WindowsHelperRequest) error {
	if request.ParentPID == 0 || !filepath.IsAbs(request.StateDir) || !filepath.IsAbs(request.Target) || filepath.Base(request.Candidate) != request.Candidate {
		return ErrInvalidInput
	}
	process, err := windows.OpenProcess(windows.SYNCHRONIZE, false, request.ParentPID)
	if err == nil {
		defer func() { _ = windows.CloseHandle(process) }()
		if status, waitErr := windows.WaitForSingleObject(process, windows.INFINITE); waitErr != nil || status != windows.WAIT_OBJECT_0 {
			return fmt.Errorf("%w: wait parent", ErrReplaceFailed)
		}
	} else if err != windows.ERROR_INVALID_PARAMETER {
		return fmt.Errorf("%w: inspect parent", ErrReplaceFailed)
	}
	state, target, lock, err := prepareInstallation(request.Target, request.StateDir)
	if err != nil {
		return ErrReplaceFailed
	}
	if target != request.Target || state != request.StateDir {
		_ = lock.Close()
		return ErrInvalidInput
	}
	defer func() { _ = lock.Close() }()
	if err = acquireLock(lock); err != nil {
		return err
	}
	defer func() { _ = releaseLock(lock) }()
	executableLock, err := acquireUpdateExecutableLock(target)
	if err != nil {
		return err
	}
	defer func() { _ = closeExecutableLock(executableLock) }()
	entry, err := loadJournal(state)
	if err != nil || entry.Phase != "replacement_started" || entry.Target != filepath.Base(target) || entry.Candidate != request.Candidate {
		return ErrRecoveryRequired
	}
	candidate := filepath.Join(filepath.Dir(target), entry.Candidate)
	digest, _, err := fileDigestAndMode(candidate)
	if err != nil || digest != entry.NewSHA256 {
		return ErrRecoveryRequired
	}
	if build, probeErr := ProbeBinary(context.Background(), candidate); probeErr != nil || !sameBuild(build, entry.New) {
		return ErrProbeFailed
	}
	if err = replaceFile(candidate, target); err != nil {
		return ErrReplaceFailed
	}
	entry.Phase = "replacement_visible"
	if err = writeJournal(state, entry); err != nil {
		return err
	}
	if digest, _, digestErr := fileDigestAndMode(target); digestErr != nil || digest != entry.NewSHA256 {
		if _, rollbackErr := rollbackLocked(context.Background(), state, target, entry, ProbeBinary); rollbackErr != nil {
			return rollbackErr
		}
		return ErrProbeFailed
	}
	if build, probeErr := ProbeBinary(context.Background(), target); probeErr != nil || !sameBuild(build, entry.New) {
		if _, rollbackErr := rollbackLocked(context.Background(), state, target, entry, ProbeBinary); rollbackErr != nil {
			return rollbackErr
		}
		return ErrProbeFailed
	}
	entry.Phase = "committed"
	if err = writeJournal(state, entry); err != nil {
		return err
	}
	return nil
}
