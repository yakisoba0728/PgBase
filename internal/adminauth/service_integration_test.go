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
			_, err := service.Setup(context.Background(), "01234567890123456789012345678901", " \u3000ADMIN@Example.COM\u3000 ", "correct horse battery staple", "192.0.2."+string(rune('1'+i)))
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
		case errors.Is(err, ErrSetupUnavailable):
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

func TestServiceLoginKDFQueueDoesNotBorrowDatabaseResources(t *testing.T) {
	pool := newAdminAuthPool(t)
	clock := &fakeClock{now: time.Date(2026, 8, 28, 1, 2, 3, 0, time.UTC)}
	secret := []byte("0123456789abcdef0123456789abcdef")
	setupToken := "01234567890123456789012345678901"
	realPasswords := NewArgon2idPasswords()
	if _, err := NewService(pool, realPasswords, secret, setupToken, time.Hour, clock).Setup(
		context.Background(), setupToken, "admin@example.com", "correct horse battery staple", "setup-client"); err != nil {
		t.Fatal(err)
	}

	blocking := &concurrencyPasswords{
		delegate: realPasswords,
		started:  make(chan struct{}, 3),
		release:  make(chan struct{}),
	}
	release := sync.OnceFunc(func() { close(blocking.release) })
	defer release()
	service := NewService(pool, blocking, secret, setupToken, time.Hour, clock)
	errorsByAttempt := make(chan error, 3)
	for attempt := 0; attempt < 3; attempt++ {
		go func(attempt int) {
			_, err := service.Login(
				context.Background(),
				fmt.Sprintf("unknown-%d@example.com", attempt),
				"pgbase dummy credential 2026",
				fmt.Sprintf("queued-network-%d", attempt),
			)
			errorsByAttempt <- err
		}(attempt)
	}
	for range passwordVerificationConcurrency {
		select {
		case <-blocking.started:
		case <-time.After(2 * time.Second):
			t.Fatal("login did not reach password verification")
		}
	}

	// The third request has completed its short network admission before it can
	// queue behind the two KDF slots. Give it a scheduling turn, then inspect the
	// public pool resource snapshot while every verifier remains blocked.
	time.Sleep(50 * time.Millisecond)
	if acquired := pool.Stats().AcquiredConns; acquired != 0 {
		t.Errorf("database connections acquired while KDF work is queued/running = %d, want 0", acquired)
	}

	release()
	for range 3 {
		if err := <-errorsByAttempt; !errors.Is(err, ErrInvalidCredentials) {
			t.Fatalf("queued login error = %v, want ErrInvalidCredentials", err)
		}
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

func TestServiceRejectsInvalidSetupTokenWithoutPersistingIt(t *testing.T) {
	pool := newAdminAuthPool(t)
	clock := &fakeClock{now: time.Date(2026, 8, 28, 1, 2, 3, 0, time.UTC)}
	configured := "01234567890123456789012345678901"
	presented := "this-token-must-never-be-persisted"
	service := NewService(pool, NewArgon2idPasswords(), []byte("0123456789abcdef0123456789abcdef"), configured, time.Hour, clock)
	if _, err := service.Setup(context.Background(), presented, "admin@example.com", "correct horse battery staple", "192.0.2.1"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("Setup invalid token error = %v, want ErrInvalidCredentials", err)
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
