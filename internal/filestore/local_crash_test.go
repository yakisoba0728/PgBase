package filestore

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Terminate a real writer without running its deferred cleanup.
func TestLocalPublicationSurvivesProcessTermination(t *testing.T) {
	const key = "objects/11111111-1111-4111-8111-111111111111"
	payload := strings.Repeat("complete replacement\n", 4096)
	if phase := os.Getenv("PGBASE_TEST_CRASH_PHASE"); phase != "" {
		root := os.Getenv("PGBASE_TEST_CRASH_ROOT")
		store, err := NewLocalStore(root)
		if err != nil {
			t.Fatal(err)
		}
		var source io.Reader = strings.NewReader(payload)
		if phase == "during_write" {
			source = io.MultiReader(strings.NewReader("partial"), crashWaitReader{root})
		}
		if err := store.Put(context.Background(), key, source); err != nil {
			t.Fatal(err)
		}
		crashWaitReader{root}.wait()
		return
	}
	for _, phase := range []string{"during_write", "after_publication"} {
		t.Run(phase, func(t *testing.T) {
			root := t.TempDir()
			store, err := NewLocalStore(root)
			if err != nil {
				t.Fatal(err)
			}
			if err := store.Put(context.Background(), key, strings.NewReader("original")); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestLocalPublicationSurvivesProcessTermination$")
			command.Env = append(os.Environ(), "PGBASE_TEST_CRASH_PHASE="+phase, "PGBASE_TEST_CRASH_ROOT="+root)
			var output bytes.Buffer
			command.Stdout, command.Stderr = &output, &output
			if err := command.Start(); err != nil {
				t.Fatal(err)
			}
			defer func() { _ = command.Process.Kill(); _ = command.Wait() }()
			for {
				if _, err := os.Stat(filepath.Join(root, "writer-ready")); err == nil {
					break
				}
				select {
				case <-ctx.Done():
					t.Fatal("writer did not reach publication boundary")
				case <-time.After(10 * time.Millisecond):
				}
			}
			if err := command.Process.Kill(); err != nil {
				t.Fatal(err)
			}
			if err := command.Wait(); err == nil {
				t.Fatal("writer exited normally instead of being terminated")
			}
			store, err = NewLocalStore(root)
			if err != nil {
				t.Fatal(err)
			}
			reader, err := store.Open(context.Background(), key)
			if err != nil {
				t.Fatal(err)
			}
			data, readErr := io.ReadAll(reader)
			closeErr := reader.Close()
			want := "original"
			if phase == "after_publication" {
				want = payload
			}
			if readErr != nil || closeErr != nil || string(data) != want {
				t.Fatalf("reopened object: length=%d, want=%d; read=%v close=%v; writer=%s", len(data), len(want), readErr, closeErr, output.String())
			}
		})
	}
}

type crashWaitReader struct{ root string }

func (r crashWaitReader) Read([]byte) (int, error) { r.wait(); return 0, io.EOF }

func (r crashWaitReader) wait() {
	if err := os.WriteFile(filepath.Join(r.root, "writer-ready"), []byte("ready"), 0600); err != nil {
		panic(fmt.Sprintf("signal publication boundary: %v", err))
	}
	time.Sleep(time.Hour)
}
