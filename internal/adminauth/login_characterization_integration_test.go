//go:build integration

package adminauth

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/webisnoteasy/PgBase/internal/database"
)

type loginSequenceGolden struct {
	Candidates []loginSequenceCandidate `json:"candidates"`
}

type loginSequenceCandidate struct {
	Name                 string   `json:"name"`
	Outcome              string   `json:"outcome"`
	Stages               []string `json:"stages"`
	SessionDelta         int      `json:"sessionDelta"`
	AccountFailures      int      `json:"accountFailures"`
	NetworkFailures      int      `json:"networkFailures"`
	LegacyGlobalFailures int      `json:"legacyGlobalFailures"`
}

func withLoginTrace(ctx context.Context, trace func(loginTraceStage)) context.Context {
	return context.WithValue(ctx, loginTraceContextKey{}, trace)
}

func TestServiceLoginSequencingGolden(t *testing.T) {
	pool := newAdminAuthPool(t)
	clock := &fakeClock{now: time.Date(2026, 8, 28, 1, 2, 3, 0, time.UTC)}
	secret := []byte("0123456789abcdef0123456789abcdef")
	setupToken := "01234567890123456789012345678901"
	passwords := NewArgon2idPasswords()
	service := NewService(pool, passwords, secret, setupToken, time.Hour, clock)
	if _, err := service.Setup(context.Background(), setupToken, "admin@example.com", "correct horse battery staple", "setup-network"); err != nil {
		t.Fatal(err)
	}
	globalHash := rateLimitSubjectHash(secret, "login", "global", "v1")
	seedBlockedRateLimit(t, pool, globalHash, clock.Now())

	tests := []struct {
		name            string
		email           string
		password        string
		network         string
		wantOutcome     string
		normalizedEmail string
	}{
		{
			name: "malformed candidate", email: "not-an-email", password: dummyPassword,
			network: "malformed-network", wantOutcome: "invalid_credentials", normalizedEmail: "not-an-email",
		},
		{
			name: "unknown candidate", email: "unknown@example.com", password: dummyPassword,
			network: "unknown-network", wantOutcome: "invalid_credentials", normalizedEmail: "unknown@example.com",
		},
		{
			name: "real candidate", email: "admin@example.com", password: "correct horse battery staple",
			network: "real-network", wantOutcome: "success", normalizedEmail: "admin@example.com",
		},
	}

	golden := loginSequenceGolden{Candidates: make([]loginSequenceCandidate, 0, len(tests))}
	for _, test := range tests {
		var stages []string
		ctx := withLoginTrace(context.Background(), func(stage loginTraceStage) {
			stages = append(stages, string(stage))
		})
		sessionsBefore := sessionCount(t, pool)
		session, err := service.Login(ctx, test.email, test.password, test.network)
		outcome := "success"
		if errors.Is(err, ErrInvalidCredentials) {
			outcome = "invalid_credentials"
		} else if err != nil {
			t.Fatalf("%s Login(): %v", test.name, err)
		}
		if outcome != test.wantOutcome {
			t.Fatalf("%s outcome = %q, want %q", test.name, outcome, test.wantOutcome)
		}
		if outcome == "success" && session.RawToken == "" {
			t.Fatalf("%s returned an empty session token", test.name)
		}
		if outcome != "success" && session.RawToken != "" {
			t.Fatalf("%s issued a session on failure", test.name)
		}

		golden.Candidates = append(golden.Candidates, loginSequenceCandidate{
			Name:                 test.name,
			Outcome:              outcome,
			Stages:               stages,
			SessionDelta:         sessionCount(t, pool) - sessionsBefore,
			AccountFailures:      rateLimitFailures(t, pool, rateLimitSubjectHash(secret, "login", "account", test.normalizedEmail)),
			NetworkFailures:      rateLimitFailures(t, pool, rateLimitSubjectHash(secret, "login", "network", test.network)),
			LegacyGlobalFailures: rateLimitFailures(t, pool, globalHash),
		})
	}

	encoded, err := json.MarshalIndent(golden, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	encoded = append(encoded, '\n')
	want, err := os.ReadFile("testdata/login_sequence.golden.json")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(encoded, want) {
		t.Fatalf("login sequencing golden mismatch\n--- got ---\n%s--- want ---\n%s", encoded, want)
	}
}

func TestServiceConcurrentKnownUnknownWaveHasNoAccountSerializationAndOneDelayClass(t *testing.T) {
	pool := newAdminAuthPool(t)
	clock := &fakeClock{now: time.Date(2026, 8, 28, 1, 2, 3, 0, time.UTC)}
	secret := []byte("0123456789abcdef0123456789abcdef")
	setupToken := "01234567890123456789012345678901"
	if _, err := NewService(pool, NewArgon2idPasswords(), secret, setupToken, time.Hour, clock).Setup(
		context.Background(), setupToken, "admin@example.com", "correct horse battery staple", "setup-network"); err != nil {
		t.Fatal(err)
	}

	passwords := newWavePasswords()
	t.Cleanup(passwords.releaseAll)
	service := NewService(pool, passwords, secret, setupToken, time.Hour, clock)
	const requestsPerClass = 5
	const requestCount = 2 * requestsPerClass
	readyForKDF := make(chan struct{}, requestCount)
	releaseKDFWaveChannel := make(chan struct{})
	releaseKDFWave := sync.OnceFunc(func() { close(releaseKDFWaveChannel) })
	t.Cleanup(releaseKDFWave)
	type loginResult struct {
		candidate string
		stages    []string
		err       error
	}
	results := make(chan loginResult, requestCount)

	launch := func(candidate, email, password string, index int) {
		go func() {
			stages := make([]string, 0, 9)
			ctx := withLoginTrace(context.Background(), func(stage loginTraceStage) {
				stages = append(stages, string(stage))
				if stage == loginStageDelayNone {
					readyForKDF <- struct{}{}
					<-releaseKDFWaveChannel
				}
			})
			_, err := service.Login(ctx, email, password, fmt.Sprintf("wave-network-%s-%d", candidate, index))
			results <- loginResult{candidate: candidate, stages: stages, err: err}
		}()
	}
	ready := 0
	launchAndHold := func(candidate, email, password string, index int) {
		launch(candidate, email, password, index)
		select {
		case <-readyForKDF:
			ready++
		case <-time.After(5 * time.Second):
			t.Fatalf("only %d/%d requests completed the same pre-KDF delay class", ready, requestCount)
		}
	}
	for index := 0; index < requestsPerClass; index++ {
		launchAndHold("known", "admin@example.com", fmt.Sprintf("wrong-known-password-%02d", index), index)
		launchAndHold("unknown", "unknown-wave@example.com", dummyPassword, index)
	}
	if acquired := pool.Stats().AcquiredConns; acquired != 0 {
		t.Fatalf("connections held after delay selection and before KDF wave = %d, want 0", acquired)
	}
	releaseKDFWave()

	started := 0
	completed := 0
	classCounts := map[string]int{"known": 0, "unknown": 0}
	waitForResult := func() {
		select {
		case result := <-results:
			if !errors.Is(result.err, ErrInvalidCredentials) {
				t.Errorf("%s candidate error = %v, want ErrInvalidCredentials", result.candidate, result.err)
			}
			classCounts[result.candidate]++
			assertLoginWaveStages(t, result.candidate, result.stages)
			completed++
		case <-time.After(5 * time.Second):
			t.Fatalf("only %d/%d login results completed", completed, requestCount)
		}
	}
	heldKnown := make([]waveVerification, 0, passwordWorkConcurrency)
	twoKnownConcurrent := false
	for started < requestCount {
		select {
		case verification := <-passwords.started:
			started++
			if !twoKnownConcurrent && verification.kind == "real" {
				heldKnown = append(heldKnown, verification)
				if len(heldKnown) == passwordWorkConcurrency {
					twoKnownConcurrent = true
					for _, held := range heldKnown {
						held.release()
						waitForResult()
					}
					heldKnown = nil
				}
				continue
			}
			verification.release()
			waitForResult()
		case <-time.After(5 * time.Second):
			t.Fatalf("only %d/%d password verifications entered the KDF wave", started, requestCount)
		}
	}
	for _, held := range heldKnown {
		held.release()
		waitForResult()
	}
	for completed < requestCount {
		waitForResult()
	}
	if !twoKnownConcurrent {
		t.Fatal("five requests for one known account never occupied both KDF slots concurrently")
	}
	if got := passwords.maximumActive(); got != passwordWorkConcurrency {
		t.Fatalf("maximum concurrent KDFs = %d, want %d", got, passwordWorkConcurrency)
	}
	if classCounts["known"] != requestsPerClass || classCounts["unknown"] != requestsPerClass {
		t.Fatalf("completed candidate classes = %v, want five known and five unknown", classCounts)
	}
	for _, candidate := range []struct {
		email string
		want  int
	}{
		{email: "admin@example.com", want: requestsPerClass},
		{email: "unknown-wave@example.com", want: requestsPerClass},
	} {
		got := rateLimitFailures(t, pool, rateLimitSubjectHash(secret, "login", "account", candidate.email))
		if got != candidate.want {
			t.Errorf("account failure history for %s = %d, want %d", candidate.email, got, candidate.want)
		}
	}
	if sessions := sessionCount(t, pool); sessions != 1 {
		t.Fatalf("session rows after invalid wave = %d, want only the setup session", sessions)
	}
}

func assertLoginWaveStages(t *testing.T, candidate string, stages []string) {
	t.Helper()
	wantKDF := string(loginStageDummyKDF)
	if candidate == "known" {
		wantKDF = string(loginStageRealKDF)
	}
	want := []string{
		string(loginStageNetworkAdmission),
		string(loginStageFailureSignalRead),
		string(loginStageDelayNone),
		string(loginStageKDFSlotAcquired),
		string(loginStageCandidateHashRead),
		wantKDF,
		string(loginStageKDFSlotReleased),
		string(loginStageFailureRecorded),
	}
	if !equalStrings(stages, want) {
		t.Errorf("%s candidate stages\n got: %v\nwant: %v", candidate, stages, want)
	}
}

type waveVerification struct {
	kind    string
	release func()
}

type wavePasswords struct {
	started  chan waveVerification
	mu       sync.Mutex
	active   int
	maximum  int
	releases []func()
}

func newWavePasswords() *wavePasswords {
	return &wavePasswords{started: make(chan waveVerification, 10)}
}

func (*wavePasswords) Hash(string) (string, error) {
	return "", errors.New("unexpected password hash")
}

func (p *wavePasswords) Verify(encodedHash, _ string) error {
	kind := "real"
	if encodedHash == dummyPasswordHash {
		kind = "dummy"
	}
	releaseChannel := make(chan struct{})
	release := sync.OnceFunc(func() { close(releaseChannel) })
	p.mu.Lock()
	p.active++
	if p.active > p.maximum {
		p.maximum = p.active
	}
	p.releases = append(p.releases, release)
	p.mu.Unlock()
	p.started <- waveVerification{kind: kind, release: release}
	<-releaseChannel
	p.mu.Lock()
	p.active--
	p.mu.Unlock()
	return ErrInvalidCredentials
}

func (p *wavePasswords) maximumActive() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.maximum
}

func (p *wavePasswords) releaseAll() {
	p.mu.Lock()
	releases := append([]func(){}, p.releases...)
	p.mu.Unlock()
	for _, release := range releases {
		release()
	}
}

func sessionCount(t *testing.T, pool *database.Pool) int {
	t.Helper()
	var count int
	if err := pool.QueryRow(context.Background(), "select count(*) from pgbase_meta.admin_sessions").Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

func rateLimitFailures(t *testing.T, pool *database.Pool, hash []byte) int {
	t.Helper()
	var failures int
	if err := pool.QueryRow(context.Background(), `select coalesce(
		(select failures from pgbase_meta.admin_rate_limits where subject_hash = $1), 0)`, hash).Scan(&failures); err != nil {
		t.Fatal(err)
	}
	return failures
}

func equalStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}
