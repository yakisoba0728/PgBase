package durablefile

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestWriteExclusiveRemovesPartialFileAfterWriteSyncAndCloseFailures(t *testing.T) {
	originalOpen := openExclusiveFile
	originalSync := syncExclusiveFile
	originalClose := closeExclusiveFile
	t.Cleanup(func() {
		openExclusiveFile = originalOpen
		syncExclusiveFile = originalSync
		closeExclusiveFile = originalClose
	})

	for _, stage := range []string{"write", "sync", "close"} {
		stage := stage
		t.Run(stage, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "target")
			openExclusiveFile = originalOpen
			syncExclusiveFile = originalSync
			closeExclusiveFile = originalClose
			if stage == "sync" {
				syncExclusiveFile = func(file *os.File) error {
					_ = originalSync(file)
					return errors.New("injected sync failure")
				}
			}
			if stage == "close" {
				closeExclusiveFile = func(file *os.File) error {
					err := originalClose(file)
					if err != nil {
						return err
					}
					return errors.New("injected close failure")
				}
			}

			err := WriteExclusive(path, 0o700, func(destination *os.File) error {
				_, writeErr := destination.Write([]byte("partial contents"))
				if stage == "write" {
					return errors.New("injected write failure")
				}
				return writeErr
			}, func(_ string, err error) error { return err })
			if err == nil {
				t.Fatal("WriteExclusive succeeded after injected failure")
			}
			if _, err := os.Stat(path); !os.IsNotExist(err) {
				t.Fatalf("partial target remains after %s failure: %v", stage, err)
			}
		})
	}
}

func TestWriteExclusiveDoesNotRemoveExistingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "target")
	if err := os.WriteFile(path, []byte("existing"), 0o600); err != nil {
		t.Fatal(err)
	}
	err := WriteExclusive(path, 0o700, func(*os.File) error { return nil }, func(_ string, err error) error { return err })
	if err == nil {
		t.Fatal("WriteExclusive replaced an existing file")
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(contents) != "existing" {
		t.Fatalf("existing file = %q", contents)
	}
}
