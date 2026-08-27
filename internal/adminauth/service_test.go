package adminauth

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
)

type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) Add(duration time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(duration)
	c.mu.Unlock()
}

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
	if setup.Administrator.Email != "admin@example.com" || setup.Administrator.ID.Version() != uuid.Version(4) {
		t.Fatalf("setup administrator = %#v", setup.Administrator)
	}
	if setup.RawToken == "" || !setup.ExpiresAt.Equal(clock.Now().Add(12*time.Hour)) {
		t.Fatalf("setup session = %#v", setup)
	}
	if got, err := service.Authenticate(context.Background(), setup.RawToken); err != nil || got != setup.Administrator {
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

func TestServiceLoginRateLimitIsSharedAndClearsOnSuccess(t *testing.T) {
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
		if _, err := services[attempt%2].Login(context.Background(), "ADMIN@example.com", "incorrect horse battery staple", "192.0.2.1"); !errors.Is(err, ErrInvalidCredentials) {
			t.Fatalf("attempt %d error = %v, want ErrInvalidCredentials", attempt+1, err)
		}
	}
	if _, err := services[1].Login(context.Background(), "admin@example.com", "correct horse battery staple", "192.0.2.1"); !errors.Is(err, ErrRateLimited) {
		t.Fatalf("blocked correct login error = %v, want ErrRateLimited", err)
	}
	clock.Add(15*time.Minute + time.Nanosecond)
	if _, err := services[0].Login(context.Background(), "admin@example.com", "correct horse battery staple", "192.0.2.1"); err != nil {
		t.Fatalf("login after block: %v", err)
	}
	var count int
	if err := pool.QueryRow(context.Background(), "select count(*) from pgbase_meta.admin_rate_limits").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("rate-limit row count after success = %d, want 0", count)
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
