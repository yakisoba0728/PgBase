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
const maximumUnpackedBytes int64 = 8 << 30
const maximumArchiveEntries = 100_000

// Pack emits a portable, verified backup directory. The archive contains no
// absolute paths, links, permissions, credentials, or deployment configuration.
func Pack(ctx context.Context, directory string, destination io.Writer) (resultErr error) {
	manifest, err := VerifyContext(ctx, directory)
	if err != nil {
		return err
	}
	gz := gzip.NewWriter(destination)
	tw := tar.NewWriter(gz)
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
func Unpack(ctx context.Context, source io.Reader, directory string) (resultErr error) {
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
	tr := tar.NewReader(gz)
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
		total += header.Size
		if total > maximumUnpackedBytes || (name == "manifest.json" && header.Size > 16<<20) {
			return errors.New("backup archive exceeds limit")
		}
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
	// Read through gzip trailer to validate CRC and bound concatenated junk.
	n, err := io.Copy(io.Discard, gz)
	if err != nil {
		return err
	}
	if n != 0 || limited.N <= 0 {
		return errors.New("invalid or oversized backup archive")
	}
	if _, err = VerifyContext(ctx, directory); err != nil {
		return err
	}
	complete = true
	return nil
}
