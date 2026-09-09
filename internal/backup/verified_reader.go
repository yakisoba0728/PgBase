package backup

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"hash"
	"io"
)

type verifiedReadCloser struct {
	source   io.ReadCloser
	digest   hash.Hash
	expected Entry
	read     int64
	checked  bool
}

func newVerifiedReadCloser(source io.ReadCloser, expected Entry) io.ReadCloser {
	return &verifiedReadCloser{source: source, digest: sha256.New(), expected: expected}
}
func (r *verifiedReadCloser) Read(buffer []byte) (int, error) {
	n, err := r.source.Read(buffer)
	if n > 0 {
		r.read += int64(n)
		_, _ = r.digest.Write(buffer[:n])
	}
	if errors.Is(err, io.EOF) && !r.checked {
		r.checked = true
		if r.expected.Size != 0 && r.read != r.expected.Size {
			return n, errors.New("backup archive size mismatch")
		}
		if r.expected.SHA256 != "" && hex.EncodeToString(r.digest.Sum(nil)) != r.expected.SHA256 {
			return n, errors.New("backup archive checksum mismatch")
		}
	}
	return n, err
}
func (r *verifiedReadCloser) Close() error { return r.source.Close() }
