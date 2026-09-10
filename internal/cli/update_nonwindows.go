//go:build !windows

package cli

import "errors"

func runWindowsUpdateHelper([]string) error {
	return configurationError{err: errors.New("windows helper is unavailable"), message: "invalid internal update helper"}
}
