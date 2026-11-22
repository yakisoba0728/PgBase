package pgbase

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/yakisoba0728/PgBase/internal/backup"
)

type staticBackupSettings struct {
	settings backup.StorageSettings
	err      error
}

func (s staticBackupSettings) Settings(context.Context) (backup.StorageSettings, error) {
	return s.settings, s.err
}

func TestStartupWarnsAboutPlaintextBackupEndpointOnly(t *testing.T) {
	for _, testCase := range []struct {
		name   string
		reader staticBackupSettings
		want   bool
	}{
		{"plaintext", staticBackupSettings{settings: backup.StorageSettings{Driver: "s3", Endpoint: "http://minio:9000", PlaintextEndpoint: true}}, true},
		{"private", staticBackupSettings{settings: backup.StorageSettings{Driver: "s3", Endpoint: "http://10.0.0.5:9000"}}, false},
		{"unreadable", staticBackupSettings{err: errors.New("unavailable")}, false},
	} {
		var logs bytes.Buffer
		options := Options{Logger: slog.New(slog.NewJSONHandler(&logs, nil))}
		warnPlaintextBackupEndpoint(context.Background(), testCase.reader, options)
		got := strings.Contains(logs.String(), `"level":"WARN"`) && strings.Contains(logs.String(), "travel unencrypted")
		if got != testCase.want {
			t.Fatalf("%s: logs=%q, want warning=%v", testCase.name, logs.String(), testCase.want)
		}
	}
	// A nil logger is tolerated rather than panicking during startup.
	warnPlaintextBackupEndpoint(context.Background(), staticBackupSettings{settings: backup.StorageSettings{PlaintextEndpoint: true}}, Options{})
}
