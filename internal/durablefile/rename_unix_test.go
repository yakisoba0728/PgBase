//go:build !windows

package durablefile

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDurableRenamePublishesCompleteFileAndReportsFailure(t *testing.T) {
	directory := t.TempDir()
	source, destination := filepath.Join(directory, "source"), filepath.Join(directory, "destination")
	if err := os.WriteFile(source, []byte("complete"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := Rename(source, destination); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(destination)
	if err != nil || string(data) != "complete" {
		t.Fatalf("published %q: %v", data, err)
	}
	if err := Rename(filepath.Join(directory, "missing"), destination); !os.IsNotExist(err) {
		t.Fatalf("missing source: %v", err)
	}
	data, err = os.ReadFile(destination)
	if err != nil || string(data) != "complete" {
		t.Fatalf("failed rename changed destination %q: %v", data, err)
	}
}

func TestDurableMkdirAllCreatesAncestorsAndRejectsAFile(t *testing.T) {
	root := t.TempDir()
	directory := filepath.Join(root, "new", "nested")
	if err := MkdirAll(directory, 0700); err != nil {
		t.Fatal(err)
	}
	if err := MkdirAll(directory, 0700); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(directory, "file")
	if err := os.WriteFile(file, nil, 0600); err != nil {
		t.Fatal(err)
	}
	if err := MkdirAll(file, 0700); err == nil {
		t.Fatal("regular file accepted as directory")
	}
}
