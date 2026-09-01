package cli

import (
	"bytes"
	"context"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/webisnoteasy/PgBase/internal/database"
	"github.com/webisnoteasy/PgBase/internal/migrate"
	"github.com/webisnoteasy/PgBase/internal/testutil"
)

func TestAdminCreateRejectsMismatchedTerminalPasswords(t *testing.T) {
	originalTerminal, originalReader, originalStdin := terminalInput, terminalPasswordInput, commandStdin
	t.Cleanup(func() {
		terminalInput, terminalPasswordInput, commandStdin = originalTerminal, originalReader, originalStdin
	})
	terminalInput = func(int) bool { return true }
	passwords := [][]byte{[]byte("correct horse battery staple"), []byte("different horse battery staple")}
	terminalPasswordInput = func(context.Context, int) ([]byte, error) {
		password := passwords[0]
		passwords = passwords[1:]
		return password, nil
	}
	commandStdin = fakeTerminal{}
	var stderr bytes.Buffer
	code := Run(context.Background(), []string{"admin", "create", "--email", "admin@example.com"}, emptyEnv, io.Discard, &stderr, DefaultCommands())
	if code != 2 || !strings.Contains(stderr.String(), "password confirmation") {
		t.Fatalf("exit=%d stderr=%q", code, stderr.String())
	}
}

func TestAdminResetPasswordRejectsInvalidUsageWithExitCodeTwo(t *testing.T) {
	var stderr bytes.Buffer
	code := Run(context.Background(), []string{"admin", "reset-password", "--password-stdin"}, emptyEnv, io.Discard, &stderr, DefaultCommands())
	if code != 2 || !strings.Contains(stderr.String(), "usage: pgbase admin reset-password") {
		t.Fatalf("exit=%d stderr=%q", code, stderr.String())
	}
}

type fakeTerminal struct{}

func (fakeTerminal) Read([]byte) (int, error) { return 0, io.EOF }
func (fakeTerminal) Fd() uintptr              { return 0 }

func prepareAdminDatabase(t *testing.T, databaseURL string) {
	t.Helper()
	pool, err := database.Open(context.Background(), databaseURL, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if err := migrate.New(pool, nil).Up(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func newAdminCreatePool(t *testing.T) *database.Pool {
	t.Helper()
	pool, err := database.Open(context.Background(), testutil.NewDatabase(t), 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if err := migrate.New(pool, nil).Up(context.Background()); err != nil {
		t.Fatal(err)
	}
	return pool
}
