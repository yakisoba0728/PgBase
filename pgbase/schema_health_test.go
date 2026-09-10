package pgbase

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/yakisoba0728/PgBase/internal/api"
	"github.com/yakisoba0728/PgBase/internal/doctor"
)

func driftReport(codes ...string) doctor.Report {
	report := doctor.Report{}
	for _, code := range codes {
		report.Findings = append(report.Findings, doctor.Finding{
			Severity: "error", Code: code, Message: "A catalog field index is missing",
		})
	}
	return report
}

func TestSchemaHealthBeforeTheFirstRunIsUnavailableRatherThanHealthy(t *testing.T) {
	health := newSchemaHealthSampler(nil, nil).Health(context.Background())
	if health.State != api.SchemaHealthUnavailable {
		t.Fatalf("state = %q, want %q", health.State, api.SchemaHealthUnavailable)
	}
	if health.LastSuccessAt != "" || health.SampledAt != "" {
		t.Fatalf("an undiagnosed schema claims a run time: %#v", health)
	}
	if len(health.Unavailable) != 1 || health.Unavailable[0].Code != systemProbeNotSampled {
		t.Fatalf("unavailable = %#v, want the doctor probe reported as unsampled", health.Unavailable)
	}
}

// A database that was diagnosed and found sound must not read the same as one
// that has never been diagnosed: both carry zero findings, so only the state
// and the success time tell them apart.
func TestSchemaHealthReportsACompletedCleanRunAsHealthy(t *testing.T) {
	sampler := newSchemaHealthSampler(nil, nil)
	sampler.now = fixedClock("2026-09-01T00:05:00Z")

	sampler.observe(doctor.Report{OK: true, Findings: []doctor.Finding{}}, nil)
	health := sampler.Health(context.Background())

	if health.State != api.SchemaHealthHealthy {
		t.Fatalf("state = %q, want %q", health.State, api.SchemaHealthHealthy)
	}
	if health.LastSuccessAt != "2026-09-01T00:05:00Z" || health.SampledAt != "2026-09-01T00:05:00Z" {
		t.Fatalf("a completed clean run did not record its time: %#v", health)
	}
	if health.FindingCount != 0 || len(health.Unavailable) != 0 {
		t.Fatalf("a healthy run reported a problem: %#v", health)
	}
}

// Drift is an answer, not a failure: the run completed, so it resets the retry
// counter and moves the success time forward even though it found problems.
func TestSchemaHealthReportsDriftAsACompletedRun(t *testing.T) {
	sampler := newSchemaHealthSampler(nil, nil)
	sampler.now = fixedClock("2026-09-01T00:10:00Z")
	sampler.snapshot.Retries = 3

	sampler.observe(driftReport("collection_index_missing"), nil)
	health := sampler.Health(context.Background())

	if health.State != api.SchemaHealthDrift {
		t.Fatalf("state = %q, want %q", health.State, api.SchemaHealthDrift)
	}
	if health.Retries != 0 {
		t.Fatalf("retries = %d, want a completed run to clear them", health.Retries)
	}
	if health.LastSuccessAt != "2026-09-01T00:10:00Z" {
		t.Fatalf("lastSuccessAt = %q, want the drift run's own time", health.LastSuccessAt)
	}
	if len(health.Findings) != 1 || health.Findings[0].Code != "collection_index_missing" {
		t.Fatalf("findings = %#v, want the reported code", health.Findings)
	}
}

