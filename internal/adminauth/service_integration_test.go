//go:build integration

package adminauth

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/webisnoteasy/PgBase/internal/database"
)

func TestServiceConcurrentSetupAllowsExactlyOneAdministrator(t *testing.T) {
	pool := newAdminAuthPool(t)
	clock := &fakeClock{now: time.Date(2026, 8, 28, 1, 2, 3, 0, time.UTC)}
	secret := []byte("0123456789abcdef0123456789abcdef")
	services := []*Service{
		NewService(pool, NewArgon2idPasswords(), secret, "01234567890123456789012345678901", 12*time.Hour, clock),
		NewService(pool, NewArgon2idPasswords(), secret, "01234567890123456789012345678901", 12*time.Hour, clock),
	}
	start := make(chan struct{})
	results := make(chan error, 2)
	var wg sync.WaitGroup
	for i, service := range services {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, err := service.Setup(context.Background(), "01234567890123456789012345678901", fmt.Sprintf("admin-%d@example.com", i), "correct horse battery staple", "192.0.2."+string(rune('1'+i)))
			results <- err
		}()
	}
	close(start)
	wg.Wait()
	close(results)
	succeeded, unavailable := 0, 0
	for err := range results {
		switch {
		case err == nil:
			succeeded++
		case errors.Is(err, ErrSetupUnavailable), database.Classify(err) == database.KindUnavailable:
			unavailable++
		default:
			t.Fatalf("unexpected setup result: %v", err)
		}
	}
	if succeeded != 1 || unavailable != 1 {
		t.Fatalf("setup results: succeeded=%d unavailable=%d", succeeded, unavailable)
	}
	var count int
	if err := pool.QueryRow(context.Background(), "select count(*) from pgbase_meta.admins").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("administrator count = %d, want 1", count)
	}
}

func TestServiceSetupContentionIsRetryableAndLeavesNoWrites(t *testing.T) {
	pool := newAdminAuthPool(t)
	clock := &fakeClock{now: time.Date(2026, 8, 28, 1, 2, 3, 0, time.UTC)}
	secret := []byte("0123456789abcdef0123456789abcdef")
	setupToken := "01234567890123456789012345678901"
	service := NewService(pool, &fixedHashPasswords{hash: "encoded-password-hash", delegate: NewArgon2idPasswords()}, secret, setupToken, time.Hour, clock)

	blocker, err := pool.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer blocker.Release()
	if _, err := blocker.Exec(context.Background(), "select pg_advisory_lock($1)", setupAdvisoryLockKey); err != nil {
		t.Fatal(err)
	}
	defer func() { _, _ = blocker.Exec(context.Background(), "select pg_advisory_unlock_all()") }()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, err = service.Setup(ctx, setupToken, "admin@example.com", "correct horse battery staple", "setup-lock-client")
	if database.Classify(err) != database.KindUnavailable {
		t.Fatalf("Setup under lock contention error = %v, want retryable unavailable", err)
	}
	if acquired := pool.Stats().AcquiredConns; acquired != 1 {
		t.Fatalf("connections acquired after setup contention = %d, want only blocker", acquired)
	}
	var administrators, sessions int
	if err := pool.QueryRow(context.Background(), "select count(*) from pgbase_meta.admins").Scan(&administrators); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(context.Background(), "select count(*) from pgbase_meta.admin_sessions").Scan(&sessions); err != nil {
		t.Fatal(err)
	}
	if administrators != 0 || sessions != 0 {
		t.Fatalf("writes after setup contention: administrators=%d sessions=%d", administrators, sessions)
	}

	if _, err := blocker.Exec(context.Background(), "select pg_advisory_unlock_all()"); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Setup(context.Background(), setupToken, "admin@example.com", "correct horse battery staple", "setup-lock-client"); err != nil {
		t.Fatalf("Setup retry after lock release: %v", err)
	}
}

func TestServiceSetupLoginAuthenticateExpireAndLogout(t *testing.T) {
	pool := newAdminAuthPool(t)
	clock := &fakeClock{now: time.Date(2026, 8, 28, 1, 2, 3, 0, time.UTC)}
	secret := []byte("0123456789abcdef0123456789abcdef")
	service := NewService(pool, NewArgon2idPasswords(), secret, "01234567890123456789012345678901", 12*time.Hour, clock)

	setup, err := service.Setup(context.Background(), "01234567890123456789012345678901", " ADMIN@Example.COM ", "correct horse battery staple", "192.0.2.1")
	if err != nil {
		t.Fatal(err)
	}
	if setup.Administrator.Email != "admin@example.com" || setup.Administrator.ID.Version() != uuid.Version(7) {
		t.Fatalf("setup administrator = %#v", setup.Administrator)
	}
	if setup.RawToken == "" || !setup.ExpiresAt.Equal(clock.Now().Add(12*time.Hour)) {
		t.Fatalf("setup session = %#v", setup)
	}
	if got, err := service.Authenticate(context.Background(), setup.RawToken); err != nil ||
		got.Administrator != setup.Administrator || !got.ExpiresAt.Equal(setup.ExpiresAt) ||
		got.CSRFToken != setup.CSRFToken {
		t.Fatalf("Authenticate setup token = %#v, %v", got, err)
	}

	if err := service.Logout(context.Background(), setup.RawToken); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Authenticate(context.Background(), setup.RawToken); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("Authenticate logged-out token error = %v", err)
	}

	login, err := service.Login(context.Background(), "admin@example.com", "correct horse battery staple", "192.0.2.1")
	if err != nil {
		t.Fatal(err)
	}
	if login.RawToken == setup.RawToken {
		t.Fatal("login reused the setup session token")
	}
	rotated := NewService(pool, NewArgon2idPasswords(), []byte("abcdef0123456789abcdef0123456789"), "", 12*time.Hour, clock)
	if _, err := rotated.Authenticate(context.Background(), login.RawToken); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("Authenticate after token-secret rotation error = %v", err)
	}
	clock.Add(12 * time.Hour)
	if _, err := service.Authenticate(context.Background(), login.RawToken); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("Authenticate expired token error = %v", err)
	}
}

