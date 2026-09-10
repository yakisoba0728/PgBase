//go:build windows

package cli

import "github.com/yakisoba0728/PgBase/internal/selfupdate"

func runWindowsUpdateHelper(args []string) error {
	if len(args) != 1 {
		return configurationError{message: "invalid internal update helper"}
	}
	return selfupdate.RunWindowsHelperArgument(args[0])
}
