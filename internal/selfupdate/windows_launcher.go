//go:build windows

package selfupdate

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
)

// LaunchWindowsHelper starts a fresh copy of the executable. That copy waits
// for this updater process to exit, then completes only the verified journal
// transition through the private helper command.
func LaunchWindowsHelper(request WindowsHelperRequest) error {
	raw, err := json.Marshal(request)
	if err != nil {
		return ErrReplaceFailed
	}
	executable, err := os.Executable()
	if err != nil {
		return ErrReplaceFailed
	}
	mode, err := executableMode(executable)
	if err != nil {
		return ErrReplaceFailed
	}
	helper, err := os.CreateTemp(request.StateDir, "helper-*.exe")
	if err != nil {
		return ErrReplaceFailed
	}
	helperPath := helper.Name()
	if err = helper.Close(); err != nil {
		_ = os.Remove(helperPath)
		return ErrReplaceFailed
	}
	if err = os.Remove(helperPath); err != nil {
		return ErrReplaceFailed
	}
	if err = copyRegular(executable, helperPath, mode); err != nil {
		_ = os.Remove(helperPath)
		return ErrReplaceFailed
	}
	if err = syncDirectory(filepath.Dir(helperPath)); err != nil {
		_ = os.Remove(helperPath)
		return ErrReplaceFailed
	}
	if err = exec.Command(helperPath, "update", "_windows_helper", base64.RawURLEncoding.EncodeToString(raw)).Start(); err != nil {
		_ = os.Remove(helperPath)
		return ErrReplaceFailed
	}
	return nil
}

func executableMode(path string) (os.FileMode, error) {
	_, mode, err := fileDigestAndMode(path)
	return mode.Perm(), err
}

// RunWindowsHelperArgument decodes the process-local helper request.
func RunWindowsHelperArgument(encoded string) error {
	raw, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return ErrInvalidInput
	}
	var request WindowsHelperRequest
	if err = json.Unmarshal(raw, &request); err != nil {
		return ErrInvalidInput
	}
	return RunWindowsHelper(request)
}
