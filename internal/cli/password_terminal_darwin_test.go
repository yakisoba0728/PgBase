//go:build darwin

package cli

import (
	"bytes"
	"context"
	"errors"
	"os"
	"runtime"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/unix"
	"golang.org/x/term"
)

func TestReadTerminalPasswordContextCancellationRestoresPTY(t *testing.T) {
	master, terminal := openDarwinTestPTY(t)
	t.Cleanup(func() {
		_ = master.Close()
		_ = terminal.Close()
	})
	if !term.IsTerminal(int(terminal.Fd())) {
		t.Fatal("PTY slave is not reported as a terminal")
	}

	initial := readDarwinTestTermios(t, terminal)
	configured := *initial
	configured.Lflag |= unix.ECHO
	configured.Lflag &^= unix.ICANON | unix.ISIG
	configured.Iflag &^= unix.ICRNL
	if err := unix.IoctlSetTermios(int(terminal.Fd()), ioctlWriteTermios, &configured); err != nil {
		t.Fatalf("configure PTY terminal state: %v", err)
	}
	original := readDarwinTestTermios(t, terminal)
	if original.Lflag&unix.ECHO == 0 ||
		original.Lflag&(unix.ICANON|unix.ISIG) != 0 ||
		original.Iflag&unix.ICRNL != 0 {
		t.Fatalf("PTY did not accept the test terminal state: %#v", *original)
	}

	ctx, cancel := context.WithCancel(context.Background())
	type passwordResult struct {
		password []byte
		err      error
	}
	completed := make(chan passwordResult, 1)
	go func() {
		password, err := readTerminalPasswordContext(ctx, int(terminal.Fd()))
		completed <- passwordResult{password: password, err: err}
	}()
	readerFinished := false
	defer func() {
		cancel()
		if readerFinished {
			return
		}
		_, _ = master.Write([]byte{'\n'})
		select {
		case <-completed:
		case <-time.After(time.Second):
		}
	}()

	active, err := waitForDarwinTestTermios(terminal, 2*time.Second, func(state *unix.Termios) bool {
		return state.Lflag&unix.ECHO == 0 &&
			state.Lflag&(unix.ICANON|unix.ISIG) == unix.ICANON|unix.ISIG &&
			state.Iflag&unix.ICRNL != 0
	})
	if err != nil {
		t.Fatalf("wait for password terminal mode: %v", err)
	}
	if active.Lflag&unix.ECHO != 0 {
		t.Fatal("terminal echo remained enabled during password read")
	}

	if _, err := master.Write([]byte("unterminated password")); err != nil {
		t.Fatalf("write partial password to PTY: %v", err)
	}
	select {
	case result := <-completed:
		readerFinished = true
		t.Fatalf("password read returned before cancellation: password=%q error=%v", result.password, result.err)
	case <-time.After(2 * passwordDescriptorPollInterval):
	}

	cancel()
	select {
	case result := <-completed:
		readerFinished = true
		if !errors.Is(result.err, context.Canceled) {
			t.Fatalf("password read error = %v, want context canceled", result.err)
		}
		if len(result.password) != 0 {
			t.Fatalf("password = %q, want no partial password after cancellation", result.password)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("password read did not stop after cancellation")
	}

	restored := readDarwinTestTermios(t, terminal)
	if *restored != *original {
		t.Fatalf("terminal state was not restored:\n got  %#v\n want %#v", *restored, *original)
	}
	if restored.Lflag&unix.ECHO == 0 {
		t.Fatal("terminal echo was not restored")
	}
}

func openDarwinTestPTY(t *testing.T) (*os.File, *os.File) {
	t.Helper()
	masterDescriptor, err := unix.Open("/dev/ptmx", unix.O_RDWR|unix.O_CLOEXEC, 0)
	if err != nil {
		t.Fatalf("open PTY master: %v", err)
	}
	master := os.NewFile(uintptr(masterDescriptor), "/dev/ptmx")
	if master == nil {
		_ = unix.Close(masterDescriptor)
		t.Fatal("create PTY master file")
	}
	closeMaster := true
	defer func() {
		if closeMaster {
			_ = master.Close()
		}
	}()

	if err := darwinTestPTYIoctl(masterDescriptor, unix.TIOCPTYGRANT); err != nil {
		t.Fatalf("grant PTY slave: %v", err)
	}
	if err := darwinTestPTYIoctl(masterDescriptor, unix.TIOCPTYUNLK); err != nil {
		t.Fatalf("unlock PTY slave: %v", err)
	}
	var name [128]byte
	if err := readDarwinTestPTYName(masterDescriptor, &name); err != nil {
		t.Fatalf("read PTY slave name: %v", err)
	}
	nameEnd := bytes.IndexByte(name[:], 0)
	if nameEnd < 0 {
		t.Fatal("PTY slave name is not NUL-terminated")
	}

	terminalDescriptor, err := unix.Open(string(name[:nameEnd]), unix.O_RDWR|unix.O_NOCTTY|unix.O_CLOEXEC, 0)
	if err != nil {
		t.Fatalf("open PTY slave: %v", err)
	}
	terminal := os.NewFile(uintptr(terminalDescriptor), string(name[:nameEnd]))
	if terminal == nil {
		_ = unix.Close(terminalDescriptor)
		t.Fatal("create PTY slave file")
	}
	closeMaster = false
	return master, terminal
}

func darwinTestPTYIoctl(descriptor int, request uint) error {
	_, _, errno := unix.Syscall(unix.SYS_IOCTL, uintptr(descriptor), uintptr(request), 0)
	if errno != 0 {
		return errno
	}
	return nil
}

func readDarwinTestPTYName(descriptor int, name *[128]byte) error {
	_, _, errno := unix.Syscall(
		unix.SYS_IOCTL,
		uintptr(descriptor),
		uintptr(unix.TIOCPTYGNAME),
		uintptr(unsafe.Pointer(&name[0])),
	)
	runtime.KeepAlive(name)
	if errno != 0 {
		return errno
	}
	return nil
}

func readDarwinTestTermios(t *testing.T, terminal *os.File) *unix.Termios {
	t.Helper()
	state, err := unix.IoctlGetTermios(int(terminal.Fd()), ioctlReadTermios)
	if err != nil {
		t.Fatalf("read PTY terminal state: %v", err)
	}
	return state
}

func waitForDarwinTestTermios(terminal *os.File, timeout time.Duration, matches func(*unix.Termios) bool) (*unix.Termios, error) {
	deadline := time.Now().Add(timeout)
	for {
		state, err := unix.IoctlGetTermios(int(terminal.Fd()), ioctlReadTermios)
		if err != nil {
			return nil, err
		}
		if matches(state) {
			return state, nil
		}
		if time.Now().After(deadline) {
			return nil, errors.New("timed out")
		}
		time.Sleep(time.Millisecond)
	}
}
