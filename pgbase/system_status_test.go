package pgbase

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/yakisoba0728/PgBase/internal/api"
)

func TestSystemStatusBeforeTheFirstSampleIsUnavailableRatherThanHealthy(t *testing.T) {
	sampler := newSystemStatusSampler(nil, nil, nil, nil)
	status := sampler.Status(context.Background())
	if status.DatabaseReachable || status.ListenerReady || status.MigrationDirty {
		t.Fatalf("unsampled status = %#v, want nothing reported as healthy", status)
	}
	if status.SampledAt != "" || status.LastSuccessAt != "" {
		t.Fatalf("unsampled status claims a sample time: %#v", status)
	}
	wanted := map[string]string{
		systemComponentDatabase:  systemProbeNotSampled,
		systemComponentMigration: systemProbeNotSampled,
		systemComponentCatalog:   systemProbeNotSampled,
		systemComponentListener:  systemProbeNotSampled,
	}
	if len(status.Unavailable) != len(wanted) {
		t.Fatalf("unsampled probes = %#v, want all four", status.Unavailable)
	}
	for _, probe := range status.Unavailable {
		if wanted[probe.Component] != probe.Code {
			t.Fatalf("probe %#v is not reported as unsampled", probe)
		}
	}
}

// TestFailedProbePreservesTheLastSuccessfulReading is the pair of guarantees a
// status page needs at once: the component whose probe failed must not read as
// healthy, and the value it last confirmed must still be visible instead of
// collapsing to a zero nobody can tell apart from a fresh database.
func TestFailedProbePreservesTheLastSuccessfulReading(t *testing.T) {
	sampler := newSystemStatusSampler(nil, nil, nil, &scriptedReadiness{})
	sampler.now = fixedClock("2026-09-01T00:00:10Z")
	sampler.snapshot = api.SystemStatus{
		Version:          Version,
		MigrationCurrent: 7,
		MigrationLatest:  7,
		MigrationDirty:   true,
		CatalogVersion:   4,
		LastSuccessAt:    "2026-08-31T23:59:00Z",
	}

	sampler.sample(context.Background())
	status := sampler.Status(context.Background())

	if status.MigrationCurrent != 7 || status.MigrationLatest != 7 || !status.MigrationDirty {
		t.Fatalf("failed migration probe discarded the last successful reading: %#v", status)
	}
	if status.CatalogVersion != 4 {
		t.Fatalf("failed catalog probe discarded the last successful reading: %#v", status)
	}
	if status.LastSuccessAt != "2026-08-31T23:59:00Z" {
		t.Fatalf("lastSuccessAt = %q, want the preserved success time", status.LastSuccessAt)
	}
	if status.SampledAt != "2026-09-01T00:00:10Z" {
		t.Fatalf("sampledAt = %q, want the attempt time", status.SampledAt)
	}
	for _, component := range []string{systemComponentDatabase, systemComponentMigration, systemComponentCatalog} {
		if !probeReported(status, component) {
			t.Fatalf("component %q is not reported as unavailable: %#v", component, status.Unavailable)
		}
	}
	if probeReported(status, systemComponentListener) || !status.ListenerReady {
		t.Fatalf("the healthy listener probe was reported as unavailable: %#v", status)
	}
}

// TestAFailingProbeIsNeverShownAsHealthy covers the other direction: a boolean
// component whose probe failed goes false and appears in the probe list.
func TestAFailingProbeIsNeverShownAsHealthy(t *testing.T) {
	listener := &scriptedReadiness{err: errors.New("listener is not connected")}
	sampler := newSystemStatusSampler(nil, nil, nil, listener)
	sampler.sample(context.Background())
	status := sampler.Status(context.Background())
	if status.ListenerReady || !probeReported(status, systemComponentListener) {
		t.Fatalf("failed listener probe reads as healthy: %#v", status)
	}
	if status.LastSuccessAt != "" {
		t.Fatalf("a sample with failures recorded a success: %#v", status)
	}
}

// TestRawDatabaseErrorsNeverReachTheStatusPayload keeps the endpoint safe to
// show an administrator: a probe failure is a code, never a diagnostic.
func TestRawDatabaseErrorsNeverReachTheStatusPayload(t *testing.T) {
	const secret = "password=status-probe-secret-must-not-leak"
	listener := &scriptedReadiness{err: errors.New(secret + ": SQLSTATE 42P01 select password_hash")}
	sampler := newSystemStatusSampler(nil, nil, nil, listener)
	sampler.sample(context.Background())

	encoded, err := json.Marshal(sampler.Status(context.Background()))
	if err != nil {
		t.Fatal(err)
	}
	for _, sensitive := range []string{secret, "SQLSTATE", "password_hash", "42P01"} {
		if strings.Contains(string(encoded), sensitive) {
			t.Fatalf("system status leaked %q: %s", sensitive, encoded)
		}
	}
	if !strings.Contains(string(encoded), `"code":"failed"`) {
		t.Fatalf("system status lost the probe classification: %s", encoded)
	}
}

func TestSystemStatusCodesAreClosed(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want string
	}{
		{name: "absent dependency", err: errSystemProbeUnavailable, want: systemProbeUnavailable},
		{name: "deadline", err: context.DeadlineExceeded, want: systemProbeTimeout},
		{name: "cancellation", err: context.Canceled, want: systemProbeTimeout},
		{name: "connection exception", err: &pgconn.PgError{Code: "08006"}, want: systemProbeUnavailable},
		{name: "undefined table", err: &pgconn.PgError{Code: "42P01"}, want: systemProbeFailed},
		{name: "unknown", err: errors.New("boom"), want: systemProbeFailed},
		{name: "healthy", err: nil, want: ""},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := systemStatusCode(test.err); got != test.want {
				t.Fatalf("systemStatusCode(%v) = %q, want %q", test.err, got, test.want)
			}
		})
	}
}

// TestStatusRequestDoesNotWaitForASlowProbe is the latency guarantee: reading
// the cached sample must not block behind a probe that is still running.
func TestStatusRequestDoesNotWaitForASlowProbe(t *testing.T) {
	listener := blockingReadiness()
	sampler := newSystemStatusSampler(nil, nil, nil, listener)
	sampler.probeTimeout = time.Minute

	sampling := make(chan struct{})
	go func() {
		sampler.sample(context.Background())
		close(sampling)
	}()
	<-listener.entered

	done := make(chan api.SystemStatus, 1)
	go func() { done <- sampler.Status(context.Background()) }()
	select {
	case status := <-done:
		if status.ListenerReady {
			t.Fatalf("status served a value the running probe has not produced: %#v", status)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("a status read waited for the in-flight probe")
	}
	close(listener.block)
	<-sampling
}

func probeReported(status api.SystemStatus, component string) bool {
	for _, probe := range status.Unavailable {
		if probe.Component == component {
			return true
		}
	}
	return false
}

func fixedClock(times ...string) func() time.Time {
	index := 0
	return func() time.Time {
		value, err := time.Parse(time.RFC3339Nano, times[index])
		if err != nil {
			panic(err)
		}
		if index < len(times)-1 {
			index++
		}
		return value
	}
}

type scriptedReadiness struct {
	err     error
	block   chan struct{}
	entered chan struct{}
	once    sync.Once
}

func blockingReadiness() *scriptedReadiness {
	return &scriptedReadiness{block: make(chan struct{}), entered: make(chan struct{})}
}

func (r *scriptedReadiness) Ready(ctx context.Context) error {
	if r.block != nil {
		r.once.Do(func() { close(r.entered) })
		select {
		case <-r.block:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return r.err
}
