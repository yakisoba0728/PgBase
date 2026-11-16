package backup

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"io"
	"math"
	"os"
	"path/filepath"
	"strings"
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

func TestPackRejectsManifestBeyondRestoreLimit(t *testing.T) {
	directory := t.TempDir()
	makeArchiveFixture(t, directory)
	file, err := os.OpenFile(filepath.Join(directory, "manifest.json"), os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		t.Fatal(err)
	}
	_, writeErr := file.Write(bytes.Repeat([]byte(" "), 16<<20))
	if err := errors.Join(writeErr, file.Close()); err != nil {
		t.Fatal(err)
	}
	var packed bytes.Buffer
	if err := Pack(context.Background(), directory, &packed); err == nil || packed.Len() != 0 {
		t.Fatal("unrestorable backup emitted")
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
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	entered := make(chan struct{})
	release := make(chan struct{})
	manager, err := NewManager(ManagerOptions{Directory: root, Retention: 1, Create: func(ctx context.Context, path string) error {
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
	// Register ownership immediately, before any assertion can fail. Workers
	// must be stopped before TempDir cleanup, including on the timeout path.
	own := func(owned *Manager) {
		t.Cleanup(func() {
			cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cleanupCancel()
			if err := owned.Shutdown(cleanupCtx); err != nil {
				t.Errorf("backup manager cleanup: %v", err)
			}
		})
	}
	own(manager)
	job, err := manager.Create(ctx)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal("backup callback was not entered")
	}
	if _, err = manager.Create(ctx); !errors.Is(err, ErrBusy) {
		t.Fatal(err)
	}
	manager.mu.Lock()
	drained := manager.drained
	manager.mu.Unlock()
	close(release)
	// This is a serialization/durability contract, not a three-second storage
	// benchmark. Wait for the actual release boundary with a deadlock watchdog.
	select {
	case <-drained:
	case <-ctx.Done():
		t.Fatal("backup operation did not release its ownership")
	}
	job, err = manager.Job(ctx, job.ID)
	if err != nil || job.Status != "completed" {
		t.Fatalf("job=%+v err=%v", job, err)
	}
	list, err := manager.List(ctx)
	if err != nil || len(list) != 1 {
		t.Fatalf("entries=%v err=%v", list, err)
	}
	if err = manager.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	options := manager.options
	manager, err = NewManager(options)
	if err != nil {
		t.Fatal(err)
	}
	own(manager)
	recovered, err := manager.Job(ctx, job.ID)
	if err != nil || recovered.Status != "completed" {
		t.Fatalf("recovered=%v err=%v", recovered, err)
	}
	stream, err := manager.Open(ctx, list[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	archive, readErr := io.ReadAll(stream)
	closeErr := stream.Close()
	if err := errors.Join(readErr, closeErr); err != nil {
		t.Fatal(err)
	}
	// Retention may remove the original archive during Upload. Release its
	// read handle first so the fixture is valid on Windows as well as Unix.
	uploaded, err := manager.Upload(ctx, bytes.NewReader(archive))
	if err != nil {
		t.Fatal(err)
	}
	if uploaded.ID == list[0].ID {
		t.Fatal("upload reused archive identity")
	}
	if retained, listErr := manager.List(ctx); listErr != nil || len(retained) != 1 || retained[0].ID != uploaded.ID {
		t.Fatalf("retention entries=%+v err=%v", retained, listErr)
	}
	if err = manager.Delete(ctx, uploaded.ID); err != nil {
		t.Fatal(err)
	}
}

func TestVerifyRejectsManifestBeyondRestoreLimit(t *testing.T) {
	directory := t.TempDir()
	makeArchiveFixture(t, directory)
	file, err := os.OpenFile(filepath.Join(directory, "manifest.json"), os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		t.Fatal(err)
	}
	_, writeErr := file.Write(bytes.Repeat([]byte(" "), 16<<20))
	if err := errors.Join(writeErr, file.Close()); err != nil {
		t.Fatal(err)
	}
	if _, err := VerifyContext(t.Context(), directory); err == nil {
		t.Fatal("oversized manifest accepted")
	}
}

type cancelArchiveWriter struct{ cancel context.CancelFunc }

func (w cancelArchiveWriter) Write(data []byte) (int, error) { w.cancel(); return len(data), nil }

type cancelArchiveReader struct {
	io.Reader
	cancel context.CancelFunc
}

func (r cancelArchiveReader) Read(data []byte) (int, error) {
	n, err := r.Reader.Read(data)
	r.cancel()
	return n, err
}

func TestArchiveCancellationCleansUnpublishedDestination(t *testing.T) {
	source := t.TempDir()
	makeArchiveFixture(t, source)
	ctx, cancel := context.WithCancel(t.Context())
	if err := Pack(ctx, source, cancelArchiveWriter{cancel}); !errors.Is(err, context.Canceled) {
		t.Fatalf("Pack cancellation: %v", err)
	}
	var encoded bytes.Buffer
	if err := Pack(t.Context(), source, &encoded); err != nil {
		t.Fatal(err)
	}
	ctx, cancel = context.WithCancel(t.Context())
	defer cancel()
	destination := filepath.Join(t.TempDir(), "canceled")
	if err := Unpack(ctx, cancelArchiveReader{&encoded, cancel}, destination); !errors.Is(err, context.Canceled) {
		t.Fatalf("Unpack cancellation: %v", err)
	}
	if _, err := os.Stat(destination); !os.IsNotExist(err) {
		t.Fatalf("unpublished destination retained: %v", err)
	}
}

func TestUnpackRejectsOversizedHeaderBeforeWritingPayload(t *testing.T) {
	for _, test := range []struct {
		name string
		size int64
	}{{"database.dump", maximumUnpackedBytes + 1}, {"manifest.json", (16 << 20) + 1}} {
		t.Run(test.name, func(t *testing.T) {
			var encoded bytes.Buffer
			gz := gzip.NewWriter(&encoded)
			tw := tar.NewWriter(gz)
			if err := tw.WriteHeader(&tar.Header{Name: test.name, Typeflag: tar.TypeReg, Mode: 0600, Size: test.size}); err != nil {
				t.Fatal(err)
			}
			// No payload is needed: the header itself exceeds the restore budget.
			_ = gz.Close()
			destination := filepath.Join(t.TempDir(), "oversized")
			if err := Unpack(t.Context(), &encoded, destination); err == nil || !strings.Contains(err.Error(), "exceeds limit") {
				t.Fatalf("header limit: %v", err)
			}
			if _, err := os.Stat(destination); !os.IsNotExist(err) {
				t.Fatalf("retained rejected destination: %v", err)
			}
		})
	}
}

func TestUnpackRejectsCumulativeSizeOverflowBeforeReadingPayload(t *testing.T) {
	var encoded bytes.Buffer
	gz := gzip.NewWriter(&encoded)
	tw := tar.NewWriter(gz)
	if err := tw.WriteHeader(&tar.Header{Name: "files/first", Typeflag: tar.TypeReg, Mode: 0600, Size: 1}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write([]byte("x")); err != nil {
		t.Fatal(err)
	}
	if err := tw.WriteHeader(&tar.Header{Name: "database.dump", Typeflag: tar.TypeReg, Mode: 0600, Size: math.MaxInt64, Format: tar.FormatGNU}); err != nil {
		t.Fatal(err)
	}
	// There is intentionally no huge payload. Rejecting the header must happen
	// before reading it, rather than wrapping the cumulative byte count.
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(t.TempDir(), "overflow")
	if err := Unpack(t.Context(), &encoded, destination); err == nil || !strings.Contains(err.Error(), "exceeds limit") {
		t.Fatalf("overflowing header limit: %v", err)
	}
	if _, err := os.Stat(destination); !os.IsNotExist(err) {
		t.Fatalf("retained rejected destination: %v", err)
	}
}

func TestArchiveWriterEnforcesCompressedBudgetAndPropagatesCloseFailure(t *testing.T) {
	var output bytes.Buffer
	bounded := &boundedArchiveWriter{writer: &output, remaining: 3}
	if n, err := bounded.Write([]byte("abc")); err != nil || n != 3 {
		t.Fatalf("exact boundary n=%d err=%v", n, err)
	}
	if n, err := bounded.Write([]byte("d")); err == nil || n != 0 || output.String() != "abc" {
		t.Fatalf("overflow n=%d err=%v output=%q", n, err, output.String())
	}
	output.Reset()
	gz := gzip.NewWriter(&boundedArchiveWriter{writer: &output, remaining: 10})
	if _, err := gz.Write([]byte("body")); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err == nil {
		t.Fatal("compressed trailer exceeded budget without failure")
	}
	if output.Len() > 10 {
		t.Fatalf("compressed budget exceeded: %d", output.Len())
	}
}

// A tar terminator followed by a gzip member is invalid immediately. Do not
// inflate the remainder merely to discover its already-known invalidity.
func TestUnpackRejectsTrailingContentWithoutDrainingIt(t *testing.T) {
	source := t.TempDir()
	makeArchiveFixture(t, source)
	var archive bytes.Buffer
	if err := Pack(t.Context(), source, &archive); err != nil {
		t.Fatal(err)
	}
	var trailing bytes.Buffer
	gz := gzip.NewWriter(&trailing)
	chunk := make([]byte, 64<<10)
	for i := range chunk {
		chunk[i] = byte(i)
	}
	for range 32 {
		if _, err := gz.Write(chunk); err != nil {
			t.Fatal(err)
		}
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	sentinel := errors.New("unpack drained beyond invalid trailing member")
	sourceReader := io.MultiReader(bytes.NewReader(archive.Bytes()), bytes.NewReader(trailing.Bytes()), failingArchiveReader{sentinel})
	destination := filepath.Join(t.TempDir(), "rejected")
	err := Unpack(t.Context(), sourceReader, destination)
	if err == nil {
		t.Fatal("trailing gzip member accepted")
	}
	if errors.Is(err, sentinel) {
		t.Fatal("invalid trailing payload was fully decompressed")
	}
	if _, err := os.Stat(destination); !os.IsNotExist(err) {
		t.Fatal("rejected extraction retained")
	}
}

type failingArchiveReader struct{ err error }

func (r failingArchiveReader) Read([]byte) (int, error) { return 0, r.err }
