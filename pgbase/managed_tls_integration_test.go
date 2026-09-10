//go:build integration

package pgbase

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/yakisoba0728/PgBase/internal/backup"
	"github.com/yakisoba0728/PgBase/internal/testutil"
)

func TestRunManagedKeepsHTTPSAcrossRestoreActivation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	databaseURL, directory, address := testutil.NewDatabase(t), t.TempDir(), unusedAddress(t)
	certificate, key := testTLSCertificate(t)
	origin := "https://" + address
	options := Options{DatabaseURL: databaseURL, HTTPAddress: unusedAddress(t), PublicURL: mustManagedURL(t, origin), SetupToken: strings.Repeat("s", 32), TokenSecret: []byte("managed-https-backup-test-token-secret"), AutoMigrate: true, AllowInsecureDatabase: true, Backups: BackupOptions{Directory: directory}, TLS: &TLSOptions{Address: address, CertificateFile: certificate, KeyFile: key}, Logger: slog.Default()}
	client := &http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}}
	defer client.CloseIdleConnections()
	done := make(chan error, 1)
	go func() { done <- RunManaged(ctx, options) }()
	waitManagedLiveWithClient(t, client, origin+"/health/live", done)
	if response, err := (&http.Client{Timeout: time.Second}).Get("http://" + address + "/health/live"); err == nil {
		_ = response.Body.Close()
		if response.StatusCode == http.StatusOK {
			t.Fatal("managed TLS address accepted a plain HTTP health request")
		}
	}
	cookie, csrf := setupManagedAdminWithClient(t, client, origin, options.SetupToken)
	connection, err := pgx.Connect(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = connection.Exec(ctx, `CREATE TABLE app.managed_https_marker(value text NOT NULL); INSERT INTO app.managed_https_marker VALUES('snapshot')`); err != nil {
		t.Fatal(err)
	}
	_ = connection.Close(ctx)
	created := managedJSONWithClient[backup.Job](t, client, http.MethodPost, origin+"/api/admin/backups", nil, cookie, csrf, http.StatusAccepted)
	created = waitManagedJobWithClient(t, client, origin, created.ID, cookie)
	if created.Status != "completed" {
		t.Fatalf("backup job=%#v", created)
	}
	connection, err = pgx.Connect(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = connection.Exec(ctx, `UPDATE app.managed_https_marker SET value='mutated'`); err != nil {
		t.Fatal(err)
	}
	_ = connection.Close(ctx)
	restored := managedJSONWithClient[backup.Job](t, client, http.MethodPost, origin+"/api/admin/backups/"+created.BackupID+"/restore", nil, cookie, csrf, http.StatusAccepted)
	restored = waitManagedJobWithClient(t, client, origin, restored.ID, cookie)
	if restored.Status != "completed" {
		t.Fatalf("restore job=%#v", restored)
	}
	managedBytesWithClient(t, client, http.MethodGet, origin+"/health/live", nil, "", nil, "", http.StatusOK)
	target := managedRestoreTarget(t, directory)
	targetConnection, err := pgx.Connect(ctx, target.DatabaseURL)
	if err != nil {
		t.Fatal(err)
	}
	var value string
	if err = targetConnection.QueryRow(ctx, `SELECT value FROM app.managed_https_marker`).Scan(&value); err != nil || value != "snapshot" {
		t.Fatalf("restored marker=%q err=%v", value, err)
	}
	_ = targetConnection.Close(ctx)
	cancel()
	if err = <-done; err != nil {
		t.Fatalf("managed TLS shutdown: %v", err)
	}
	dropManagedTarget(t, databaseURL, target)
}

func TestRunManagedACMEIssuesCachesAndStopsChallenge(t *testing.T) {
	ca := newMockACME(t)
	databaseURL, directory, cache := testutil.NewDatabase(t), t.TempDir(), t.TempDir()
	httpsAddress, challengeAddress := unusedAddress(t), unusedAddress(t)
	ca.challengeAddress = challengeAddress
	options := func() Options {
		return Options{DatabaseURL: databaseURL, HTTPAddress: unusedAddress(t), TokenSecret: make([]byte, 32), AutoMigrate: true, AllowInsecureDatabase: true, Backups: BackupOptions{Directory: directory}, TLS: &TLSOptions{Address: httpsAddress, ACMEHosts: []string{"acme.test"}, ACMECacheDirectory: cache, HTTPChallengeAddress: challengeAddress, ACMEDirectoryURL: ca.server.URL}, Logger: slog.Default()}
	}
	first := runManagedACME(t, options(), challengeAddress)
	redirect := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }, Timeout: time.Second}
	request, err := http.NewRequest(http.MethodGet, "http://"+challengeAddress+"/ordinary", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Host = "acme.test"
	response, err := redirect.Do(request)
	if err != nil || response.StatusCode != http.StatusTemporaryRedirect || response.Header.Get("Location") != "https://acme.test/ordinary" {
		if response != nil {
			_ = response.Body.Close()
		}
		t.Fatalf("managed ACME redirect response=%v err=%v", response, err)
	}
	_ = response.Body.Close()
	if err := dialTLS(httpsAddress, "unknown.test", ca.roots); err == nil || ca.orders() != 0 {
		t.Fatalf("unknown host err=%v orders=%d", err, ca.orders())
	}
	if err := dialTLS(httpsAddress, "acme.test", ca.roots); err != nil {
		t.Fatalf("managed ACME certificate: %v", err)
	}
	if ca.orders() != 1 || ca.challengeHits() != 1 {
		t.Fatalf("managed ACME orders=%d hits=%d", ca.orders(), ca.challengeHits())
	}
	first.stop(t)
	if connection, err := net.DialTimeout("tcp", challengeAddress, 100*time.Millisecond); err == nil {
		_ = connection.Close()
		t.Fatal("managed ACME challenge listener remained open after shutdown")
	}
	second := runManagedACME(t, options(), challengeAddress)
	if err := dialTLS(httpsAddress, "acme.test", ca.roots); err != nil || ca.orders() != 1 {
		t.Fatalf("managed ACME cache err=%v orders=%d", err, ca.orders())
	}
	second.stop(t)
}

