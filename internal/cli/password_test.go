package cli

import (
	"errors"
	"io"
	"strings"
	"testing"
)

func TestReadAdministratorPasswordBoundsStandardInputBeforeNewline(t *testing.T) {
	reader := &countingReader{reader: strings.NewReader(strings.Repeat("x", 1<<20) + "\n")}
	_, err := readAdministratorPassword(reader, io.Discard, true)
	var configErr configurationError
	if !errors.As(err, &configErr) || configErr.message != "password must not exceed 1024 bytes" {
		t.Fatalf("error = %v", err)
	}
	if reader.read > maximumAdministratorPasswordBytes+3 {
		t.Fatalf("read %d bytes, want bounded input", reader.read)
	}
}

func TestReadAdministratorPasswordAcceptsMaximumLengthWithCRLF(t *testing.T) {
	password := strings.Repeat("x", maximumAdministratorPasswordBytes)
	got, err := readAdministratorPassword(strings.NewReader(password+"\r\n"), io.Discard, true)
	if err != nil || got != password {
		t.Fatalf("password length=%d error=%v", len(got), err)
	}
}

type countingReader struct {
	reader io.Reader
	read   int
}

func (r *countingReader) Read(buffer []byte) (int, error) {
	n, err := r.reader.Read(buffer)
	r.read += n
	return n, err
}
