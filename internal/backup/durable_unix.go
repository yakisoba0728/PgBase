//go:build !windows

package backup

import (
	"errors"
	"os"
	"path/filepath"
)

// DurableRename publishes a file and syncs its parent directory before success.
// An error after rename may leave the destination visible and must be treated as
// an indeterminate publication, not proof that the rename did not happen.
func DurableRename(source, destination string) error {
	if err := os.Rename(source, destination); err != nil {
		return err
	}
	directory, err := os.Open(filepath.Dir(destination))
	if err != nil {
		return err
	}
	return errors.Join(directory.Sync(), directory.Close())
}