type managedACMERun struct {
	cancel context.CancelFunc
	done   <-chan error
}

func runManagedACME(t *testing.T, options Options, challengeAddress string) managedACMERun {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- RunManaged(ctx, options) }()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if connection, err := net.DialTimeout("tcp", challengeAddress, 20*time.Millisecond); err == nil {
			_ = connection.Close()
			return managedACMERun{cancel: cancel, done: done}
		}
		time.Sleep(time.Millisecond)
	}
	cancel()
	_ = <-done
	t.Fatal("managed ACME challenge listener did not start")
	return managedACMERun{}
}

func (run managedACMERun) stop(t *testing.T) {
	t.Helper()
	run.cancel()
	if err := <-run.done; err != nil {
		t.Fatalf("managed ACME shutdown: %v", err)
	}
}

func managedRestoreTarget(t *testing.T, directory string) RestoreTarget {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(directory, "active-target.json"))
	if err != nil {
		t.Fatal(err)
	}
	var target RestoreTarget
	if err = json.Unmarshal(raw, &target); err != nil {
		t.Fatal(err)
	}
	return target
}

func dropManagedTarget(t *testing.T, databaseURL string, target RestoreTarget) {
	t.Helper()
	parsed, err := url.Parse(target.DatabaseURL)
	if err != nil {
		t.Fatal(err)
	}
	connection, err := pgx.Connect(context.Background(), databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	_, dropErr := connection.Exec(context.Background(), "DROP DATABASE "+pgx.Identifier{strings.TrimPrefix(parsed.Path, "/")}.Sanitize()+" WITH (FORCE)")
	closeErr := connection.Close(context.Background())
	if dropErr != nil || closeErr != nil {
		t.Fatalf("drop managed target: %v / %v", dropErr, closeErr)
	}
}
