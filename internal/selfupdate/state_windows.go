//go:build windows

package selfupdate

import (
	"os"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Windows access is governed by ACLs. Go synthesizes Unix permission bits for
// Windows files (ordinary files commonly appear as 0666 and directories as
// 0777), so those bits cannot be used to decide whether an installation is
// private. The common validation path still rejects links and special files,
// and every state file is opened and checked through a regular descriptor.
func validPrivateStateDirectory(info os.FileInfo) bool {
	return info.IsDir()
}

func validInstallationTarget(info os.FileInfo) bool {
	return info.Mode().IsRegular()
}

func validInstallationDirectory(info os.FileInfo) bool {
	return info.IsDir()
}

func validPrivateStateFile(file *os.File) bool {
	info, err := file.Stat()
	return err == nil && info.Mode().IsRegular() && validHandleSecurity(windows.Handle(file.Fd()))
}

func validPathSecurity(path string) bool {
	descriptor, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION)
	return err == nil && validSecurityDescriptor(descriptor)
}

func validHandleSecurity(handle windows.Handle) bool {
	descriptor, err := windows.GetSecurityInfo(handle, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION)
	return err == nil && validSecurityDescriptor(descriptor)
}

func validSecurityDescriptor(descriptor *windows.SECURITY_DESCRIPTOR) bool {
	owner, _, err := descriptor.Owner()
	if err != nil || owner == nil {
		return false
	}
	system, systemErr := windows.CreateWellKnownSid(windows.WinLocalSystemSid)
	administrators, adminErr := windows.CreateWellKnownSid(windows.WinBuiltinAdministratorsSid)
	trustedInstaller, trustedInstallerErr := windows.StringToSid("S-1-5-80-956008885-3418522649-1831038044-1853292631-2271478464")
	var token windows.Token
	if err = windows.OpenProcessToken(windows.CurrentProcess(), windows.TOKEN_QUERY, &token); err != nil {
		return false
	}
	defer func() { _ = token.Close() }()
	user, userErr := token.GetTokenUser()
	dacl, _, err := descriptor.DACL()
	if err != nil || dacl == nil || systemErr != nil || adminErr != nil || trustedInstallerErr != nil || userErr != nil {
		return false
	}
	if !owner.Equals(user.User.Sid) && !owner.Equals(system) && !owner.Equals(administrators) && !owner.Equals(trustedInstaller) {
		return false
	}
	const fileDeleteChild = 0x40
	const writable = windows.GENERIC_ALL | windows.GENERIC_WRITE | windows.FILE_WRITE_DATA | windows.FILE_APPEND_DATA |
		windows.FILE_WRITE_EA | windows.FILE_WRITE_ATTRIBUTES | fileDeleteChild | windows.DELETE | windows.WRITE_DAC | windows.WRITE_OWNER
	for index := uint32(0); index < uint32(dacl.AceCount); index++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err = windows.GetAce(dacl, index, &ace); err != nil || ace == nil {
			return false
		}
		if ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE {
			if ace.Header.AceType != windows.ACCESS_DENIED_ACE_TYPE {
				return false
			}
			continue
		}
		if uint32(ace.Mask)&uint32(writable) == 0 {
			continue
		}
		sid := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
		if !sid.Equals(owner) && !sid.Equals(user.User.Sid) && !sid.Equals(system) && !sid.Equals(administrators) && !sid.Equals(trustedInstaller) {
			return false
		}
	}
	return true
}
