//go:build integration

package cli

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/webisnoteasy/PgBase/internal/testutil"
	"github.com/webisnoteasy/PgBase/pgbase"
)

func TestJavaScriptWatchKeepsValidGenerationAndDoesNotRunMigrations(t *testing.T) {
	directory := t.TempDir()
	manifest := `{
 "format":"pgbase.scripts/v1",
 "routes":[{"method":"GET","pattern":"/watch","access":"public","handler":{"module":"app.js","export":"route"}}],
 "migrations":[{"id":"202609100010_watch_must_not_run","module":"app.js","up":"up","down":"down"}]
}`
	if err := os.WriteFile(filepath.Join(directory, "pgbase.scripts.json"), []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}
	writeWatchSource(t, directory, "one")
	address := watchUnusedAddress(t)
	options := pgbase.Options{DatabaseURL: testutil.NewDatabase(t), HTTPAddress: address, TokenSecret: []byte("javascript-watch-secret-32-bytes-minimum"),
		AutoMigrate: true, ShutdownTimeout: 2 * time.Second, Logger: slog.New(slog.NewJSONHandler(io.Discard, nil)),
		JavaScript: &pgbase.JavaScriptOptions{Directory: directory}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var stdout, stderr bytes.Buffer
	result := make(chan error, 1)
	go func() {
		result <- superviseJavaScript(ctx, options, 50*time.Millisecond, testIO(nil, &stdout, &stderr))
	}()
	endpoint := "http://" + address + "/watch"
	waitWatchResponse(t, endpoint, `{"version":"one"}`)
	if err := os.WriteFile(filepath.Join(directory, "app.js"), []byte(`exports.route=()=>{`), 0o600); err != nil {
		t.Fatal(err)
	}
	time.Sleep(150 * time.Millisecond)
	waitWatchResponse(t, endpoint, `{"version":"one"}`)
	if !strings.Contains(stderr.String(), "reload rejected") {
		t.Fatalf("invalid generation status = %q", stderr.String())
	}
	writeWatchSource(t, directory, "two")
	waitWatchResponse(t, endpoint, `{"version":"two"}`)
	if strings.Count(stdout.String(), "JavaScript generation") < 2 {
		t.Fatalf("generation output = %q", stdout.String())
	}
	connection, err := pgx.Connect(ctx, options.DatabaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close(context.Background())
	var migrationTable *string
	if err := connection.QueryRow(ctx, `select to_regclass('public.watch_should_not_exist')::text`).Scan(&migrationTable); err != nil {
		t.Fatal(err)
	}
	if migrationTable != nil {
		t.Fatalf("watch unexpectedly ran migration: %s", *migrationTable)
	}
	cancel()
	select {
	case err := <-result:
		if err != nil {
			t.Fatalf("watch shutdown = %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("watch did not shut down")
	}
}

func writeWatchSource(t *testing.T, directory, version string) {
	t.Helper()
	source := `exports.route=()=>({status:200,json:{version:"` + version + `"}}); exports.up=function(){const pg=require("pgbase");pg.migration.exec({sql:"create table watch_should_not_exist(value text)"});return {};}; exports.down=()=>({});`
	if err := os.WriteFile(filepath.Join(directory, "app.js"), []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
}

func waitWatchResponse(t *testing.T, endpoint, expected string) {
	t.Helper()
	deadline := time.Now().Add(4 * time.Second)
	for {
		response, err := http.Get(endpoint)
		if err == nil {
			body, readErr := io.ReadAll(response.Body)
			_ = response.Body.Close()
			if readErr == nil && response.StatusCode == http.StatusOK && strings.TrimSpace(string(body)) == expected {
				return
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("watch response never became %s: %v", expected, err)
		}
		time.Sleep(25 * time.Millisecond)
	}
}

func watchUnusedAddress(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	return address
}
