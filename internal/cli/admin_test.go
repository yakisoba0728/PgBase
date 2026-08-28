package cli

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/webisnoteasy/PgBase/internal/adminauth"
	"github.com/webisnoteasy/PgBase/internal/database"
	"github.com/webisnoteasy/PgBase/internal/migrate"
	"github.com/webisnoteasy/PgBase/internal/testutil"
)

func TestAdminCreateReadsConfirmedTerminalPasswordWithoutEchoingIt(t *testing.T) {
	databaseURL := testutil.NewDatabase(t)
	prepareAdminDatabase(t, databaseURL)
	originalTerminal, originalReader, originalStdin := terminalInput, passwordInput, commandStdin
	t.Cleanup(func() { terminalInput, passwordInput, commandStdin = originalTerminal, originalReader, originalStdin })
	terminalInput = func(int) bool { return true }
	passwords := [][]byte{[]byte("correct horse battery staple"), []byte("correct horse battery staple")}
	passwordInput = func(int) ([]byte, error) {
		password := passwords[0]
		passwords = passwords[1:]
		return password, nil
	}
	commandStdin = fakeTerminal{}
	var stdout, stderr bytes.Buffer
	code := Run(context.Background(), []string{"admin", "create", "--email", "ADMIN@example.com"}, migrationEnv(databaseURL), &stdout, &stderr, DefaultCommands())
	if code != 0 {
		t.Fatalf("exit=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if strings.Contains(stdout.String()+stderr.String(), "correct horse battery staple") {
		t.Fatal("password was echoed")
	}
	if !strings.Contains(stdout.String(), "administrator created") {
		t.Fatalf("stdout = %q", stdout.String())
	}
}

func TestAdminCreateReadsOneNewlineTerminatedPasswordFromStandardInput(t *testing.T) {
	databaseURL := testutil.NewDatabase(t)
	prepareAdminDatabase(t, databaseURL)
	originalStdin := commandStdin
	commandStdin = strings.NewReader("correct horse battery staple\n")
	t.Cleanup(func() { commandStdin = originalStdin })
	var stdout, stderr bytes.Buffer
	code := Run(context.Background(), []string{"admin", "create", "--email", "admin@example.com", "--password-stdin"}, migrationEnv(databaseURL), &stdout, &stderr, DefaultCommands())
	if code != 0 {
		t.Fatalf("exit=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if strings.Contains(stdout.String()+stderr.String(), "correct horse battery staple") {
		t.Fatal("password was echoed")
	}
}

func TestAdminCreateDuplicateReturnsOperationalExitCode(t *testing.T) {
	databaseURL := testutil.NewDatabase(t)
	prepareAdminDatabase(t, databaseURL)
	originalStdin := commandStdin
	t.Cleanup(func() { commandStdin = originalStdin })

	commandStdin = strings.NewReader("correct horse battery staple\n")
	if code := Run(context.Background(), []string{"admin", "create", "--email", "admin@example.com", "--password-stdin"}, migrationEnv(databaseURL), io.Discard, io.Discard, DefaultCommands()); code != 0 {
		t.Fatalf("first create exit=%d", code)
	}
	commandStdin = strings.NewReader("correct horse battery staple\n")
	var stdout, stderr bytes.Buffer
	code := Run(context.Background(), []string{"admin", "create", "--email", "admin@example.com", "--password-stdin"}, migrationEnv(databaseURL), &stdout, &stderr, DefaultCommands())
	if code != 1 || stdout.Len() != 0 || stderr.String() != "pgbase: command failed\n" {
		t.Fatalf("duplicate create exit=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
}

func TestAdminCreateRejectsMismatchedTerminalPasswords(t *testing.T) {
	originalTerminal, originalReader, originalStdin := terminalInput, passwordInput, commandStdin
	t.Cleanup(func() { terminalInput, passwordInput, commandStdin = originalTerminal, originalReader, originalStdin })
	terminalInput = func(int) bool { return true }
	passwords := [][]byte{[]byte("correct horse battery staple"), []byte("different horse battery staple")}
	passwordInput = func(int) ([]byte, error) {
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

func TestAdminResetPasswordUsesStandardInputAndDoesNotDiscloseIt(t *testing.T) {
	databaseURL := testutil.NewDatabase(t)
	prepareAdminDatabase(t, databaseURL)
	pool, err := database.Open(context.Background(), databaseURL, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	service := adminauth.NewService(pool, adminauth.NewArgon2idPasswords(), []byte("0123456789abcdef0123456789abcdef"), "", time.Hour, nil)
	if _, err := service.CreateAdministrator(context.Background(), "admin@example.com", "correct horse battery staple"); err != nil {
		t.Fatal(err)
	}
	originalStdin := commandStdin
	commandStdin = strings.NewReader("new correct horse battery staple\n")
	t.Cleanup(func() { commandStdin = originalStdin })
	var stdout, stderr bytes.Buffer
	code := Run(context.Background(), []string{"admin", "reset-password", "--email", "admin@example.com", "--password-stdin"}, migrationEnv(databaseURL), &stdout, &stderr, DefaultCommands())
	if code != 0 {
		t.Fatalf("exit=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	for _, secret := range []string{"new correct horse battery staple", "correct horse battery staple"} {
		if strings.Contains(stdout.String()+stderr.String(), secret) {
			t.Fatalf("password leaked in command output: %q", secret)
		}
	}
	if _, err := service.Login(context.Background(), "admin@example.com", "new correct horse battery staple", "reset-cli-client"); err != nil {
		t.Fatalf("login with reset password: %v", err)
	}
}

func TestAdminResetPasswordRejectsInvalidUsageWithExitCodeTwo(t *testing.T) {
	var stderr bytes.Buffer
	code := Run(context.Background(), []string{"admin", "reset-password", "--password-stdin"}, emptyEnv, io.Discard, &stderr, DefaultCommands())
	if code != 2 || !strings.Contains(stderr.String(), "usage: pgbase admin reset-password") {
		t.Fatalf("exit=%d stderr=%q", code, stderr.String())
	}
}

func TestAdminResetPasswordReturnsOperationalExitCodeForUnknownAdministrator(t *testing.T) {
	databaseURL := testutil.NewDatabase(t)
	prepareAdminDatabase(t, databaseURL)
	originalStdin := commandStdin
	commandStdin = strings.NewReader("new correct horse battery staple\n")
	t.Cleanup(func() { commandStdin = originalStdin })
	var stdout, stderr bytes.Buffer
	code := Run(context.Background(), []string{"admin", "reset-password", "--email", "unknown@example.com", "--password-stdin"}, migrationEnv(databaseURL), &stdout, &stderr, DefaultCommands())
	if code != 1 || stderr.String() != "pgbase: command failed\n" {
		t.Fatalf("exit=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if strings.Contains(stdout.String()+stderr.String(), "new correct horse battery staple") {
		t.Fatal("password leaked in command output")
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

func TestCreateAdministratorCreatesNoSessionAndRejectsDuplicates(t *testing.T) {
	pool := newAdminCreatePool(t)
	service := adminauth.NewService(pool, adminauth.NewArgon2idPasswords(), []byte("0123456789abcdef0123456789abcdef"), "", time.Hour, nil)
	created, err := service.CreateAdministrator(context.Background(), " ADMIN@Example.COM ", "correct horse battery staple")
	if err != nil || created.Email != "admin@example.com" || !created.Active || created.ID.Version() != 7 {
		t.Fatalf("created=%#v err=%v", created, err)
	}
	var sessions int
	if err := pool.QueryRow(context.Background(), "select count(*) from pgbase_meta.admin_sessions").Scan(&sessions); err != nil || sessions != 0 {
		t.Fatalf("sessions=%d err=%v, want zero", sessions, err)
	}
	if _, err := service.CreateAdministrator(context.Background(), "admin@example.com", "correct horse battery staple"); !errors.Is(err, adminauth.ErrConflict) {
		t.Fatalf("duplicate creation error=%v, want conflict", err)
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
