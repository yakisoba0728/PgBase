package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
)

const maximumAdministratorPasswordBytes = 1024

var (
	errDescriptorReadUnsupported = errors.New("context-aware descriptor reads are unsupported")
	errNoTerminal                = errors.New("no controlling terminal is available")
)

type fileDescriptor interface{ Fd() uintptr }

func readAdministratorPassword(ctx context.Context, streams IO, passwordFromStdin bool) (string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	stdin := streams.stdin()
	stderr := streams.stderr()
	if passwordFromStdin {
		return readStreamPassword(ctx, stdin)
	}
	descriptor, ok := stdin.(fileDescriptor)
	if !ok || !streams.terminal().IsTerminal(int(descriptor.Fd())) {
		return "", configurationError{err: errors.New("administrator password input is not a terminal"), message: "use --password-stdin when standard input is not a terminal"}
	}
	terminal := streams.terminal()
	if _, err := fmt.Fprint(stderr, "Password: "); err != nil {
		return "", err
	}
	first, err := terminal.ReadPassword(ctx, int(descriptor.Fd()))
	if err != nil {
		if ctx.Err() != nil {
			_, _ = fmt.Fprintln(stderr)
		}
		return "", fmt.Errorf("read administrator password: %w", err)
	}
	if len(first) == 0 {
		return "", configurationError{err: errors.New("empty administrator password"), message: "password must not be empty"}
	}
	if len(first) > maximumAdministratorPasswordBytes {
		return "", configurationError{err: errors.New("administrator password is too long"), message: "password must not exceed 1024 bytes"}
	}
	if err := ctx.Err(); err != nil {
		_, _ = fmt.Fprintln(stderr)
		return "", err
	}
	if _, err := fmt.Fprint(stderr, "\nConfirm password: "); err != nil {
		return "", err
	}
	second, err := terminal.ReadPassword(ctx, int(descriptor.Fd()))
	if err != nil {
		if ctx.Err() != nil {
			_, _ = fmt.Fprintln(stderr)
		}
		return "", fmt.Errorf("read administrator password confirmation: %w", err)
	}
	if _, err := fmt.Fprintln(stderr); err != nil {
		return "", err
	}
	if string(first) != string(second) {
		return "", configurationError{err: errors.New("administrator password confirmation does not match"), message: "password confirmation does not match"}
	}
	return string(first), nil
}

func readStreamPassword(ctx context.Context, stdin io.Reader) (string, error) {
	if descriptor, ok := stdin.(fileDescriptor); ok {
		line, terminated, err := readStreamPasswordLineContext(ctx, int(descriptor.Fd()), maximumAdministratorPasswordBytes+2)
		if !errors.Is(err, errDescriptorReadUnsupported) {
			return validateStreamPassword(line, terminated, err)
		}
	}

	reader := bufio.NewReader(io.LimitReader(stdin, maximumAdministratorPasswordBytes+3))
	line, err := reader.ReadString('\n')
	if ctxErr := ctx.Err(); ctxErr != nil {
		return "", ctxErr
	}
	return validateStreamPassword([]byte(strings.TrimSuffix(line, "\n")), err == nil, err)
}

func validateStreamPassword(line []byte, terminated bool, readErr error) (string, error) {
	if len(line) > maximumAdministratorPasswordBytes+1 {
		return "", configurationError{err: errors.New("administrator password is too long"), message: "password must not exceed 1024 bytes"}
	}
	if readErr != nil || !terminated {
		if len(line) > maximumAdministratorPasswordBytes {
			return "", configurationError{err: errors.New("administrator password is too long"), message: "password must not exceed 1024 bytes"}
		}
		if readErr == nil {
			readErr = io.EOF
		}
		return "", configurationError{err: readErr, message: "password input must end with a newline"}
	}
	password := string(line)
	if strings.Contains(password, "\r") {
		password = strings.TrimSuffix(password, "\r")
	}
	if len(password) > maximumAdministratorPasswordBytes {
		return "", configurationError{err: errors.New("administrator password is too long"), message: "password must not exceed 1024 bytes"}
	}
	if password == "" {
		return "", configurationError{err: errors.New("empty administrator password"), message: "password must not be empty"}
	}
	return password, nil
}