func TestServiceSetupIsDisabledWithoutConfiguredToken(t *testing.T) {
	pool := newAdminAuthPool(t)
	clock := &fakeClock{now: time.Date(2026, 8, 28, 1, 2, 3, 0, time.UTC)}
	service := NewService(
		pool,
		NewArgon2idPasswords(),
		[]byte("0123456789abcdef0123456789abcdef"),
		"",
		time.Hour,
		clock,
	)

	for _, presented := range []string{"", "attacker-supplied-token"} {
		if _, err := service.Setup(context.Background(), presented, "admin@example.com", "correct horse battery staple", "192.0.2.1"); !errors.Is(err, ErrSetupUnavailable) {
			t.Fatalf("Setup without configured token using %q error = %v, want ErrSetupUnavailable", presented, err)
		}
	}

	var administrators, sessions int
	if err := pool.QueryRow(context.Background(), "select count(*) from pgbase_meta.admins").Scan(&administrators); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(context.Background(), "select count(*) from pgbase_meta.admin_sessions").Scan(&sessions); err != nil {
		t.Fatal(err)
	}
	if administrators != 0 || sessions != 0 {
		t.Fatalf("disabled setup persisted administrators=%d sessions=%d, want 0/0", administrators, sessions)
	}
}

func TestServicePropagatesAdministratorUUIDv7GenerationFailure(t *testing.T) {
	pool := newAdminAuthPool(t)
	clock := &fakeClock{now: time.Date(2026, 8, 28, 1, 2, 3, 0, time.UTC)}
	service := NewService(
		pool,
		NewArgon2idPasswords(),
		[]byte("0123456789abcdef0123456789abcdef"),
		"01234567890123456789012345678901",
		time.Hour,
		clock,
	)

	uuid.SetRand(failingUUIDReader{})
	t.Cleanup(func() { uuid.SetRand(nil) })
	if _, err := service.Setup(context.Background(), "01234567890123456789012345678901", "admin@example.com", "correct horse battery staple", "192.0.2.1"); err == nil {
		t.Fatal("Setup succeeded when UUIDv7 generation failed")
	}

	var administrators, sessions int
	if err := pool.QueryRow(context.Background(), "select count(*) from pgbase_meta.admins").Scan(&administrators); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(context.Background(), "select count(*) from pgbase_meta.admin_sessions").Scan(&sessions); err != nil {
		t.Fatal(err)
	}
	if administrators != 0 || sessions != 0 {
		t.Fatalf("UUID generation failure persisted administrators=%d sessions=%d, want 0/0", administrators, sessions)
	}
}

func TestServiceCreateAdministratorPropagatesUUIDv7GenerationFailure(t *testing.T) {
	pool := newAdminAuthPool(t)
	service := NewService(
		pool,
		NewArgon2idPasswords(),
		[]byte("0123456789abcdef0123456789abcdef"),
		"",
		time.Hour,
		&fakeClock{now: time.Date(2026, 8, 28, 1, 2, 3, 0, time.UTC)},
	)

	uuid.SetRand(failingUUIDReader{})
	t.Cleanup(func() { uuid.SetRand(nil) })
	if _, err := service.CreateAdministrator(context.Background(), "admin@example.com", "correct horse battery staple"); err == nil {
		t.Fatal("CreateAdministrator succeeded when UUIDv7 generation failed")
	}
	var administrators int
	if err := pool.QueryRow(context.Background(), "select count(*) from pgbase_meta.admins").Scan(&administrators); err != nil {
		t.Fatal(err)
	}
	if administrators != 0 {
		t.Fatalf("UUID generation failure persisted administrators=%d, want 0", administrators)
	}
}

