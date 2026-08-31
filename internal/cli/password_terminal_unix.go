//go:build aix || darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris || zos

package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"golang.org/x/sys/unix"
)

const passwordDescriptorPollInterval = 25 * time.Millisecond

func readTerminalPasswordContext(ctx context.Context, descriptor int) (password []byte, err error) {
	original, err := unix.IoctlGetTermios(descriptor, ioctlReadTermios)
	if err != nil {
		return nil, fmt.Errorf("read terminal state: %w", err)
	}
	withoutEcho := *original
	withoutEcho.Lflag &^= unix.ECHO
	withoutEcho.Lflag |= unix.ICANON | unix.ISIG
	withoutEcho.Iflag |= unix.ICRNL
	if err := unix.IoctlSetTermios(descriptor, ioctlWriteTermios, &withoutEcho); err != nil {
		return nil, fmt.Errorf("disable terminal echo: %w", err)
	}
	defer func() {
		if restoreErr := unix.IoctlSetTermios(descriptor, ioctlWriteTermios, original); restoreErr != nil {
			err = errors.Join(err, fmt.Errorf("restore terminal state: %w", restoreErr))
		}
	}()

	password, terminated, err := readDescriptorLineContext(ctx, descriptor, maximumAdministratorPasswordBytes+1)
	if err != nil {
		return nil, err
	}
	if !terminated && len(password) == 0 {
		return nil, io.EOF
	}
	return password, nil
}

func readStreamPasswordLineContext(ctx context.Context, descriptor, maximumBytes int) ([]byte, bool, error) {
	return readDescriptorLineContext(ctx, descriptor, maximumBytes)
}

func readDescriptorLineContext(ctx context.Context, descriptor, maximumBytes int) ([]byte, bool, error) {
	line := make([]byte, 0, min(maximumBytes, 128))
	pollDescriptors := []unix.PollFd{{
		Fd:     int32(descriptor),
		Events: unix.POLLIN | unix.POLLHUP | unix.POLLERR,
	}}
	var buffer [1]byte
	for {
		if err := ctx.Err(); err != nil {
			return nil, false, err
		}
		pollDescriptors[0].Revents = 0
		ready, err := unix.Poll(pollDescriptors, int(passwordDescriptorPollInterval/time.Millisecond))
		if errors.Is(err, unix.EINTR) {
			continue
		}
		if err != nil {
			return nil, false, fmt.Errorf("wait for password input: %w", err)
		}
		if ready == 0 {
			continue
		}
		if pollDescriptors[0].Revents&unix.POLLNVAL != 0 {
			return nil, false, fmt.Errorf("read password input: %w", unix.EBADF)
		}
		read, readErr := unix.Read(descriptor, buffer[:])
		if read > 0 {
			if buffer[0] == '\n' {
				return line, true, nil
			}
			line = append(line, buffer[0])
			if len(line) >= maximumBytes {
				return line, false, nil
			}
		}
		if errors.Is(readErr, unix.EINTR) || errors.Is(readErr, unix.EAGAIN) {
			continue
		}
		if readErr != nil {
			return nil, false, fmt.Errorf("read password input: %w", readErr)
		}
		if read == 0 {
			return line, false, nil
		}
	}
}