// A run that could not complete must not overwrite the last real diagnosis
// with an empty one, and must be countable: one missed sample and an hour of
// missed samples look identical without the retry count.
func TestSchemaHealthKeepsTheLastCompletedRunWhileARunFails(t *testing.T) {
	sampler := newSchemaHealthSampler(nil, nil)
	sampler.now = fixedClock("2026-09-01T00:15:00Z", "2026-09-01T00:20:00Z")
	sampler.snapshot = api.SchemaHealth{State: api.SchemaHealthHealthy, LastSuccessAt: "2026-09-01T00:10:00Z"}

	sampler.observe(doctor.Report{}, errors.New("ping PostgreSQL: dial tcp 10.0.0.4:5432: connect: refused"))
	sampler.observe(doctor.Report{}, errors.New("ping PostgreSQL: dial tcp 10.0.0.4:5432: connect: refused"))
	health := sampler.Health(context.Background())

	if health.State != api.SchemaHealthUnavailable {
		t.Fatalf("state = %q, want %q", health.State, api.SchemaHealthUnavailable)
	}
	if health.LastSuccessAt != "2026-09-01T00:10:00Z" {
		t.Fatalf("lastSuccessAt = %q, want the preserved success time", health.LastSuccessAt)
	}
	if health.Retries != 2 {
		t.Fatalf("retries = %d, want 2 consecutive failed attempts", health.Retries)
	}
	if len(health.Unavailable) != 1 || health.Unavailable[0].Component != systemComponentDoctor {
		t.Fatalf("unavailable = %#v, want the doctor probe", health.Unavailable)
	}
}

// The report is rendered for an administrator, so a PostgreSQL diagnostic —
// which can carry a host, a connection string or a SQL fragment — must be
// classified into a code rather than passed through.
func TestSchemaHealthPublishesNoDatabaseDiagnostic(t *testing.T) {
	sampler := newSchemaHealthSampler(nil, nil)
	sampler.observe(doctor.Report{}, errors.New(`connect "postgres://pgbase:hunter2@db.internal:5432/app": refused`))

	encoded, err := json.Marshal(sampler.Health(context.Background()))
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"hunter2", "db.internal", "postgres://", "refused"} {
		if strings.Contains(string(encoded), secret) {
			t.Fatalf("published diagnostic leaks %q: %s", secret, encoded)
		}
	}
}

// Findings carry no identifiers, so their number is bounded by the code set
// this binary defines; the cap is what keeps the payload bounded regardless.
func TestSchemaHealthCapsThePublishedFindings(t *testing.T) {
	codes := make([]string, 0, api.MaximumSchemaFindings+8)
	for index := range cap(codes) {
		codes = append(codes, fmt.Sprintf("synthetic_finding_%d", index))
	}
	sampler := newSchemaHealthSampler(nil, nil)

	sampler.observe(driftReport(codes...), nil)
	health := sampler.Health(context.Background())

	if len(health.Findings) != api.MaximumSchemaFindings {
		t.Fatalf("published findings = %d, want the cap of %d", len(health.Findings), api.MaximumSchemaFindings)
	}
	if health.FindingCount != len(codes) {
		t.Fatalf("findingCount = %d, want the uncapped total %d", health.FindingCount, len(codes))
	}
}

// Health hands back a copy, so a caller iterating the report cannot be racing
// the next background run that replaces it.
func TestSchemaHealthHandsBackACopyOfTheCachedReport(t *testing.T) {
	sampler := newSchemaHealthSampler(nil, nil)
	sampler.observe(driftReport("collection_index_missing"), nil)

	first := sampler.Health(context.Background())
	first.Findings[0].Code = "mutated"
	first.Unavailable = append(first.Unavailable, api.SystemStatusProbe{Component: "injected"})

	second := sampler.Health(context.Background())
	if second.Findings[0].Code != "collection_index_missing" || len(second.Unavailable) != 0 {
		t.Fatalf("a caller mutated the cached report: %#v", second)
	}
}

// A run that never happened is diagnosable without a database: doctor reports
// the unreachable database itself, and the sampler must publish that as an
// unavailable diagnosis rather than as a clean bill of health.
func TestSchemaHealthSampleWithoutAPoolIsUnavailable(t *testing.T) {
	sampler := newSchemaHealthSampler(nil, nil)
	sampler.sample(context.Background())

	health := sampler.Health(context.Background())
	if health.State != api.SchemaHealthUnavailable || health.LastSuccessAt != "" {
		t.Fatalf("a run with no database reads as diagnosed: %#v", health)
	}
	select {
	case <-sampler.sampled:
	default:
		t.Fatal("the first-diagnosis gate did not close")
	}
}
