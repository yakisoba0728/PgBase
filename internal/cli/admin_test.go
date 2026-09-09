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
	terminal := scriptedPasswords("correct horse battery staple", "different horse battery staple")
	var stderr bytes.Buffer
	code := Execute(context.Background(), []string{"admin", "create", "--email", "admin@example.com"}, emptyEnv,
		terminalIO(terminal, io.Discard, &stderr))
	if code != 2 || !strings.Contains(stderr.String(), "password confirmation") {
		t.Fatalf("exit=%d stderr=%q", code, stderr.String())
	}
}

func TestAdminResetPasswordRejectsInvalidUsageWithExitCodeTwo(t *testing.T) {
	var stderr bytes.Buffer
	code := Execute(context.Background(), []string{"admin", "reset-password", "--password-stdin"}, emptyEnv, testIO(nil, io.Discard, &stderr))
	if code != 2 || !strings.Contains(stderr.String(), "usage: pgbase admin reset-password") {
		t.Fatalf("exit=%d stderr=%q", code, stderr.String())
	}
}

func TestAdminN3CommandsRejectIncompletePolicyAndMutationUsage(t *testing.T) {
	for _, args := range [][]string{
		{"admin", "update", "--password-stdin"},
		{"admin", "delete"},
		{"admin", "otp", "enable", "--email", "admin@example.com"},
		{"admin", "ip", "allow", "--email", "admin@example.com"},
		{"admin", "ip", "unknown", "--email", "admin@example.com"},
	} {
		var stderr bytes.Buffer
		if code := Execute(context.Background(), args, emptyEnv, testIO(nil, io.Discard, &stderr)); code != 2 || !strings.Contains(stderr.String(), "usage:") {
			t.Fatalf("args=%q code=%d stderr=%q", args, code, stderr.String())
		}
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
