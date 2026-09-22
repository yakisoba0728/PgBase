//go:build integration && multiprocess

package pgbase

import (
	"context"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/yakisoba0728/PgBase/internal/backup"
	"github.com/yakisoba0728/PgBase/internal/testutil"
)

func TestAdministrationSDKsAgainstRealHTTPServer(t *testing.T) {
	databaseURL := testutil.NewDatabase(t)
	binary := buildMultiProcessBinary(t)
	address := reserveMultiProcessAddress(t)
	backupDirectory := t.TempDir()
	server := startSDKAcceptanceServer(t, binary, databaseURL, address, backupDirectory)
	defer server.stop(t)
	baseURL := "http://" + address
	multiProcessSetup(t, baseURL)

	archive := makeSDKAcceptanceArchive(t)
	environment := map[string]string{
		"PGBASE_ACCEPTANCE_URL": baseURL, "PGBASE_ACCEPTANCE_EMAIL": publicAdminEmail,
		"PGBASE_ACCEPTANCE_PASSWORD": publicAdminPass, "PGBASE_ACCEPTANCE_ARCHIVE": archive,
		"PGBASE_ACCEPTANCE_ORIGIN": "https://pgbase.test",
	}
	t.Run("TypeScript external consumer", func(t *testing.T) {
		npm := "npm"
		if runtime.GOOS == "windows" {
			npm = "npm.cmd"
		}
		runSDKAcceptanceCommand(t, 90*time.Second, "install TypeScript SDK dependencies", "", nil, npm, "ci", "--ignore-scripts", "--prefix", "sdk/typescript")
		runSDKAcceptanceCommand(t, 90*time.Second, "build TypeScript SDK", "", nil, npm, "run", "build", "--prefix", "sdk/typescript")
		script, err := filepath.Abs(filepath.Join("..", "tests", "sdk-acceptance", "admin_http.mjs"))
		if err != nil {
			t.Fatal(err)
		}
		output := runSDKAcceptanceCommand(t, 90*time.Second, "external TypeScript consumer", "", environment, "node", script)
		if string(output) != "sdk-admin-http-acceptance: ok\n" {
			t.Fatalf("unexpected TypeScript consumer output %q", output)
		}
	})
	t.Run("Dart external consumer", func(t *testing.T) {
		runSDKAcceptanceCommand(t, 90*time.Second, "resolve Dart SDK dependencies", filepath.Join("..", "sdk", "dart"), nil, "dart", "pub", "get")
		tool, err := filepath.Abs(filepath.Join("..", "sdk", "dart", "tool", "admin_http_acceptance.dart"))
		if err != nil {
			t.Fatal(err)
		}
		output := runSDKAcceptanceCommand(t, 90*time.Second, "external Dart consumer", filepath.Join("..", "sdk", "dart"), environment, "dart", "run", tool)
		if string(output) != "sdk-admin-http-acceptance: ok\n" {
			t.Fatalf("unexpected Dart consumer output %q", output)
		}
	})
}

func runSDKAcceptanceCommand(t *testing.T, timeout time.Duration, label, directory string, environment map[string]string, executable string, arguments ...string) []byte {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	command := exec.CommandContext(ctx, executable, arguments...)
	command.Dir = ".."
	if directory != "" {
		command.Dir = directory
	}
	command.Env = multiProcessEnvironment(environment)
	output, err := command.CombinedOutput()
	if ctx.Err() != nil {
		t.Fatalf("%s exceeded %s\n%s", label, timeout, output)
	}
	if err != nil {
		t.Fatalf("%s: %v\n%s", label, err, output)
	}
	return output
}

func startSDKAcceptanceServer(t *testing.T, binary, databaseURL, address, backups string) *multiProcessServer {
	t.Helper()
	logs := &lockedBuffer{}
	command := exec.Command(binary, "serve")
	command.Env = multiProcessEnvironment(map[string]string{
		"PGBASE_DATABASE_URL": databaseURL, "PGBASE_HTTP_ADDRESS": address,
		"PGBASE_PUBLIC_URL": "https://pgbase.test", "PGBASE_SETUP_TOKEN": publicSetupToken,
		"PGBASE_TOKEN_SECRET": "c2RrLWFjY2VwdGFuY2UtdG9rZW4tc2VjcmV0LTMyLWJ5dGVz",
		"PGBASE_AUTO_MIGRATE": "true", "PGBASE_ALLOW_INSECURE_DATABASE": "true",
		"PGBASE_BACKUPS_DIRECTORY": backups,
	})
	command.Stdout, command.Stderr = logs, logs
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	server := &multiProcessServer{command: command, logs: logs}
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		response, err := httpClient300ms.Get("http://" + address + "/health/ready")
		if err == nil {
			response.Body.Close()
			if response.StatusCode == 200 {
				return server
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	server.kill(t)
	t.Fatalf("SDK acceptance server was not ready: %s", logs.String())
	return nil
}

var httpClient300ms = &http.Client{Timeout: 300 * time.Millisecond}

func makeSDKAcceptanceArchive(t *testing.T) string {
	t.Helper()
	directory := t.TempDir()
	if err := os.Mkdir(filepath.Join(directory, "files"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "database.dump"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	manifest := `{"format":1,"createdAt":"2026-09-22T00:00:00Z","postgresqlMajor":17,"database":{"path":"database.dump","sha256":"e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855","size":0},"files":[]}`
	if err := os.WriteFile(filepath.Join(directory, "manifest.json"), []byte(manifest), 0600); err != nil {
		t.Fatal(err)
	}
	archivePath := filepath.Join(t.TempDir(), "sdk-acceptance.tar.gz")
	file, err := os.OpenFile(archivePath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	err = backup.Pack(context.Background(), directory, file)
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		t.Fatal(err)
	}
	return archivePath
}
