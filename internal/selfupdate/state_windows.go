//go:build windows

package selfupdate

import "os"

// Windows ACL ownership is established by the user's profile and inherited by
// the newly created state directory. Go's FileInfo does not expose an ACL
// owner portably, so reject writable-by-group/other mode bits here; the helper
// also validates every opened regular descriptor before use.
func validPrivateStateDirectory(info os.FileInfo) bool {
	return info.Mode().Perm()&0o022 == 0
}

func validInstallationTarget(info os.FileInfo) bool {
	return info.Mode().IsRegular() && info.Mode().Perm()&0o022 == 0
}

func validInstallationDirectory(info os.FileInfo) bool {
	return info.IsDir() && info.Mode().Perm()&0o022 == 0
}

func validPrivateStateFile(file *os.File) bool {
	info, err := file.Stat()
	return err == nil && info.Mode().IsRegular() && validPrivateStateDirectory(info)
}
