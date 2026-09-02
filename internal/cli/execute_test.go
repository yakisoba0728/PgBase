package cli

import (
	"bytes"
	"context"
	"io"
	"strings"
	"sync"
	"testing"
)

// TestConcurrentInvocationsKeepTheirOwnStreamsAndTerminal is the property the
// package's stdin and terminal variables could not provide: two invocations in
// one process must not be able to read each other's password, prompt each
// other's terminal, or write each other's diagnostics.
func TestConcurrentInvocationsKeepTheirOwnStreamsAndTerminal(t *testing.T) {
	const invocations = 8
	type outcome struct {
		password string
		err      error
	}
	results := make([]outcome, invocations)
	var group sync.WaitGroup
	start := make(chan struct{})
	for index := range results {
		group.Add(1)
		go func(index int) {
			defer group.Done()
			<-start
			want := strings.Repeat("password-", index+1)
			var streams IO
			if index%2 == 0 {
				streams = testIO(strings.NewReader(want+"\n"), io.Discard, io.Discard)
			} else {
				streams = terminalIO(scriptedPasswords(want, want), io.Discard, io.Discard)
			}
			password, err := readAdministratorPassword(context.Background(), streams, index%2 == 0)
			results[index] = outcome{password: password, err: err}
		}(index)
	}
	close(start)
	group.Wait()

	for index, got := range results {
		want := strings.Repeat("password-", index+1)
		if got.err != nil || got.password != want {
			t.Fatalf("invocation %d read %q (error %v), want %q", index, got.password, got.err, want)
		}
	}
}

// TestInvocationWithoutATerminalRefusesToPromptInsteadOfUsingTheProcessTerminal
// fails closed: an IO that carries no terminal adapter is not interactive, so
// the command tells the operator to use --password-stdin rather than reaching
// for whatever terminal the process happens to own.
func TestInvocationWithoutATerminalRefusesToPromptInsteadOfUsingTheProcessTerminal(t *testing.T) {
	var stderr bytes.Buffer
	code := Execute(context.Background(), []string{"admin", "create", "--email", "admin@example.com"},
		emptyEnv, IO{Stdin: fakeTerminal{}, Stdout: io.Discard, Stderr: &stderr})
	if code != 2 || !strings.Contains(stderr.String(), "use --password-stdin") {
		t.Fatalf("code=%d stderr=%q", code, stderr.String())
	}
}
