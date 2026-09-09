//go:build !windows

package selfupdate

func LaunchWindowsHelper(WindowsHelperRequest) error { return ErrInvalidInput }

func RunWindowsHelperArgument(string) error { return ErrInvalidInput }
