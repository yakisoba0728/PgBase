package backup

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func makeArchiveFixture(t *testing.T, directory string) {
	t.Helper()
	ctx := context.Background()
	if err := os.MkdirAll(filepath.Join(directory, "files", "objects"), 0700); err != nil {
		t.Fatal(err)
	}
	for name, contents := range map[string]string{"database.dump": "fixture dump", "files/objects/test": "fixture object"} {
		if err := os.WriteFile(filepath.Join(directory, name), []byte(contents), 0600); err != nil {
			t.Fatal(err)
		}
	}
	dump, err := checksum(ctx, filepath.Join(directory, "database.dump"), "database.dump")
	if err != nil {
		t.Fatal(err)
	}
	object, err := checksum(ctx, filepath.Join(directory, "files/objects/test"), "objects/test")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(Manifest{Format: FormatVersion, PostgreSQLMajor: 18, CreatedAt: time.Now().UTC(), Database: dump, Files: []File{object}})
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(directory, "manifest.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
}
func TestArchiveRoundTripAndTamper(t *testing.T) {
	source := t.TempDir()
	makeArchiveFixture(t, source)
	var encoded bytes.Buffer
	if err := Pack(context.Background(), source, &encoded); err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(t.TempDir(), "restored")
	if err := Unpack(context.Background(), bytes.NewReader(encoded.Bytes()), destination); err != nil {
		t.Fatal(err)
	}
	if _, err := Verify(destination); err != nil {
		t.Fatal(err)
	}
	corrupted := append([]byte(nil), encoded.Bytes()...)
	corrupted[len(corrupted)-8] ^= 255
	rejected := filepath.Join(t.TempDir(), "corrupt")
	if err := Unpack(context.Background(), bytes.NewReader(corrupted), rejected); err == nil {
		t.Fatal("corrupt gzip accepted")
	}
	if _, err := os.Stat(rejected); !os.IsNotExist(err) {
		t.Fatal("failed upload directory retained")
	}
	if err := os.WriteFile(filepath.Join(destination, "files/objects/test"), []byte("tamper"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := Pack(context.Background(), destination, io.Discard); err == nil {
		t.Fatal("tampered archive accepted")
	}
}
func TestArchiveRejectsTraversalLinksAndDuplicate(t *testing.T) {
	for _, name := range []string{"../escape", "/absolute", "files/../escape", "unexpected", "manifest.json"} {
		t.Run(name, func(t *testing.T) {
			var buffer bytes.Buffer
			gz := gzip.NewWriter(&buffer)
			tw := tar.NewWriter(gz)
			for range 2 {
				if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0600, Size: 1, Typeflag: tar.TypeReg}); err != nil {
					t.Fatal(err)
				}
				_, _ = tw.Write([]byte("x"))
			}
			_ = tw.Close()
			_ = gz.Close()
			destination := filepath.Join(t.TempDir(), "unpack")
			if err := Unpack(context.Background(), &buffer, destination); err == nil {
				t.Fatal("invalid archive accepted")
			}
		})
	}
}
func TestManagerSerializesPersistsAndRecoversJobs(t *testing.T) {
	root := t.TempDir()
	entered := make(chan struct{})
	release := make(chan struct{})
	manager, err := NewManager(ManagerOptions{Directory: root, Create: func(ctx context.Context, path string) error {
		close(entered)
		select {
		case <-release:
		case <-ctx.Done():
			return ctx.Err()
		}
		makeArchiveFixture(t, path)
		return nil
	}, Restore: func(context.Context, string) error { return nil }})
	if err != nil {
		t.Fatal(err)
	}
	job, err := manager.Create(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	<-entered
	if _, err = manager.Create(context.Background()); !errors.Is(err, ErrBusy) {
		t.Fatal(err)
	}
	close(release)
	deadline := time.Now().Add(3 * time.Second)
	for {
		job, err = manager.Job(context.Background(), job.ID)
		if err != nil {
			t.Fatal(err)
		}
		if job.Status != "running" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("job never finished")
		}
		time.Sleep(time.Millisecond)
	}
	if job.Status != "completed" {
		t.Fatal(job)
	}
	list, err := manager.List(context.Background())
	if err != nil || len(list) != 1 {
		t.Fatalf("entries=%v err=%v", list, err)
	}
	if err = manager.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	options := manager.options
	manager, err = NewManager(options)
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Shutdown(context.Background())
	recovered, err := manager.Job(context.Background(), job.ID)
	if err != nil || recovered.Status != "completed" {
		t.Fatalf("recovered=%v err=%v", recovered, err)
	}
	stream, err := manager.Open(context.Background(), list[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	uploaded, err := manager.Upload(context.Background(), stream)
	_ = stream.Close()
	if err != nil {
		t.Fatal(err)
	}
	if uploaded.ID == list[0].ID {
		t.Fatal("upload reused archive identity")
	}
	if err = manager.Delete(context.Background(), uploaded.ID); err != nil {
		t.Fatal(err)
	}
}
