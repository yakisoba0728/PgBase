//go:build windows

package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"runtime"
	"syscall"
	"time"
	"unicode/utf8"

	"golang.org/x/sys/windows"
)

var cancelSynchronousIO = windows.NewLazySystemDLL("kernel32.dll").NewProc("CancelSynchronousIo")

func readTerminalPasswordContext(ctx context.Context, descriptor int) (password []byte, err error) {
	handle := windows.Handle(descriptor)
	reader, err := duplicateWindowsPasswordInput(handle)
	if err != nil {
		return nil, err
	}
	defer reader.Close()
	var original uint32
	if err := windows.GetConsoleMode(handle, &original); err != nil {
		return nil, fmt.Errorf("read terminal state: %w", err)
	}
	withoutEcho := original
	withoutEcho &^= windows.ENABLE_ECHO_INPUT | windows.ENABLE_LINE_INPUT
	withoutEcho |= windows.ENABLE_PROCESSED_OUTPUT | windows.ENABLE_PROCESSED_INPUT
	if err := windows.SetConsoleMode(handle, withoutEcho); err != nil {
		return nil, fmt.Errorf("disable terminal echo: %w", err)
	}
	defer func() {
		if restoreErr := windows.SetConsoleMode(handle, original); restoreErr != nil {
			err = errors.Join(err, fmt.Errorf("restore terminal state: %w", restoreErr))
		}
	}()

	result := runCancelableWindowsRead(ctx, func() windowsPasswordResult {
		line, _, err := readWindowsLine(reader, maximumAdministratorPasswordBytes+1, true)
		return windowsPasswordResult{line: line, err: err}
	})
	if result.err != nil {
		return nil, result.err
	}
	return result.line, nil
}

func readStreamPasswordLineContext(ctx context.Context, descriptor, maximumBytes int) ([]byte, bool, error) {
	reader, err := duplicateWindowsPasswordInput(windows.Handle(descriptor))
	if err != nil {
		return nil, false, err
	}
	defer reader.Close()
	result := runCancelableWindowsRead(ctx, func() windowsPasswordResult {
		line, terminated, err := readWindowsLine(reader, maximumBytes, false)
		return windowsPasswordResult{line: line, terminated: terminated, err: err}
	})
	return result.line, result.terminated, result.err
}

type windowsPasswordResult struct {
	line       []byte
	terminated bool
	err        error
}

func duplicateWindowsPasswordInput(handle windows.Handle) (*os.File, error) {
	process, err := windows.GetCurrentProcess()
	if err != nil {
		return nil, fmt.Errorf("open current process: %w", err)
	}
	var duplicate windows.Handle
	if err := windows.DuplicateHandle(process, handle, process, &duplicate, 0, false, windows.DUPLICATE_SAME_ACCESS); err != nil {
		return nil, fmt.Errorf("duplicate password input: %w", err)
	}
	return os.NewFile(uintptr(duplicate), "password input"), nil
}

func readWindowsLine(reader io.Reader, maximumBytes int, terminal bool) ([]byte, bool, error) {
	line := make([]byte, 0, min(maximumBytes, 128))
	var buffer [256]byte
	readBuffer := buffer[:]
	if terminal {
		// Preserve bytes after the first carriage return for the confirmation
		// prompt. os.File's console reader retains UTF-8 conversion remainder
		// across these one-byte reads.
		readBuffer = buffer[:1]
	}
	for {
		read, err := reader.Read(readBuffer)
		if err != nil {
			return nil, false, fmt.Errorf("read password input: %w", err)
		}
		if read == 0 {
			if terminal && len(line) == 0 {
				return nil, false, io.EOF
			}
			return line, false, nil
		}
		for _, character := range buffer[:read] {
			if terminal {
				switch character {
				case '\b':
					if len(line) > 0 {
						_, size := utf8.DecodeLastRune(line)
						line = line[:len(line)-size]
					}
					continue
				case '\r':
					return line, true, nil
				case '\n':
					continue
				}
			}
			if character == '\n' {
				return line, true, nil
			}
			line = append(line, character)
			if len(line) >= maximumBytes {
				return line, false, nil
			}
		}
	}
}

func runCancelableWindowsRead(ctx context.Context, read func() windowsPasswordResult) windowsPasswordResult {
	type readyResult struct {
		thread windows.Handle
		err    error
	}
	ready := make(chan readyResult, 1)
	completed := make(chan windowsPasswordResult, 1)
	release := make(chan struct{})
	released := make(chan struct{})
	go func() {
		runtime.LockOSThread()
		thread, err := windows.OpenThread(windows.THREAD_TERMINATE, false, windows.GetCurrentThreadId())
		ready <- readyResult{thread: thread, err: err}
		if err != nil {
			completed <- windowsPasswordResult{err: fmt.Errorf("open password reader thread: %w", err)}
		} else {
			completed <- read()
		}
		<-release
		runtime.UnlockOSThread()
		close(released)
	}()

	reader := <-ready
	if reader.thread != 0 {
		defer windows.CloseHandle(reader.thread)
	}
	defer func() {
		close(release)
		<-released
	}()
	if reader.err != nil {
		return <-completed
	}
	select {
	case result := <-completed:
		return result
	case <-ctx.Done():
	}

	// CancelSynchronousIo targets the exact OS thread blocked in ReadFile.
	// Retry ERROR_NOT_FOUND until the worker has entered the read, then wait
	// for it to exit before terminal restoration can run.
	for {
		select {
		case <-completed:
			return windowsPasswordResult{err: ctx.Err()}
		default:
		}
		canceled, _, callErr := cancelSynchronousIO.Call(uintptr(reader.thread))
		if canceled == 0 {
			if errno, ok := callErr.(syscall.Errno); !ok || errno != windows.ERROR_NOT_FOUND {
				result := <-completed
				return windowsPasswordResult{err: errors.Join(ctx.Err(), fmt.Errorf("cancel password input: %w", callErr), result.err)}
			}
		}
		time.Sleep(time.Millisecond)
	}
}
