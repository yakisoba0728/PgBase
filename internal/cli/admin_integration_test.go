//go:build integration

package cli

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/yakisoba0728/PgBase/internal/adminauth"
	"github.com/yakisoba0728/PgBase/internal/database"
	"github.com/yakisoba0728/PgBase/internal/mailer"
	"github.com/yakisoba0728/PgBase/internal/testutil"
)

func TestAdminCreateReadsConfirmedTerminalPasswordWithoutEchoingIt(t *testing.T) {
	databaseURL := testutil.NewDatabase(t)
	prepareAdminDatabase(t, databaseURL)
	terminal := scriptedPasswords("correct horse battery staple", "correct horse battery staple")
	var stdout, stderr bytes.Buffer
	code := Execute(context.Background(), []string{"admin", "create", "--email", "ADMIN@example.com"},
		migrationEnv(databaseURL), terminalIO(terminal, &stdout, &stderr))
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
	var stdout, stderr bytes.Buffer
	code := Execute(context.Background(), []string{"admin", "create", "--email", "admin@example.com", "--password-stdin"},
		migrationEnv(databaseURL), testIO(strings.NewReader("correct horse battery staple\n"), &stdout, &stderr))
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
	if code := Execute(context.Background(), []string{"admin", "create", "--email", "admin@example.com", "--password-stdin"},
		migrationEnv(databaseURL), testIO(strings.NewReader("correct horse battery staple\n"), io.Discard, io.Discard)); code != 0 {
		t.Fatalf("first create exit=%d", code)
	}
	var stdout, stderr bytes.Buffer
	code := Execute(context.Background(), []string{"admin", "create", "--email", "admin@example.com", "--password-stdin"},
		migrationEnv(databaseURL), testIO(strings.NewReader("correct horse battery staple\n"), &stdout, &stderr))
	if code != 1 || stdout.Len() != 0 || !strings.Contains(stderr.String(), "create administrator") {
		t.Fatalf("duplicate create exit=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if strings.Contains(stderr.String(), "correct horse battery staple") {
		t.Fatalf("duplicate create diagnostic leaked the password: %q", stderr.String())
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
	var stdout, stderr bytes.Buffer
	code := Execute(context.Background(), []string{"admin", "reset-password", "--email", "admin@example.com", "--password-stdin"},
		migrationEnv(databaseURL), testIO(strings.NewReader("new correct horse battery staple\n"), &stdout, &stderr))
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

func TestAdminResetPasswordReturnsOperationalExitCodeForUnknownAdministrator(t *testing.T) {
	databaseURL := testutil.NewDatabase(t)
	prepareAdminDatabase(t, databaseURL)
	var stdout, stderr bytes.Buffer
	code := Execute(context.Background(), []string{"admin", "reset-password", "--email", "unknown@example.com", "--password-stdin"},
		migrationEnv(databaseURL), testIO(strings.NewReader("new correct horse battery staple\n"), &stdout, &stderr))
	if code != 1 || !strings.Contains(stderr.String(), "reset administrator password") || strings.Contains(stderr.String(), "create administrator") {
		t.Fatalf("exit=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if strings.Contains(stdout.String()+stderr.String(), "new correct horse battery staple") {
		t.Fatal("password leaked in command output")
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

func TestN3AdminCLIUpsertUpdateDeleteAndPolicies(t *testing.T) {
	databaseURL := testutil.NewDatabase(t)
	prepareAdminDatabase(t, databaseURL)
	env := migrationEnv(databaseURL)
	run := func(args []string, input string) (int, string) {
		var stdout, stderr bytes.Buffer
		code := Execute(context.Background(), args, env, testIO(strings.NewReader(input), &stdout, &stderr))
		if stderr.Len() != 0 {
			t.Fatalf("args=%q stderr=%q", args, stderr.String())
		}
		return code, stdout.String()
	}
	if code, _ := run([]string{"admin", "upsert", "--email", "admin@example.com", "--password-stdin"}, "first correct horse battery staple\n"); code != 0 {
		t.Fatalf("upsert exit=%d", code)
	}
	pool, err := database.Open(context.Background(), databaseURL, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	service := adminauth.NewService(pool, adminauth.NewArgon2idPasswords(), []byte("0123456789abcdef0123456789abcdef"), "", time.Hour, nil)
	session, err := service.Login(context.Background(), "admin@example.com", "first correct horse battery staple", "192.0.2.1")
	if err != nil {
		t.Fatal(err)
	}
	if code, _ := run([]string{"admin", "update", "--email", "admin@example.com", "--password-stdin"}, "second correct horse battery staple\n"); code != 0 {
		t.Fatalf("update exit=%d", code)
	}
	if _, err := service.Authenticate(context.Background(), session.RawToken); !errors.Is(err, adminauth.ErrInvalidCredentials) {
		t.Fatalf("old session error=%v", err)
	}
	if code, _ := run([]string{"admin", "ip", "allow", "--email", "admin@example.com", "--cidr", "192.0.2.0/24"}, ""); code != 0 {
		t.Fatalf("ip allow exit=%d", code)
	}
	if code, output := run([]string{"admin", "ip", "list", "--email", "admin@example.com"}, ""); code != 0 || output != "192.0.2.0/24\n" {
		t.Fatalf("ip list exit=%d output=%q", code, output)
	}
	if code, _ := run([]string{"admin", "otp", "enable", "--email", "admin@example.com", "--secret-stdin"}, "JBSWY3DPEHPK3PXP\n"); code != 0 {
		t.Fatalf("otp enable exit=%d", code)
	}
	if enabled, err := service.TOTPEnabled(context.Background(), "admin@example.com"); err != nil || !enabled {
		t.Fatalf("OTP enabled=%t err=%v", enabled, err)
	}
	var stdout, stderr bytes.Buffer
	if code := Execute(context.Background(), []string{"admin", "delete", "--email", "admin@example.com"}, env, testIO(nil, &stdout, &stderr)); code != 1 {
		t.Fatalf("last delete exit=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if code, _ := run([]string{"admin", "create", "--email", "second@example.com", "--password-stdin"}, "second administrator password phrase\n"); code != 0 {
		t.Fatalf("second create exit=%d", code)
	}
	if code, _ := run([]string{"admin", "delete", "--email", "admin@example.com"}, ""); code != 0 {
		t.Fatalf("delete exit=%d", code)
	}
}

func TestAdminEmailOTPCLIPrintsPBCompatibleReceiptDeliversMailAndCompletes(t *testing.T) {
	databaseURL := testutil.NewDatabase(t)
	prepareAdminDatabase(t, databaseURL)
	address, messages := cliSMTPFixture(t)
	env := func(key string) (string, bool) {
		values := map[string]string{
			"PGBASE_DATABASE_URL": databaseURL,
			"PGBASE_TOKEN_SECRET": "MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY=",
			"PGBASE_PUBLIC_URL":   "http://localhost",
			"PGBASE_SMTP_ADDRESS": address, "PGBASE_SMTP_FROM": "sender@example.test", "PGBASE_SMTP_ALLOW_INSECURE": "true",
		}
		value, ok := values[key]
		return value, ok
	}
	pool, err := database.Open(context.Background(), databaseURL, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	service := adminauth.NewService(pool, adminauth.NewArgon2idPasswords(), []byte("0123456789abcdef0123456789abcdef"), "", time.Hour, nil)
	if _, err := service.CreateAdministrator(context.Background(), "admin@example.test", "correct horse battery staple"); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	if code := Execute(context.Background(), []string{"admin", "otp", "email", "--email", "admin@example.test"}, env, testIO(nil, &stdout, &stderr)); code != 0 {
		t.Fatalf("exit=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "administrator email OTP created") || !strings.Contains(stdout.String(), "├─ Id:") || !strings.Contains(stdout.String(), "├─ Pass:") || !strings.Contains(stdout.String(), "└─ Valid:") {
		t.Fatalf("receipt=%q", stdout.String())
	}
	code := receiptValue(t, stdout.String(), "├─ Pass:")
	id := receiptValue(t, stdout.String(), "├─ Id:")
	select {
	case message := <-messages:
		if !strings.Contains(message, "To: admin@example.test") || !strings.Contains(message, code) {
			t.Fatalf("mail=%q", message)
		}
	case <-time.After(time.Second):
		t.Fatal("CLI did not deliver administrator email OTP")
	}
	sender, err := mailer.NewSMTP(mailer.SMTPConfig{Address: address, From: "sender@example.test", AllowInsecure: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := service.ConfigureEmailOTP(sender, 5*time.Minute); err != nil {
		t.Fatal(err)
	}
	if _, err := service.CompleteEmailOTP(context.Background(), id, code, "192.0.2.1"); err != nil {
		t.Fatalf("complete CLI OTP: %v", err)
	}
}

func receiptValue(t *testing.T, output, label string) string {
	t.Helper()
	for _, line := range strings.Split(output, "\n") {
		if strings.HasPrefix(line, label) {
			return strings.TrimSpace(strings.TrimPrefix(line, label))
		}
	}
	t.Fatalf("receipt missing %s: %q", label, output)
	return ""
}

func cliSMTPFixture(t *testing.T) (string, <-chan string) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	messages := make(chan string, 1)
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				reader, writer := bufio.NewReader(conn), bufio.NewWriter(conn)
				write := func(value string) { _, _ = writer.WriteString(value); _ = writer.Flush() }
				write("220 localhost\r\n")
				for {
					line, err := reader.ReadString('\n')
					if err != nil {
						return
					}
					command := strings.ToUpper(strings.TrimSpace(line))
					switch {
					case strings.HasPrefix(command, "EHLO"), strings.HasPrefix(command, "HELO"), strings.HasPrefix(command, "MAIL FROM"), strings.HasPrefix(command, "RCPT TO"):
						write("250 ok\r\n")
					case command == "DATA":
						write("354 data\r\n")
						var body strings.Builder
						for {
							line, err = reader.ReadString('\n')
							if err != nil {
								return
							}
							if line == ".\r\n" {
								break
							}
							body.WriteString(line)
						}
						messages <- body.String()
						write("250 queued\r\n")
					case command == "QUIT":
						write("221 bye\r\n")
						return
					default:
						write("250 ok\r\n")
					}
				}
			}()
		}
	}()
	return listener.Addr().String(), messages
}