func TestServiceLoginFailureHistoryNeverRejectsValidCredentials(t *testing.T) {
	pool := newAdminAuthPool(t)
	clock := &fakeClock{now: time.Date(2026, 8, 28, 1, 2, 3, 0, time.UTC)}
	secret := []byte("0123456789abcdef0123456789abcdef")
	services := []*Service{
		NewService(pool, NewArgon2idPasswords(), secret, "01234567890123456789012345678901", time.Hour, clock),
		NewService(pool, NewArgon2idPasswords(), secret, "01234567890123456789012345678901", time.Hour, clock),
	}
	if _, err := services[0].Setup(context.Background(), "01234567890123456789012345678901", "admin@example.com", "correct horse battery staple", "setup-client"); err != nil {
		t.Fatal(err)
	}
	for attempt := 0; attempt < 5; attempt++ {
		network := fmt.Sprintf("attacker-network-%d", attempt)
		if _, err := services[attempt%2].Login(context.Background(), "ADMIN@example.com", "incorrect horse battery staple", network); !errors.Is(err, ErrInvalidCredentials) {
			t.Fatalf("attempt %d error = %v, want ErrInvalidCredentials", attempt+1, err)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := services[1].Login(ctx, "admin@example.com", "correct horse battery staple", "legitimate-network"); err != nil {
		t.Fatalf("valid credentials were rejected after targeted failures: %v", err)
	}
	var emailFailures int
	emailHash := rateLimitSubjectHash(secret, "login", "account", "admin@example.com")
	if err := pool.QueryRow(context.Background(), `select count(*) from pgbase_meta.admin_rate_limits where subject_hash = $1`, emailHash).Scan(&emailFailures); err != nil {
		t.Fatal(err)
	}
	if emailFailures != 0 {
		t.Fatalf("email failure history rows after success = %d, want 0", emailFailures)
	}
}

func TestServiceLoginSuccessPreservesOtherAccountsSharedFailures(t *testing.T) {
	pool := newAdminAuthPool(t)
	clock := &fakeClock{now: time.Date(2026, 8, 28, 1, 2, 3, 0, time.UTC)}
	secret := []byte("0123456789abcdef0123456789abcdef")
	setupToken := "01234567890123456789012345678901"
	service := NewService(pool, NewArgon2idPasswords(), secret, setupToken, time.Hour, clock)
	if _, err := service.Setup(context.Background(), setupToken, "admin@example.com", "correct horse battery staple", "setup-client"); err != nil {
		t.Fatal(err)
	}
	const network = "192.0.2.51"
	for attempt := 0; attempt < 2; attempt++ {
		if _, err := service.Login(context.Background(), fmt.Sprintf("other-%d@example.com", attempt), "incorrect horse battery staple", network); !errors.Is(err, ErrInvalidCredentials) {
			t.Fatalf("other account attempt %d error = %v", attempt+1, err)
		}
	}
	if _, err := service.Login(context.Background(), "admin@example.com", "correct horse battery staple", network); err != nil {
		t.Fatal(err)
	}
	var failures int
	hash := rateLimitSubjectHash(secret, "login", "network", network)
	if err := pool.QueryRow(context.Background(), "select failures from pgbase_meta.admin_rate_limits where subject_hash = $1", hash).Scan(&failures); err != nil {
		t.Fatal(err)
	}
	if failures != 2 {
		t.Fatalf("network shared failures after another account succeeds = %d, want 2", failures)
	}
}

func TestServiceLoginIgnoresLegacyGlobalRateLimit(t *testing.T) {
	pool := newAdminAuthPool(t)
	clock := &fakeClock{now: time.Date(2026, 8, 28, 1, 2, 3, 0, time.UTC)}
	secret := []byte("0123456789abcdef0123456789abcdef")
	setupToken := "01234567890123456789012345678901"
	service := NewService(pool, NewArgon2idPasswords(), secret, setupToken, time.Hour, clock)
	if _, err := service.Setup(context.Background(), setupToken, "admin@example.com", "correct horse battery staple", "setup-client"); err != nil {
		t.Fatal(err)
	}
	seedBlockedRateLimit(t, pool, rateLimitSubjectHash(secret, "login", "global", "v1"), clock.Now())

	if _, err := service.Login(context.Background(), "admin@example.com", "correct horse battery staple", "192.0.2.90"); err != nil {
		t.Fatalf("Login() with legacy global row error = %v", err)
	}
}

func TestServiceSetupIgnoresLegacyGlobalRateLimit(t *testing.T) {
	pool := newAdminAuthPool(t)
	clock := &fakeClock{now: time.Date(2026, 8, 28, 1, 2, 3, 0, time.UTC)}
	secret := []byte("0123456789abcdef0123456789abcdef")
	setupToken := "01234567890123456789012345678901"
	service := NewService(pool, NewArgon2idPasswords(), secret, setupToken, time.Hour, clock)
	seedBlockedRateLimit(t, pool, rateLimitSubjectHash(secret, "setup", "global", "v1"), clock.Now())

	if _, err := service.Setup(context.Background(), setupToken, "admin@example.com", "correct horse battery staple", "192.0.2.91"); err != nil {
		t.Fatalf("Setup() with legacy global row error = %v", err)
	}
}

func TestServiceLoginEmailRotationIsStoppedByNetworkRateLimit(t *testing.T) {
	pool := newAdminAuthPool(t)
	clock := &fakeClock{now: time.Date(2026, 8, 28, 1, 2, 3, 0, time.UTC)}
	secret := []byte("0123456789abcdef0123456789abcdef")
	setupToken := "01234567890123456789012345678901"
	if _, err := NewService(pool, NewArgon2idPasswords(), secret, setupToken, time.Hour, clock).Setup(
		context.Background(), setupToken, "admin@example.com", "correct horse battery staple", "setup-client"); err != nil {
		t.Fatal(err)
	}
	passwords := &immediateInvalidPasswords{}
	service := NewService(pool, passwords, secret, setupToken, time.Hour, clock)
	for attempt := 0; attempt < networkFailureLimit; attempt++ {
		email := fmt.Sprintf("rotated-%d@example.com", attempt)
		if _, err := service.Login(context.Background(), email, "incorrect horse battery staple", "192.0.2.44"); !errors.Is(err, ErrInvalidCredentials) {
			t.Fatalf("attempt %d error = %v, want invalid credentials", attempt+1, err)
		}
	}
	if _, err := service.Login(context.Background(), "next@example.com", "incorrect horse battery staple", "192.0.2.44"); !errors.Is(err, ErrRateLimited) {
		t.Fatalf("rotated email after network limit error = %v, want rate limited", err)
	}
	if passwords.verifyCount != networkFailureLimit {
		t.Fatalf("password verifications = %d, want %d; blocked request must not run KDF", passwords.verifyCount, networkFailureLimit)
	}
	var rows int
	if err := pool.QueryRow(context.Background(), "select count(*) from pgbase_meta.admin_rate_limits").Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows > networkFailureLimit+1 {
		t.Fatalf("rate-limit rows = %d, want at most %d", rows, networkFailureLimit+1)
	}
}

func TestServiceCredentialKDFBacklogDoesNotStarveSessionOrProbeAdmissions(t *testing.T) {
	pool := newAdminAuthPool(t)
	clock := &fakeClock{now: time.Date(2026, 8, 28, 1, 2, 3, 0, time.UTC)}
	secret := []byte("0123456789abcdef0123456789abcdef")
	setupToken := "01234567890123456789012345678901"
	realPasswords := NewArgon2idPasswords()
	service := NewService(pool, realPasswords, secret, setupToken, time.Hour, clock)
	issued, err := service.Setup(context.Background(), setupToken, "admin@example.com", "correct horse battery staple", "setup-client")
	if err != nil {
		t.Fatal(err)
	}

	blocking := &concurrencyPasswords{
		delegate: realPasswords,
		started:  make(chan struct{}, credentialAdmissionConcurrency),
		release:  make(chan struct{}),
	}
	release := sync.OnceFunc(func() { close(blocking.release) })
	defer release()
	service.passwords = blocking
	admissionLimit := cap(service.credentialSlots)
	errorsByAttempt := make(chan error, admissionLimit)
	for attempt := range admissionLimit {
		go func() {
			_, err := service.Login(
				context.Background(),
				fmt.Sprintf("unknown-%d@example.com", attempt),
				"pgbase dummy credential 2026",
				fmt.Sprintf("queued-network-%d", attempt),
			)
			errorsByAttempt <- err
		}()
	}
	for range passwordWorkConcurrency {
		select {
		case <-blocking.started:
		case <-time.After(2 * time.Second):
			t.Fatal("login did not reach password verification")
		}
	}

	deadline := time.Now().Add(5 * time.Second)
	for {
		var rows int
		if err := pool.QueryRow(context.Background(), "select count(*) from pgbase_meta.admin_rate_limits").Scan(&rows); err != nil {
			t.Fatal(err)
		}
		if rows == admissionLimit && pool.Stats().AcquiredConns == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("credential requests did not finish pre-KDF database work: rows=%d acquired=%d", rows, pool.Stats().AcquiredConns)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if occupied := len(service.credentialSlots); occupied != admissionLimit {
		t.Fatalf("occupied credential admission slots = %d, want %d", occupied, admissionLimit)
	}
	if acquired := pool.Stats().AcquiredConns; acquired != 0 {
		t.Fatalf("database connections acquired while KDF work is queued/running = %d, want 0", acquired)
	}
	select {
	case <-blocking.started:
		t.Fatal("credential KDF work exceeded the password-work bound")
	case <-time.After(50 * time.Millisecond):
	}

	acquiresBefore := pool.Stats().AcquireCount
	if _, err := service.Login(context.Background(), "overflow@example.com", dummyPassword, "overflow-network"); !errors.Is(err, ErrRateLimited) {
		t.Fatalf("Login over credential admission capacity error = %v, want ErrRateLimited", err)
	}
	if acquiresAfter := pool.Stats().AcquireCount; acquiresAfter != acquiresBefore {
		t.Fatalf("database acquisitions for rejected credential request = %d, want 0", acquiresAfter-acquiresBefore)
	}
	principal, err := service.Authenticate(context.Background(), issued.RawToken)
	if err != nil || principal.Administrator.ID != issued.Administrator.ID {
		t.Fatalf("Authenticate during credential saturation = %#v, %v", principal, err)
	}
	if required, err := service.SetupRequired(context.Background()); err != nil || required {
		t.Fatalf("SetupRequired during credential saturation = %t, %v; want false, nil", required, err)
	}
	if err := service.Logout(context.Background(), issued.RawToken); err != nil {
		t.Fatalf("Logout during credential saturation: %v", err)
	}

	release()
	for range admissionLimit {
		if err := <-errorsByAttempt; !errors.Is(err, ErrInvalidCredentials) {
			t.Fatalf("queued login error = %v, want ErrInvalidCredentials", err)
		}
	}
	if len(service.credentialSlots) != 0 || len(service.sessionSlots) != 0 || len(service.probeSlots) != 0 {
		t.Fatalf("admission slots leaked: credential=%d session=%d probe=%d",
			len(service.credentialSlots), len(service.sessionSlots), len(service.probeSlots))
	}
}

func TestServiceSetupPasswordQueueAndCredentialAdmissionDoNotBorrowDatabaseResources(t *testing.T) {
	pool := newAdminAuthPool(t)
	clock := &fakeClock{now: time.Date(2026, 8, 28, 1, 2, 3, 0, time.UTC)}
	secret := []byte("0123456789abcdef0123456789abcdef")
	setupToken := "01234567890123456789012345678901"
	blocking := &concurrencyPasswords{
		delegate: &fixedHashPasswords{hash: "encoded-password-hash", delegate: NewArgon2idPasswords()},
		started:  make(chan struct{}, credentialAdmissionConcurrency),
		release:  make(chan struct{}),
	}
	release := sync.OnceFunc(func() { close(blocking.release) })
	defer release()
	service := NewService(pool, blocking, secret, setupToken, time.Hour, clock)
	admissionLimit := cap(service.credentialSlots)
	if admissionLimit <= passwordWorkConcurrency {
		t.Fatalf("credential admission capacity = %d, want more than password work capacity %d", admissionLimit, passwordWorkConcurrency)
	}

	errorsByAttempt := make(chan error, admissionLimit)
	for attempt := range admissionLimit {
		go func() {
			_, err := service.Setup(
				context.Background(),
				setupToken,
				fmt.Sprintf("setup-%d@example.com", attempt),
				"correct horse battery staple",
				fmt.Sprintf("setup-network-%d", attempt),
			)
			errorsByAttempt <- err
		}()
	}
	for range passwordWorkConcurrency {
		select {
		case <-blocking.started:
		case <-time.After(2 * time.Second):
			t.Fatal("setup did not reach password hashing")
		}
	}

	var rateLimitRows int
	if err := pool.QueryRow(context.Background(), "select count(*) from pgbase_meta.admin_rate_limits").Scan(&rateLimitRows); err != nil {
		t.Fatal(err)
	}
	if rateLimitRows != 0 {
		t.Fatalf("setup created %d pre-authentication limiter rows, want 0", rateLimitRows)
	}
	select {
	case <-blocking.started:
		t.Fatal("setup password hashing exceeded the password-work bound")
	case <-time.After(50 * time.Millisecond):
	}
	if acquired := pool.Stats().AcquiredConns; acquired != 0 {
		t.Fatalf("database connections acquired while setup password work is queued/running = %d, want 0", acquired)
	}
	acquiresBefore := pool.Stats().AcquireCount
	if _, err := service.Setup(context.Background(), setupToken, "overflow@example.com", "correct horse battery staple", "overflow-network"); !errors.Is(err, ErrRateLimited) {
		t.Fatalf("Setup over credential admission capacity error = %v, want ErrRateLimited", err)
	}
	if acquiresAfter := pool.Stats().AcquireCount; acquiresAfter != acquiresBefore {
		t.Fatalf("database acquisitions for rejected credential request = %d, want 0", acquiresAfter-acquiresBefore)
	}
	if required, err := service.SetupRequired(context.Background()); err != nil || !required {
		t.Fatalf("SetupRequired during credential saturation = %t, %v; want true, nil", required, err)
	}
	if _, err := service.Authenticate(context.Background(), "distinct-flood-token"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("Authenticate during credential saturation error = %v, want ErrInvalidCredentials", err)
	}

	release()
	succeeded := 0
	for range admissionLimit {
		err := <-errorsByAttempt
		switch {
		case err == nil:
			succeeded++
		case errors.Is(err, ErrSetupUnavailable), database.Classify(err) == database.KindUnavailable:
		default:
			t.Fatalf("queued setup error = %v", err)
		}
	}
	if succeeded != 1 {
		t.Fatalf("successful queued setup calls = %d, want 1", succeeded)
	}
	if blocking.maximum != passwordWorkConcurrency {
		t.Fatalf("maximum concurrent setup password work = %d, want %d", blocking.maximum, passwordWorkConcurrency)
	}
	if _, err := service.Authenticate(context.Background(), "unknown-token-after-release"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("Authenticate after admissions released error = %v, want ErrInvalidCredentials", err)
	}
	if occupied := len(service.credentialSlots); occupied != 0 {
		t.Fatalf("occupied credential admission slots after completion = %d, want 0", occupied)
	}
}

func TestServicePublicAuthAdmissionReleasesOnEveryReturnPath(t *testing.T) {
	pool := newAdminAuthPool(t)
	clock := &fakeClock{now: time.Date(2026, 8, 28, 1, 2, 3, 0, time.UTC)}
	service := NewService(
		pool,
		&immediateInvalidPasswords{},
		[]byte("0123456789abcdef0123456789abcdef"),
		"",
		time.Hour,
		clock,
	)
	assertReleased := func(operation string, slots chan struct{}) {
		t.Helper()
		if occupied := len(slots); occupied != 0 {
			t.Fatalf("occupied authentication admission slots after %s = %d, want 0", operation, occupied)
		}
	}

	if _, err := service.Setup(context.Background(), "", "admin@example.com", "correct horse battery staple", "setup-client"); !errors.Is(err, ErrSetupUnavailable) {
		t.Fatalf("Setup error = %v, want ErrSetupUnavailable", err)
	}
	assertReleased("Setup", service.credentialSlots)
	if required, err := service.SetupRequired(context.Background()); err != nil || !required {
		t.Fatalf("SetupRequired = %t, %v; want true, nil", required, err)
	}
	assertReleased("SetupRequired", service.probeSlots)
	if _, err := service.Login(context.Background(), "unknown@example.com", "incorrect horse battery staple", "login-client"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("Login error = %v, want ErrInvalidCredentials", err)
	}
	assertReleased("Login", service.credentialSlots)
	if _, err := service.Authenticate(context.Background(), "unknown-token"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("Authenticate error = %v, want ErrInvalidCredentials", err)
	}
	assertReleased("Authenticate", service.sessionSlots)
	if err := service.Logout(context.Background(), "unknown-token"); err != nil {
		t.Fatalf("Logout error = %v", err)
	}
	assertReleased("Logout", service.sessionSlots)
}

func TestServiceAdmissionClassesSaturateAndReleaseAfterCancellation(t *testing.T) {
	pool := newAdminAuthPool(t)
	setupToken := "01234567890123456789012345678901"
	service := NewService(
		pool,
		&immediateInvalidPasswords{},
		[]byte("0123456789abcdef0123456789abcdef"),
		setupToken,
		time.Hour,
		&fakeClock{now: time.Date(2026, 8, 28, 1, 2, 3, 0, time.UTC)},
	)
	classes := []struct {
		name  string
		slots chan struct{}
		call  func(context.Context) error
	}{
		{
			name: "credential", slots: service.credentialSlots,
			call: func(ctx context.Context) error {
				_, err := service.Setup(ctx, setupToken, "admin@example.com", "correct horse battery staple", "canceled-setup")
				return err
			},
		},
		{
			name: "session", slots: service.sessionSlots,
			call: func(ctx context.Context) error {
				_, err := service.Authenticate(ctx, "unknown-token")
				return err
			},
		},
		{
			name: "probe", slots: service.probeSlots,
			call: func(ctx context.Context) error {
				_, err := service.SetupRequired(ctx)
				return err
			},
		},
	}
	for _, class := range classes {
		t.Run(class.name, func(t *testing.T) {
			releases := make([]func(), 0, cap(class.slots))
			for range cap(class.slots) {
				release, err := acquireAdmission(class.slots)
				if err != nil {
					t.Fatal(err)
				}
				releases = append(releases, release)
			}
			releaseAll := sync.OnceFunc(func() {
				for _, release := range releases {
					release()
				}
			})
			defer releaseAll()
			acquiresBefore := pool.Stats().AcquireCount
			if err := class.call(context.Background()); !errors.Is(err, ErrRateLimited) {
				t.Fatalf("saturated call error = %v, want ErrRateLimited", err)
			}
			if acquiresAfter := pool.Stats().AcquireCount; acquiresAfter != acquiresBefore {
				t.Fatalf("database acquisitions for saturated call = %d, want 0", acquiresAfter-acquiresBefore)
			}
			releaseAll()

			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			if err := class.call(ctx); !errors.Is(err, context.Canceled) {
				t.Fatalf("canceled call error = %v, want context.Canceled", err)
			}
			if occupied := len(class.slots); occupied != 0 {
				t.Fatalf("occupied slots after cancellation = %d, want 0", occupied)
			}
		})
	}
}

func TestServiceCLIMutationsRemainOutsidePublicAuthAdmission(t *testing.T) {
	pool := newAdminAuthPool(t)
	service := NewService(
		pool,
		&fixedHashPasswords{hash: "encoded-password-hash", delegate: NewArgon2idPasswords()},
		[]byte("0123456789abcdef0123456789abcdef"),
		"",
		time.Hour,
		&fakeClock{now: time.Date(2026, 8, 28, 1, 2, 3, 0, time.UTC)},
	)
	releases := make([]func(), 0, cap(service.credentialSlots))
	for range cap(service.credentialSlots) {
		release, err := acquireAdmission(service.credentialSlots)
		if err != nil {
			t.Fatal(err)
		}
		releases = append(releases, release)
	}
	defer func() {
		for _, release := range releases {
			release()
		}
	}()

	if _, err := service.CreateAdministrator(context.Background(), "admin@example.com", "correct horse battery staple"); err != nil {
		t.Fatalf("CreateAdministrator while public admission is saturated: %v", err)
	}
	if _, err := service.ResetAdministratorPassword(context.Background(), "admin@example.com", "new correct horse battery staple"); err != nil {
		t.Fatalf("ResetAdministratorPassword while public admission is saturated: %v", err)
	}
}

func TestServiceLoginAlwaysPerformsExactlyOnePasswordVerification(t *testing.T) {
	pool := newAdminAuthPool(t)
	clock := &fakeClock{now: time.Date(2026, 8, 28, 1, 2, 3, 0, time.UTC)}
	secret := []byte("0123456789abcdef0123456789abcdef")
	setupToken := "01234567890123456789012345678901"
	realPasswords := NewArgon2idPasswords()
	setupService := NewService(pool, realPasswords, secret, setupToken, time.Hour, clock)
	if _, err := setupService.Setup(context.Background(), setupToken, "admin@example.com", "correct horse battery staple", "setup-client"); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name      string
		email     string
		password  string
		wantError error
	}{
		{name: "valid account", email: "ADMIN@example.com", password: "correct horse battery staple"},
		{name: "unknown account", email: "unknown@example.com", password: "pgbase dummy credential 2026", wantError: ErrInvalidCredentials},
		{name: "malformed email", email: "not-an-email", password: "pgbase dummy credential 2026", wantError: ErrInvalidCredentials},
	}
	for index, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			passwords := &countingPasswords{delegate: realPasswords}
			service := NewService(pool, passwords, secret, setupToken, time.Hour, clock)
			_, err := service.Login(context.Background(), tc.email, tc.password, fmt.Sprintf("192.0.2.%d", index+1))
			if !errors.Is(err, tc.wantError) {
				t.Fatalf("Login error = %v, want %v", err, tc.wantError)
			}
			if len(passwords.verifyHashes) != 1 {
				t.Fatalf("password verification count = %d, want exactly 1", len(passwords.verifyHashes))
			}
			if _, _, ok := parsePasswordHash(passwords.verifyHashes[0]); !ok {
				t.Fatalf("verification hash is not a valid approved Argon2id PHC string: %q", passwords.verifyHashes[0])
			}
		})
	}
}

