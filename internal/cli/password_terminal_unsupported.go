//go:build !aix && !darwin && !dragonfly && !freebsd && !linux && !netbsd && !openbsd && !solaris && !windows && !zos

package cli

import (
	"context"

	"golang.org/x/term"
)

func readTerminalPasswordContext(ctx context.Context, descriptor int) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return term.ReadPassword(descriptor)
}

func readStreamPasswordLineContext(context.Context, int, int) ([]byte, bool, error) {
	return nil, false, errDescriptorReadUnsupported
}
