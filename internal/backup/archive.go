package backup

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const MaximumArchiveBytes int64 = 2 << 30

// Expanded bytes include tar metadata, padding and terminators as well as
// regular-file payloads. Pack and Unpack enforce the same stream budget.
const maximumUnpackedBytes int64 = 8 << 30
const maximumArchiveEntries = 100_000

// Pack emits a portable, verified backup directory. The archive contains no
// absolute paths, links, permissions, credentials, or deployment configuration.
func Pack(ctx context.Context, directory string, destination io.Writer) error {
	return packArchive(ctx, directory, destination, maximumUnpackedBytes)
}

func packArchive(ctx context.Context, directory string, destination io.Writer, maximumExpanded int64) (resultErr error) {
	manifest, err := VerifyContext(ctx, directory)
	if err != nil {
		return err
	}
	info, err := os.Stat(filepath.Join(directory, "manifest.json"))
	if err != nil {
		return err
	}
	if len(manifest.Files) > maximumArchiveEntries-2 || info.Size() > 16<<20 {
		return errors.New("backup archive exceeds limit")
	}
	remaining := maximumExpanded - info.Size()
	for _, file := range append([]File{manifest.Database}, manifest.Files...) {
		if file.Size > remaining {
			return errors.New("backup archive exceeds limit")
		}
		remaining -= file.Size
	}
	gz := gzip.NewWriter(&boundedArchiveWriter{writer: destination, remaining: MaximumArchiveBytes})
	tw := tar.NewWriter(&boundedArchiveWriter{writer: gz, remaining: maximumExpanded})
	defer func() { resultErr = errors.Join(resultErr, tw.Close(), gz.Close()) }()
	names := []string{"manifest.json", "database.dump"}
	for _, file := range manifest.Files {
		names = append(names, "files/"+file.Path)
	}
	for _, name := range names {
		path := filepath.Join(directory, filepath.FromSlash(name))
		info, err := os.Stat(path)
		if err != nil {
			return err
		}
		if err = tw.WriteHeader(&tar.Header{Name: name, Mode: 0600, Size: info.Size(), Typeflag: tar.TypeReg}); err != nil {
			return err
		}
		file, err := os.Open(path)
		if err != nil {
			return err
		}
		_, copyErr := io.Copy(tw, contextReader{ctx, file})
		if err = errors.Join(copyErr, file.Close()); err != nil {
			return err
		}
	}
	return nil
}

// Unpack accepts only a bounded manifest, database dump and object tree. Every
// entry is created exclusively under a new directory, and Verify rejects missing
// objects, duplicates, extra objects and checksum mismatch before use.
func Unpack(ctx context.Context, source io.Reader, directory string) error {
	return unpackArchive(ctx, source, directory, maximumUnpackedBytes)
}

func unpackArchive(ctx context.Context, source io.Reader, directory string, maximumExpanded int64) (resultErr error) {
	if err := os.Mkdir(directory, 0700); err != nil {
		return err
	}
	complete := false
	defer func() {
		if !complete {
			resultErr = errors.Join(resultErr, os.RemoveAll(directory))
		}
	}()
	if err := os.Mkdir(filepath.Join(directory, "files"), 0700); err != nil {
		return err
	}
	limited := &io.LimitedReader{R: contextReader{ctx, source}, N: MaximumArchiveBytes + 1}
	gz, err := gzip.NewReader(limited)
	if err != nil {
		return err
	}
	defer func() { resultErr = errors.Join(resultErr, gz.Close()) }()
	// Next may consume arbitrarily many local PAX/GNU metadata headers before
	// returning a regular entry. Bound the entire expanded stream, not only
	// payload sizes visible to the extraction loop. One sentinel byte makes an
	// exhausted budget distinguishable from a genuine, checksum-validated EOF.
	expanded := &io.LimitedReader{R: contextReader{ctx, gz}, N: maximumExpanded + 1}
	tr := tar.NewReader(expanded)
	var total int64
	count := 0
	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		count++
		if count > maximumArchiveEntries || header.Typeflag != tar.TypeReg || header.Size < 0 {
			return errors.New("invalid backup archive entry")
		}
		name, err := cleanRelative(header.Name)
		if err != nil {
			return err
		}
		if name != "manifest.json" && name != "database.dump" && !strings.HasPrefix(header.Name, "files/") {
			return errors.New("unexpected backup archive entry")
		}
		// Compare before adding: an attacker-controlled tar size can be close
		// to MaxInt64 and wrap a cumulative total past the extraction budget.
		if header.Size > maximumExpanded-total || (name == "manifest.json" && header.Size > 16<<20) {
			return errors.New("backup archive exceeds limit")
		}
		total += header.Size
		path := filepath.Join(directory, name)
		if err = os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			return err
		}
		file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			return err
		}
		_, copyErr := io.Copy(file, contextReader{ctx, tr})
		if err = errors.Join(copyErr, file.Close()); err != nil {
			return err
		}
	}
	// With no extra uncompressed byte, reading to EOF validates the gzip CRC.
	// One extra byte already makes this archive invalid: draining a trailing
	// compressed member would inflate attacker-controlled junk outside the
	// per-entry expanded-size budget.
	n, err := io.Copy(io.Discard, io.LimitReader(expanded, 1))
	if err != nil {
		return err
	}
	if n != 0 || limited.N <= 0 || expanded.N <= 0 {
		return errors.New("invalid or oversized backup archive")
	}
	if _, err = VerifyContext(ctx, directory); err != nil {
		return err
	}
	complete = true
	return nil
}

// Bound both tar and compressed output: a verified directory can exceed the
// stream budgets once framing or compression overhead is included.
type boundedArchiveWriter struct {
	writer    io.Writer
	remaining int64
}

func (w *boundedArchiveWriter) Write(data []byte) (int, error) {
	if int64(len(data)) > w.remaining {
		return 0, errors.New("backup archive exceeds limit")
	}
	n, err := w.writer.Write(data)
	w.remaining -= int64(n)
	return n, err
}