func TestServiceRejectsInvalidSetupTokenBeforePersistentRateLimitState(t *testing.T) {
	pool := newAdminAuthPool(t)
	clock := &fakeClock{now: time.Date(2026, 8, 28, 1, 2, 3, 0, time.UTC)}
	configured := "01234567890123456789012345678901"
	presented := "this-token-must-never-be-persisted"
	secret := []byte("0123456789abcdef0123456789abcdef")
	service := NewService(pool, &immediateInvalidPasswords{}, secret, configured, time.Hour, clock)
	for attempt := 0; attempt < accountFailureLimit+4; attempt++ {
		email := "admin@example.com"
		if attempt >= accountFailureLimit {
			email = fmt.Sprintf("attacker-%d@example.com", attempt)
		}
		if _, err := service.Setup(context.Background(), presented, email, "correct horse battery staple", fmt.Sprintf("192.0.2.%d", attempt+1)); !errors.Is(err, ErrInvalidCredentials) {
			t.Fatalf("Setup invalid token attempt %d error = %v, want ErrInvalidCredentials", attempt+1, err)
		}
	}
	var leaked bool
	if err := pool.QueryRow(context.Background(), `select exists(
		select 1 from pgbase_meta.admins where password_hash like '%' || $1 || '%'
		union all
		select 1 from pgbase_meta.admin_rate_limits where encode(subject_hash, 'escape') like '%' || $1 || '%'
	)`, presented).Scan(&leaked); err != nil {
		t.Fatal(err)
	}
	if leaked {
		t.Fatal("presented setup token was persisted")
	}
	var limiterRows int
	if err := pool.QueryRow(context.Background(), `select count(*) from pgbase_meta.admin_rate_limits`).Scan(&limiterRows); err != nil {
		t.Fatal(err)
	}
	if limiterRows != 0 {
		t.Fatalf("rate-limit rows after invalid setup tokens = %d, want 0", limiterRows)
	}
	service = NewService(pool, NewArgon2idPasswords(), secret, configured, time.Hour, clock)
	if _, err := service.Setup(context.Background(), configured, "admin@example.com", "correct horse battery staple", "198.51.100.10"); err != nil {
		t.Fatalf("valid setup after attacker traffic: %v", err)
	}
}

