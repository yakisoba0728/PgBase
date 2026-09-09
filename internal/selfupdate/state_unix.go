//go:build !windows

package selfupdate

import (
	"os"
	"syscall"
)

func validPrivateStateDirectory(info os.FileInfo) bool {
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && stat.Uid == uint32(os.Geteuid()) && info.Mode().Perm()&0o077 == 0
}

func validPrivateStateFile(file *os.File) bool {
	info, err := file.Stat()
	return err == nil && info.Mode().IsRegular() && validPrivateStateDirectory(info)
}
