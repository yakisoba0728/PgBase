//go:build restore

package restore

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestStockImageBackupToolsVerifyTLS(t *testing.T) {
	image := os.Getenv("PGBASE_RUNTIME_BACKUP_IMAGE")
	if image == "" {
		t.Skip("set PGBASE_RUNTIME_BACKUP_IMAGE to the built stock image")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Minute)
	defer cancel()
	directory := t.TempDir()
	if err := os.Chmod(directory, 0755); err != nil {
		t.Fatal(err)
	}
	runtimeTLSFiles(t, directory)
	network := "pgbase-runtime-tls-" + randomHex(t, 6)
	if out, err := dockerRun(ctx, time.Minute, "network", "create", network); err != nil {
		t.Fatalf("network: %v %s", err, out)
	}
	t.Cleanup(func() { _, _ = dockerRun(context.Background(), time.Minute, "network", "rm", network) })
	p := &postgresServer{container: "pgbase-runtime-tls-db-" + randomHex(t, 6), alias: "postgres", major: "18"}
	reference := postgresMatrix()[2].reference
	if out, err := dockerRun(ctx, time.Minute, "run", "--detach", "--name", p.container, "--network", network, "--network-alias", "postgres", "--network-alias", "wronghost", "--volume", directory+":/tls:ro", "--env", "POSTGRES_USER="+databaseUser, "--env", "POSTGRES_PASSWORD=runtime-local-only", "--env", "POSTGRES_DB="+sourceDatabase, reference, "postgres", "-c", "ssl=on", "-c", "ssl_cert_file=/tls/server.crt", "-c", "ssl_key_file=/tls/server.key"); err != nil {
		t.Fatalf("TLS PostgreSQL: %v %s", err, out)
	}
	t.Cleanup(func() { _, _ = dockerRun(context.Background(), time.Minute, "rm", "--force", "--volumes", p.container) })
	runtimePoll(t, ctx, func() bool {
		_, err := p.exec(ctx, "psql", "--host", "127.0.0.1", "--username", databaseUser, "--dbname", sourceDatabase, "--command", "select 1")
		return err == nil
	})
	p.query(t, ctx, sourceDatabase, `CREATE SCHEMA app; CREATE TABLE app.tls_backup(value text); INSERT INTO app.tls_backup VALUES('verified TLS restores archive bytes')`)
	p.query(t, ctx, sourceDatabase, "CREATE DATABASE "+restoredDatabase)
	output := t.TempDir()
	if err := os.Chmod(output, 0777); err != nil {
		t.Fatal(err)
	}
	command := func(tool, host, ca, database string, arguments ...string) (string, error) {

		args := []string{"run", "--rm", "--read-only", "--tmpfs", "/tmp:rw,noexec,nosuid,size=16777216,mode=1777", "--network", network, "--volume", directory + ":/tls:ro", "--volume", output + ":/backup", "--env", "PGHOST=" + host, "--env", "PGPORT=5432", "--env", "PGUSER=" + databaseUser, "--env", "PGPASSWORD=runtime-local-only", "--env", "PGDATABASE=" + database, "--env", "PGSSLMODE=verify-full", "--env", "PGSSLROOTCERT=/tls/" + ca, "--entrypoint", "/opt/pgbase/postgresql/18/bin/" + tool, image}
		return dockerRun(ctx, time.Minute, append(args, arguments...)...)
	}
	for _, test := range []struct{ host, ca, reason string }{{"postgres", "wrong-ca.crt", "certificate verify failed"}, {"wronghost", "ca.crt", "does not match host name"}} {
		out, err := command("pg_dump", test.host, test.ca, sourceDatabase, "--format=custom", "--schema=app", "--file=/backup/rejected.dump")
		if err == nil || !strings.Contains(out, test.reason) {
			t.Fatalf("untrusted TLS accepted or unexpected failure: %v %s", err, out)
		}
	}
	if out, err := command("pg_dump", "postgres", "ca.crt", sourceDatabase, "--format=custom", "--no-owner", "--no-acl", "--schema=app", "--file=/backup/database.dump"); err != nil {
		t.Fatalf("verified TLS pg_dump: %v %s", err, out)
	}
	if out, err := command("pg_restore", "postgres", "ca.crt", restoredDatabase, "--dbname=", "--single-transaction", "--no-owner", "--no-acl", "--exit-on-error", "/backup/database.dump"); err != nil {
		t.Fatalf("verified TLS pg_restore: %v %s", err, out)
	}
	if got := p.query(t, ctx, restoredDatabase, "select value from app.tls_backup"); got != "verified TLS restores archive bytes" {
		t.Fatalf("restored value=%q", got)
	}
}

func runtimeTLSFiles(t *testing.T, directory string) {
	t.Helper()
	makeCA := func(name string) (*x509.Certificate, *rsa.PrivateKey, []byte) {
		key, err := rsa.GenerateKey(rand.Reader, 2048)
		if err != nil {
			t.Fatal(err)
		}
		certificate := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: name}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature}
		raw, err := x509.CreateCertificate(rand.Reader, certificate, certificate, &key.PublicKey, key)
		if err != nil {
			t.Fatal(err)
		}
		return certificate, key, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: raw})
	}
	ca, key, caPEM := makeCA("runtime fixture CA")
	_, _, wrong := makeCA("wrong fixture CA")
	serverKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	server := &x509.Certificate{SerialNumber: big.NewInt(2), Subject: pkix.Name{CommonName: "postgres"}, DNSNames: []string{"postgres"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	raw, err := x509.CreateCertificate(rand.Reader, server, ca, &serverKey.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	for name, data := range map[string][]byte{"ca.crt": caPEM, "wrong-ca.crt": wrong, "server.crt": pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: raw}), "server.key": pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(serverKey)})} {
		mode := os.FileMode(0644)
		if name == "server.key" {
			mode = 0600
		}
		if err := os.WriteFile(filepath.Join(directory, name), data, mode); err != nil {
			t.Fatal(err)
		}
	}
	// PostgreSQL permits a root-owned 0640 key. Copy through a fixture container
	// so host UID mappings never force an insecure world-readable private key.
	if out, err := dockerRun(t.Context(), time.Minute, "run", "--rm", "--user", "0", "--volume", directory+":/tls", postgresMatrix()[2].reference, "sh", "-c", "chown root:postgres /tls/server.key && chmod 0640 /tls/server.key"); err != nil {
		t.Fatalf("fixture key permissions: %v %s", err, out)
	}
}