func TestServiceResetAdministratorPasswordRevokesAllExistingSessions(t *testing.T) {
	pool := newAdminAuthPool(t)
	clock := &fakeClock{now: time.Date(2026, 8, 28, 1, 2, 3, 0, time.UTC)}
	secret := []byte("0123456789abcdef0123456789abcdef")
	service := NewService(pool, NewArgon2idPasswords(), secret, "01234567890123456789012345678901", time.Hour, clock)
	issued, err := service.Setup(context.Background(), "01234567890123456789012345678901", "admin@example.com", "correct horse battery staple", "setup-client")
	if err != nil {
		t.Fatal(err)
	}
	for attempt := 0; attempt < accountFailureLimit; attempt++ {
		if _, err := service.Login(context.Background(), "admin@example.com", "wrong horse battery staple", fmt.Sprintf("reset-attacker-%d", attempt)); !errors.Is(err, ErrInvalidCredentials) {
			t.Fatalf("lockout attempt %d error = %v", attempt+1, err)
		}
	}
	preResetSession, err := service.Login(context.Background(), "admin@example.com", "correct horse battery staple", "reset-client")
	if err != nil {
		t.Fatalf("valid login after targeted failures: %v", err)
	}
	updated, err := service.ResetAdministratorPassword(context.Background(), " ADMIN@example.com ", "new correct horse battery staple")
	if err != nil {
		t.Fatal(err)
	}
	if updated.Email != "admin@example.com" || !updated.Active {
		t.Fatalf("updated administrator = %#v", updated)
	}
	if _, err := service.Authenticate(context.Background(), issued.RawToken); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("old session authentication error = %v, want invalid credentials", err)
	}
	if _, err := service.Authenticate(context.Background(), preResetSession.RawToken); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("pre-reset login session authentication error = %v, want invalid credentials", err)
	}
	var sessions int
	if err := pool.QueryRow(context.Background(), "select count(*) from pgbase_meta.admin_sessions").Scan(&sessions); err != nil || sessions != 0 {
		t.Fatalf("sessions after reset = %d, err=%v; want 0", sessions, err)
	}
	if _, err := service.Login(context.Background(), "admin@example.com", "new correct horse battery staple", "reset-client"); err != nil {
		t.Fatalf("login with reset password: %v", err)
	}
}

