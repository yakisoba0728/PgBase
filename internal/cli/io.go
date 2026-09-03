package cli

import (
	"context"
	"io"
	"os"

	"golang.org/x/term"
)

// Terminal is the interactive half of a CLI invocation's standard input. It
// exists so a test can script an interactive session without a controlling
// terminal, and so nothing in this package reaches for a process-wide variable
// to decide how a password is read.
type Terminal interface {
	IsTerminal(descriptor int) bool
	ReadPassword(ctx context.Context, descriptor int) ([]byte, error)
}

// IO is every stream one CLI invocation may read or write. Passing it by value
// keeps two concurrent invocations independent, which process-wide stdin and
// terminal variables could not guarantee.
type IO struct {
	Stdin    io.Reader
	Stdout   io.Writer
	Stderr   io.Writer
	Terminal Terminal
}

// SystemIO is the process's own streams and its real controlling terminal.
func SystemIO() IO {
	return IO{
		Stdin:    os.Stdin,
		Stdout:   os.Stdout,
		Stderr:   os.Stderr,
		Terminal: systemTerminal{},
	}
}

type systemTerminal struct{}

func (systemTerminal) IsTerminal(descriptor int) bool { return term.IsTerminal(descriptor) }

func (systemTerminal) ReadPassword(ctx context.Context, descriptor int) ([]byte, error) {
	return readTerminalPasswordContext(ctx, descriptor)
}

func (streams IO) stdin() io.Reader {
	if streams.Stdin == nil {
		return emptyReader{}
	}
	return streams.Stdin
}

func (streams IO) stdout() io.Writer {
	if streams.Stdout == nil {
		return io.Discard
	}
	return streams.Stdout
}

func (streams IO) stderr() io.Writer {
	if streams.Stderr == nil {
		return io.Discard
	}
	return streams.Stderr
}

// terminal fails closed: an invocation that supplied no terminal adapter is not
// interactive, so a command that needs a password must be told to read it from
// standard input instead of silently prompting nowhere.
func (streams IO) terminal() Terminal {
	if streams.Terminal == nil {
		return absentTerminal{}
	}
	return streams.Terminal
}

type emptyReader struct{}

func (emptyReader) Read([]byte) (int, error) { return 0, io.EOF }

type absentTerminal struct{}

func (absentTerminal) IsTerminal(int) bool { return false }

func (absentTerminal) ReadPassword(context.Context, int) ([]byte, error) {
	return nil, errNoTerminal
}
