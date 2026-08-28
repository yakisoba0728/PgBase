package cli

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"golang.org/x/term"
)

var (
	commandStdin  io.Reader = os.Stdin
	terminalInput           = term.IsTerminal
	passwordInput           = term.ReadPassword
)

const maximumAdministratorPasswordBytes = 1024

type fileDescriptor interface{ Fd() uintptr }

func readAdministratorPassword(stdin io.Reader, stderr io.Writer, passwordFromStdin bool) (string, error) {
	if passwordFromStdin {
		reader := bufio.NewReader(io.LimitReader(stdin, maximumAdministratorPasswordBytes+3))
		password, err := reader.ReadString('\n')
		if err != nil {
			if len(password) > maximumAdministratorPasswordBytes+2 {
				return "", configurationError{err: errors.New("administrator password is too long"), message: "password must not exceed 1024 bytes"}
			}
			return "", configurationError{err: err, message: "password input must end with a newline"}
		}
		if strings.Contains(password[:len(password)-1], "\r") {
			password = strings.TrimSuffix(password, "\n")
			password = strings.TrimSuffix(password, "\r")
		} else {
			password = strings.TrimSuffix(password, "\n")
		}
		if len(password) > maximumAdministratorPasswordBytes {
			return "", configurationError{err: errors.New("administrator password is too long"), message: "password must not exceed 1024 bytes"}
		}
		if password == "" {
			return "", configurationError{err: errors.New("empty administrator password"), message: "password must not be empty"}
		}
		return password, nil
	}
	terminal, ok := stdin.(fileDescriptor)
	if !ok || !terminalInput(int(terminal.Fd())) {
		return "", configurationError{err: errors.New("administrator password input is not a terminal"), message: "use --password-stdin when standard input is not a terminal"}
	}
	if _, err := fmt.Fprint(stderr, "Password: "); err != nil {
		return "", err
	}
	first, err := passwordInput(int(terminal.Fd()))
	if err != nil {
		return "", fmt.Errorf("read administrator password: %w", err)
	}
	if _, err := fmt.Fprint(stderr, "\nConfirm password: "); err != nil {
		return "", err
	}
	second, err := passwordInput(int(terminal.Fd()))
	if err != nil {
		return "", fmt.Errorf("read administrator password confirmation: %w", err)
	}
	if _, err := fmt.Fprintln(stderr); err != nil {
		return "", err
	}
	if len(first) == 0 {
		return "", configurationError{err: errors.New("empty administrator password"), message: "password must not be empty"}
	}
	if len(first) > maximumAdministratorPasswordBytes {
		return "", configurationError{err: errors.New("administrator password is too long"), message: "password must not exceed 1024 bytes"}
	}
	if string(first) != string(second) {
		return "", configurationError{err: errors.New("administrator password confirmation does not match"), message: "password confirmation does not match"}
	}
	return string(first), nil
}
