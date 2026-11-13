package cli

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMigrationDownCountPassesInvocationValidation(t *testing.T) {
	err := runUserMigration(context.Background(), []string{"down", "2"}, emptyEnv, testIO(nil, io.Discard, io.Discard))
	if err == nil || strings.Contains(err.Error(), "invalid user migration usage") {
		t.Fatalf("down count rejected by invocation validation: %v", err)
	}
}
func TestPartialTLSFlagKeepsEnvironmentCertificateAndACMEOptions(t *testing.T) {
	root := t.TempDir()
	cert := filepath.Join(root, "cert.pem")
	key := filepath.Join(root, "key.pem")
	for _, path := range []string{cert, key} {
		if err := os.WriteFile(path, []byte("fixture"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	for _, mode := range []string{"static", "acme"} {
		t.Run(mode, func(t *testing.T) {
			values := map[string]string{"PGBASE_HTTPS_ADDRESS": "127.0.0.1:8443"}
			if mode == "static" {
				values["PGBASE_HTTPS_CERT_FILE"] = cert
				values["PGBASE_HTTPS_KEY_FILE"] = key
			} else {
				values["PGBASE_ACME_HOSTS"] = "example.test"
				values["PGBASE_ACME_CACHE_DIR"] = root
				values["PGBASE_ACME_HTTP_ADDRESS"] = "127.0.0.1:8080"
			}
			base := migrationEnv("postgres://fixture:fixture@127.0.0.1:1/pgbase?sslmode=disable&connect_timeout=1")
			env := func(name string) (string, bool) {
				if v, ok := values[name]; ok {
					return v, true
				}
				return base(name)
			}
			err := runServe(context.Background(), []string{"--https-address", "127.0.0.1:9443"}, env, testIO(nil, io.Discard, io.Discard))
			if err == nil || strings.Contains(err.Error(), "TLS") || !strings.Contains(err.Error(), "connect") {
				t.Fatalf("partial flag damaged TLS environment: %v", err)
			}
		})
	}
}
func TestInvalidActiveRestoreReceiptRejectsBeforeDatabaseOpen(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "active-target.json"), []byte(`{"databaseUrl":""}`), 0600); err != nil {
		t.Fatal(err)
	}
	base := migrationEnv("postgres://fixture:fixture@127.0.0.1:1/pgbase?sslmode=disable")
	env := func(name string) (string, bool) {
		if name == "PGBASE_BACKUPS_DIRECTORY" {
			return root, true
		}
		return base(name)
	}
	_, err := openDatabase(context.Background(), env, testIO(nil, io.Discard, io.Discard))
	if err == nil || !strings.Contains(err.Error(), "invalid private restore activation receipt") {
		t.Fatalf("invalid target receipt: %v", err)
	}
}
