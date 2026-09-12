//go:build integration

package pgbase

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"io"
	"log/slog"
	"math/big"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/yakisoba0728/PgBase/internal/backup"
	"github.com/yakisoba0728/PgBase/internal/database"
	"github.com/yakisoba0728/PgBase/internal/migrate"
	"github.com/yakisoba0728/PgBase/internal/testutil"
)

func TestRunServesHTTPSWithProvidedCertificateAndStopsCleanly(t *testing.T) {
	certificate, key := testTLSCertificate(t)
	address := unusedAddress(t)
	app, err := New(context.Background(), Options{DatabaseURL: testutil.NewDatabase(t), HTTPAddress: unusedAddress(t), TLS: &TLSOptions{Address: address, CertificateFile: certificate, KeyFile: key}, TokenSecret: make([]byte, 32), AutoMigrate: true, Logger: slog.New(slog.NewJSONHandler(io.Discard, nil))})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- app.Run(ctx) }()
	client := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}, Timeout: time.Second}
	defer client.CloseIdleConnections()
	deadline := time.Now().Add(3 * time.Second)
	for {
		response, requestErr := client.Get("https://" + address + "/health/live")
		if requestErr == nil && response.StatusCode == http.StatusOK {
			_ = response.Body.Close()
			break
		}
		if response != nil {
			_ = response.Body.Close()
		}
		if time.Now().After(deadline) {
			t.Fatalf("HTTPS listener did not become live: %v", requestErr)
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Run()=%v", err)
	}
}

func TestManagedBackupFailureAlertsActiveAdministratorsThroughLocalSMTP(t *testing.T) {
	pool, err := database.Open(context.Background(), testutil.NewDatabase(t), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if err := migrate.New(pool, nil).Up(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(context.Background(), `insert into pgbase_meta.admins(id,email,password_hash,active,created_at,updated_at) values($1,$2,'fixture',true,clock_timestamp(),clock_timestamp())`, uuid.New(), "backup-owner@example.test"); err != nil {
		t.Fatal(err)
	}
	address, messages := startN6SMTPFixture(t)
	alerter := newBackupFailureAlerter(pool, Options{SMTP: SMTPOptions{Address: address, From: "sender@example.test", ServerName: "localhost", AllowInsecure: true, Timeout: time.Second}, Logger: slog.Default()})
	if alerter == nil {
		t.Fatal("alert configuration rejected")
	}
	alerter.Notify(context.Background(), backup.Job{Managed: true, Status: "failed", UpdatedAt: time.Now(), Error: "smtp password=not-for-email"})
	select {
	case message := <-messages:
		if !strings.Contains(message, "PgBase managed backup failed") || strings.Contains(message, "not-for-email") {
			t.Fatalf("alert leaked or missing content: %q", message)
		}
	case <-time.After(time.Second):
		t.Fatal("managed backup failure mail was not captured")
	}
}

func testTLSCertificate(t *testing.T) (string, string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{SerialNumber: serial, Subject: pkix.Name{CommonName: "localhost"}, NotBefore: time.Now().Add(-time.Minute), NotAfter: time.Now().Add(time.Hour), DNSNames: []string{"localhost"}, KeyUsage: x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	certificate, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	certificatePath, keyPath := filepath.Join(directory, "cert.pem"), filepath.Join(directory, "key.pem")
	if err := os.WriteFile(certificatePath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certificate}), 0600); err != nil {
		t.Fatal(err)
	}
	private, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: private}), 0600); err != nil {
		t.Fatal(err)
	}
	return certificatePath, keyPath
}
