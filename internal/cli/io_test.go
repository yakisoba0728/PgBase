package cli

import (
	"context"
	"io"
	"sync"
)

// testIO builds an invocation's streams with no controlling terminal, which is
// what a scripted run should look like unless it deliberately supplies one.
func testIO(stdin io.Reader, stdout, stderr io.Writer) IO {
	return IO{Stdin: stdin, Stdout: stdout, Stderr: stderr}
}

// scriptedTerminal is the test-side Terminal adapter. It hands out one queued
// password per read so a confirmation prompt can be given a matching or
// mismatching second answer, and it can block on cancellation instead.
type scriptedTerminal struct {
	interactive bool
	read        func(context.Context, int) ([]byte, error)

	mu        sync.Mutex
	passwords [][]byte
}

func (terminal *scriptedTerminal) IsTerminal(int) bool { return terminal.interactive }

func (terminal *scriptedTerminal) ReadPassword(ctx context.Context, descriptor int) ([]byte, error) {
	if terminal.read != nil {
		return terminal.read(ctx, descriptor)
	}
	terminal.mu.Lock()
	defer terminal.mu.Unlock()
	if len(terminal.passwords) == 0 {
		return nil, io.EOF
	}
	password := terminal.passwords[0]
	terminal.passwords = terminal.passwords[1:]
	return password, nil
}

func scriptedPasswords(passwords ...string) *scriptedTerminal {
	queued := make([][]byte, len(passwords))
	for index, password := range passwords {
		queued[index] = []byte(password)
	}
	return &scriptedTerminal{interactive: true, passwords: queued}
}

// terminalIO pairs a scripted terminal with a standard input that reports a
// file descriptor, which is what the interactive password path requires.
func terminalIO(terminal Terminal, stdout, stderr io.Writer) IO {
	return IO{Stdin: fakeTerminal{}, Stdout: stdout, Stderr: stderr, Terminal: terminal}
}