func TestServicePasswordResetCompletesDuringKDFAndInvalidatesVerifiedSnapshot(t *testing.T) {
	pool := newAdminAuthPool(t)
	clock := &fakeClock{now: time.Date(2026, 8, 28, 1, 2, 3, 0, time.UTC)}
	secret := []byte("0123456789abcdef0123456789abcdef")
	setupToken := "01234567890123456789012345678901"
	realPasswords := NewArgon2idPasswords()
	baseService := NewService(pool, realPasswords, secret, setupToken, time.Hour, clock)
	if _, err := baseService.Setup(context.Background(), setupToken, "admin@example.com", "correct horse battery staple", "setup-client"); err != nil {
		t.Fatal(err)
	}

	blocking := &concurrencyPasswords{
		delegate: realPasswords,
		started:  make(chan struct{}, 1),
		release:  make(chan struct{}),
	}
	loginService := NewService(pool, blocking, secret, setupToken, time.Hour, clock)
	loginResult := make(chan Session, 1)
	loginErr := make(chan error, 1)
	releaseVerification := sync.OnceFunc(func() { close(blocking.release) })
	defer releaseVerification()
	go func() {
		session, err := loginService.Login(context.Background(), "admin@example.com", "correct horse battery staple", "login-client")
		loginResult <- session
		loginErr <- err
	}()
	select {
	case <-blocking.started:
	case <-time.After(2 * time.Second):
		t.Fatal("login did not reach password verification")
	}

	newHash, err := realPasswords.Hash("new correct horse battery staple")
	if err != nil {
		t.Fatal(err)
	}
	resetService := NewService(pool, &fixedHashPasswords{hash: newHash, delegate: realPasswords}, secret, setupToken, time.Hour, clock)
	resetDone := make(chan error, 1)
	go func() {
		_, err := resetService.ResetAdministratorPassword(context.Background(), "admin@example.com", "unused valid password")
		resetDone <- err
	}()
	resetFinishedBeforeKDF := false
	var resetErr error
	select {
	case resetErr = <-resetDone:
		resetFinishedBeforeKDF = true
	case <-time.After(250 * time.Millisecond):
	}

	acquiredDuringKDF := pool.Stats().AcquiredConns
	releaseVerification()
	gotLoginErr := <-loginErr
	issued := <-loginResult
	if !resetFinishedBeforeKDF {
		resetErr = <-resetDone
	}
	if resetErr != nil {
		t.Fatalf("password reset: %v", resetErr)
	}
	if !resetFinishedBeforeKDF {
		t.Error("password reset was blocked while password verification held the old hash snapshot")
	}
	if acquiredDuringKDF != 0 {
		t.Errorf("database connections acquired during password verification = %d, want 0", acquiredDuringKDF)
	}
	if !errors.Is(gotLoginErr, ErrInvalidCredentials) {
		t.Errorf("login verified against a stale password hash error = %v, want ErrInvalidCredentials", gotLoginErr)
	}
	if issued.RawToken != "" {
		t.Errorf("login verified against a stale password hash issued a session token")
	}
	var sessions int
	if err := pool.QueryRow(context.Background(), "select count(*) from pgbase_meta.admin_sessions").Scan(&sessions); err != nil {
		t.Fatal(err)
	}
	if sessions != 0 {
		t.Fatalf("sessions after reset raced with old-hash login = %d, want 0", sessions)
	}
	if _, err := baseService.Login(context.Background(), "admin@example.com", "correct horse battery staple", "old-password-client"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("old password after reset error = %v, want invalid credentials", err)
	}
	if _, err := baseService.Login(context.Background(), "admin@example.com", "new correct horse battery staple", "new-password-client"); err != nil {
		t.Fatalf("new password after reset: %v", err)
	}
}

