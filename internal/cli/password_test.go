package cli

import (
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestReadAdministratorPasswordBoundsStandardInputBeforeNewline(t *testing.T) {
	reader := &countingReader{reader: strings.NewReader(strings.Repeat("x", 1<<20) + "\n")}
	_, err := readAdministratorPassword(context.Background(), reader, io.Discard, true)
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
	got, err := readAdministratorPassword(context.Background(), strings.NewReader(password+"\r\n"), io.Discard, true)
	if err != nil || got != password {
		t.Fatalf("password length=%d error=%v", len(got), err)
	}
}

func TestReadAdministratorPasswordReadsDescriptorInput(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{name: "newline", input: "descriptor password\n", want: "descriptor password"},
		{name: "crlf", input: "descriptor password\r\n", want: "descriptor password"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reader, writer, err := os.Pipe()
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = reader.Close() })
			go func() {
				_, _ = io.WriteString(writer, tt.input)
				_ = writer.Close()
			}()

			got, err := readAdministratorPassword(context.Background(), reader, io.Discard, true)
			if err != nil || got != tt.want {
				t.Fatalf("password = %q, error = %v", got, err)
			}
		})
	}
}

func TestReadAdministratorPasswordFromStdinHonorsCancellation(t *testing.T) {
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = reader.Close()
		_ = writer.Close()
	})
	started := make(chan struct{})
	stdin := &announcedPasswordFile{File: reader, started: started}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := readAdministratorPassword(ctx, stdin, io.Discard, true)
		done <- err
	}()
	<-started
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("error = %v, want context canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("descriptor password read did not stop after cancellation")
	}
}

func TestReadAdministratorPasswordTerminalReadHonorsCancellation(t *testing.T) {
	originalTerminal, originalReader := terminalInput, terminalPasswordInput
	t.Cleanup(func() {
		terminalInput, terminalPasswordInput = originalTerminal, originalReader
	})
	terminalInput = func(int) bool { return true }
	started := make(chan struct{})
	terminalPasswordInput = func(ctx context.Context, _ int) ([]byte, error) {
		close(started)
		<-ctx.Done()
		return nil, ctx.Err()
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := readAdministratorPassword(ctx, fakeTerminal{}, io.Discard, false)
		done <- err
	}()
	<-started
	cancel()
	err := <-done
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context canceled", err)
	}
}

type countingReader struct {
	reader io.Reader
	read   int
}

type announcedPasswordFile struct {
	*os.File
	started chan struct{}
	once    sync.Once
}

func (f *announcedPasswordFile) Fd() uintptr {
	f.once.Do(func() { close(f.started) })
	return f.File.Fd()
}

func (r *countingReader) Read(buffer []byte) (int, error) {
	n, err := r.reader.Read(buffer)
	r.read += n
	return n, err
}
