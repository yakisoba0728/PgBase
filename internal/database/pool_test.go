package database

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
)

func TestConfigureSessionPinsSearchPathBeforeOtherSettings(t *testing.T) {
	recorder := &sessionSettingsRecorder{}
	if err := configureSession(context.Background(), recorder.Exec, 1500*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"set search_path = pg_catalog",
		"set application_name = 'pgbase'",
		"set timezone = 'UTC'",
		"select pg_catalog.set_config('statement_timeout', $1, false)",
	}
	if !reflect.DeepEqual(recorder.statements, want) {
		t.Fatalf("session settings = %#v, want %#v", recorder.statements, want)
	}
	if len(recorder.arguments) != 4 || len(recorder.arguments[3]) != 1 || recorder.arguments[3][0] != "1500" {
		t.Fatalf("session setting arguments = %#v, want final timeout 1500ms", recorder.arguments)
	}
}

func TestConfigureSessionFailsClosedWhenSearchPathCannotBePinned(t *testing.T) {
	sentinel := errors.New("setting rejected")
	recorder := &sessionSettingsRecorder{failAt: 1, err: sentinel}
	err := configureSession(context.Background(), recorder.Exec, time.Second)
	if !errors.Is(err, sentinel) || !strings.Contains(err.Error(), "safe search path") {
		t.Fatalf("configureSession() error = %v, want wrapped search-path failure", err)
	}
	if len(recorder.statements) != 1 {
		t.Fatalf("session executed %d settings after search-path failure, want 1", len(recorder.statements))
	}
}

type sessionSettingsRecorder struct {
	statements []string
	arguments  [][]any
	failAt     int
	err        error
}

func (r *sessionSettingsRecorder) Exec(_ context.Context, statement string, arguments ...any) (pgconn.CommandTag, error) {
	r.statements = append(r.statements, statement)
	r.arguments = append(r.arguments, append([]any(nil), arguments...))
	if r.failAt == len(r.statements) {
		return pgconn.CommandTag{}, r.err
	}
	return pgconn.CommandTag{}, nil
}

func TestParsePoolConfigRequiresHeadroomForBackgroundAndRequestWork(t *testing.T) {
	_, err := parsePoolConfig(
		"postgres://postgres:postgres@127.0.0.1/pgbase?sslmode=disable&pool_max_conns=3",
		false,
	)
	if err == nil || !strings.Contains(err.Error(), "at least 4") {
		t.Fatalf("parsePoolConfig() error = %v, want minimum pool size rejection", err)
	}
	if got := Classify(err); got != KindValidation {
		t.Fatalf("Classify(parsePoolConfig error) = %v, want validation", got)
	}
}

func TestParsePoolConfigRequiresVerifiedTLSForEveryRemoteTarget(t *testing.T) {
	t.Setenv("PGSSLMODE", "")
	tests := []struct {
		name          string
		url           string
		allowInsecure bool
		wantErr       bool
	}{
		{
			name:    "remote default prefer",
			url:     "postgres://postgres:postgres@db.example/pgbase?pool_max_conns=4",
			wantErr: true,
		},
		{
			name:    "remote require without identity verification",
			url:     "postgres://postgres:postgres@db.example/pgbase?sslmode=require&pool_max_conns=4",
			wantErr: true,
		},
		{
			name:    "remote verify ca without hostname verification",
			url:     "postgres://postgres:postgres@db.example/pgbase?sslmode=verify-ca&pool_max_conns=4",
			wantErr: true,
		},
		{
			name: "remote verify full",
			url:  "postgres://postgres:postgres@db.example/pgbase?sslmode=verify-full&pool_max_conns=4",
		},
		{
			name: "every remote fallback verifies its host",
			url:  "postgres://postgres:postgres@db-a.example:5432,db-b.example:5432/pgbase?sslmode=verify-full&pool_max_conns=4",
		},
		{
			name:    "remote plaintext fallback behind local primary",
			url:     "postgres://postgres:postgres@127.0.0.1:5432,db.example:5432/pgbase?sslmode=disable&pool_max_conns=4",
			wantErr: true,
		},
		{
			name:    "query host cannot hide behind local URL authority",
			url:     "postgres://postgres:postgres@127.0.0.1/pgbase?host=db.example&sslmode=disable&pool_max_conns=4",
			wantErr: true,
		},
		{
			name: "loopback development database",
			url:  "postgres://postgres:postgres@127.0.0.1/pgbase?sslmode=disable&pool_max_conns=4",
		},
		{
			name: "local Unix socket",
			url:  "postgres:///pgbase?host=/var/run/postgresql&sslmode=disable&pool_max_conns=4",
		},
		{
			name:          "explicit trusted development network",
			url:           "postgres://postgres:postgres@postgres/pgbase?sslmode=disable&pool_max_conns=4",
			allowInsecure: true,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := parsePoolConfig(test.url, test.allowInsecure)
			if test.wantErr && (err == nil || !strings.Contains(err.Error(), "sslmode=verify-full")) {
				t.Fatalf("parsePoolConfig() error = %v, want verified-TLS rejection", err)
			}
			if !test.wantErr && err != nil {
				t.Fatalf("parsePoolConfig() error = %v, want nil", err)
			}
		})
	}
}

func TestParsePoolConfigTreatsUnixSocketAsLocal(t *testing.T) {
	config, err := parsePoolConfig(
		"postgres:///pgbase?host=/var/run/postgresql&sslmode=disable&pool_max_conns=4",
		false,
	)
	if err != nil {
		t.Fatal(err)
	}
	if got := config.ConnConfig.Host; got != "/var/run/postgresql" {
		t.Fatalf("parsed host = %q, want Unix socket directory", got)
	}
	if network, _ := pgconn.NetworkAddress(config.ConnConfig.Host, config.ConnConfig.Port); network != "unix" {
		t.Fatalf("parsed network = %q, want unix", network)
	}
}

func TestRemoteTransportPolicyErrorDoesNotEchoTheDatabaseURL(t *testing.T) {
	const secret = "transport-policy-secret-must-not-leak"
	_, err := parsePoolConfig(
		"postgres://admin:"+secret+"@db.example/pgbase?sslmode=disable&pool_max_conns=4",
		false,
	)
	if err == nil {
		t.Fatal("parsePoolConfig() error = nil, want transport-policy rejection")
	}
	if strings.Contains(err.Error(), secret) || strings.Contains(err.Error(), "postgres://") {
		t.Fatalf("transport-policy error leaked the database URL: %q", err)
	}
}

func TestNilPoolStatsIsZeroSnapshot(t *testing.T) {
	var pool *Pool
	if got := pool.Stats(); got != (PoolStats{}) {
		t.Fatalf("Stats() = %+v, want zero snapshot", got)
	}
}

func TestPostgresDurationRoundsPositiveSubMillisecondValuesUpWithoutGoUnits(t *testing.T) {
	tests := []struct {
		duration time.Duration
		want     string
	}{
		{duration: 0, want: "0"},
		{duration: 100 * time.Microsecond, want: "1"},
		{duration: time.Millisecond, want: "1"},
		{duration: 90 * time.Second, want: "90000"},
	}
	for _, test := range tests {
		if got := postgresDuration(test.duration); got != test.want {
			t.Fatalf("postgresDuration(%s) = %q, want %q", test.duration, got, test.want)
		}
	}
}