func TestServiceResetAdministratorPasswordRollsBackWhenSessionRevocationFails(t *testing.T) {
	pool := newAdminAuthPool(t)
	clock := &fakeClock{now: time.Date(2026, 8, 28, 1, 2, 3, 0, time.UTC)}
	secret := []byte("0123456789abcdef0123456789abcdef")
	service := NewService(pool, NewArgon2idPasswords(), secret, "01234567890123456789012345678901", time.Hour, clock)
	issued, err := service.Setup(context.Background(), "01234567890123456789012345678901", "admin@example.com", "correct horse battery staple", "setup-client")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(context.Background(), `create function app.fail_admin_session_delete() returns trigger language plpgsql as $$ begin raise exception 'forced session deletion failure'; end; $$;
		create trigger fail_admin_session_delete before delete on pgbase_meta.admin_sessions for each row execute function app.fail_admin_session_delete()`); err != nil {
		t.Fatal(err)
	}
	if _, err := service.ResetAdministratorPassword(context.Background(), "admin@example.com", "new correct horse battery staple"); err == nil {
		t.Fatal("reset succeeded despite forced session-deletion failure")
	}
	if _, err := service.Authenticate(context.Background(), issued.RawToken); err != nil {
		t.Fatalf("old session was not preserved after rollback: %v", err)
	}
	if _, err := service.Login(context.Background(), "admin@example.com", "correct horse battery staple", "rollback-client"); err != nil {
		t.Fatalf("old password did not survive rollback: %v", err)
	}
}
