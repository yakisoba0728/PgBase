package adminauth

import (
	"context"
	"errors"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/yakisoba0728/PgBase/internal/database"
)

type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

type countingPasswords struct {
	delegate     Passwords
	verifyHashes []string
}

func (p *countingPasswords) Hash(password string) (string, error) {
	return p.delegate.Hash(password)
}

func (p *countingPasswords) Verify(encodedHash, password string) error {
	p.verifyHashes = append(p.verifyHashes, encodedHash)
	return p.delegate.Verify(encodedHash, password)
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

type failingUUIDReader struct{}

func (failingUUIDReader) Read([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }

func seedBlockedRateLimit(t *testing.T, pool *database.Pool, subjectHash []byte, now time.Time) {
	t.Helper()
	if _, err := pool.Exec(context.Background(), `insert into pgbase_meta.admin_rate_limits
		(subject_hash, window_started_at, failures, blocked_until, updated_at)
		values ($1, $2::timestamptz, 1000, $2::timestamptz + interval '15 minutes', $2::timestamptz)`, subjectHash, now); err != nil {
		t.Fatalf("seed legacy global rate limit: %v", err)
	}
}

type immediateInvalidPasswords struct {
	verifyCount int
}

func (*immediateInvalidPasswords) Hash(string) (string, error) {
	return "", errors.New("unexpected hash")
}
func (p *immediateInvalidPasswords) Verify(string, string) error {
	p.verifyCount++
	return ErrInvalidCredentials
}

type concurrencyPasswords struct {
	delegate Passwords
	started  chan struct{}
	release  chan struct{}
	mu       sync.Mutex
	active   int
	maximum  int
}

func (p *concurrencyPasswords) Hash(password string) (string, error) {
	p.start()
	defer p.finish()
	return p.delegate.Hash(password)
}
func (p *concurrencyPasswords) Verify(encodedHash, password string) error {
	p.start()
	defer p.finish()
	return p.delegate.Verify(encodedHash, password)
}

func (p *concurrencyPasswords) start() {
	p.mu.Lock()
	p.active++
	if p.active > p.maximum {
		p.maximum = p.active
	}
	p.mu.Unlock()
	p.started <- struct{}{}
	<-p.release
}

func (p *concurrencyPasswords) finish() {
	p.mu.Lock()
	p.active--
	p.mu.Unlock()
}

func TestServiceAdmissionClassesAreIndependentNonBlockingAndReusable(t *testing.T) {
	service := &Service{
		credentialSlots: make(chan struct{}, 3),
		sessionSlots:    make(chan struct{}, 2),
		probeSlots:      make(chan struct{}, 1),
	}
	classes := []struct {
		name  string
		slots chan struct{}
	}{
		{name: "credential", slots: service.credentialSlots},
		{name: "session", slots: service.sessionSlots},
		{name: "probe", slots: service.probeSlots},
	}
	for _, class := range classes {
		t.Run(class.name, func(t *testing.T) {
			releases := make([]func(), 0, cap(class.slots))
			for range cap(class.slots) {
				release, err := acquireAdmission(class.slots)
				if err != nil {
					t.Fatalf("acquire admission within capacity: %v", err)
				}
				releases = append(releases, release)
			}
			if release, err := acquireAdmission(class.slots); !errors.Is(err, ErrRateLimited) || release != nil {
				t.Fatalf("saturated admission release is nil = %t, error = %v; want true and ErrRateLimited", release == nil, err)
			}
			for _, release := range releases {
				release()
			}
			if release, err := acquireAdmission(class.slots); err != nil {
				t.Fatalf("acquire admission after release: %v", err)
			} else {
				release()
			}
			if occupied := len(class.slots); occupied != 0 {
				t.Fatalf("occupied admission slots after release = %d, want 0", occupied)
			}
		})
	}

	credentialReleases := make([]func(), 0, cap(service.credentialSlots))
	for range cap(service.credentialSlots) {
		release, err := acquireAdmission(service.credentialSlots)
		if err != nil {
			t.Fatal(err)
		}
		credentialReleases = append(credentialReleases, release)
	}
	defer func() {
		for _, release := range credentialReleases {
			release()
		}
	}()
	for name, slots := range map[string]chan struct{}{"session": service.sessionSlots, "probe": service.probeSlots} {
		if release, err := acquireAdmission(slots); err != nil {
			t.Fatalf("%s admission blocked by credential saturation: %v", name, err)
		} else {
			release()
		}
	}
}

func TestServiceBoundsConcurrentPasswordWork(t *testing.T) {
	passwords := &concurrencyPasswords{
		delegate: NewArgon2idPasswords(),
		started:  make(chan struct{}, 3),
		release:  make(chan struct{}),
	}
	service := NewService(nil, passwords, []byte("0123456789abcdef0123456789abcdef"), "", time.Hour, nil)
	done := make(chan error, 3)
	for range 3 {
		go func() {
			release, err := service.acquirePasswordWork(context.Background())
			if err == nil {
				defer release()
				err = service.passwords.Verify(dummyPasswordHash, dummyPassword)
			}
			done <- err
		}()
	}
	for range passwordWorkConcurrency {
		select {
		case <-passwords.started:
		case <-time.After(time.Second):
			t.Fatal("password verification did not start")
		}
	}
	select {
	case <-passwords.started:
		t.Fatal("third password verification bypassed the concurrency bound")
	case <-time.After(50 * time.Millisecond):
	}
	close(passwords.release)
	for range 3 {
		if err := <-done; err != nil && !errors.Is(err, ErrInvalidCredentials) {
			t.Fatal(err)
		}
	}
	if passwords.maximum != passwordWorkConcurrency {
		t.Fatalf("maximum concurrent password work = %d, want %d", passwords.maximum, passwordWorkConcurrency)
	}
}

type fixedHashPasswords struct {
	hash     string
	delegate Passwords
}

func (p *fixedHashPasswords) Hash(string) (string, error) { return p.hash, nil }
func (p *fixedHashPasswords) Verify(encodedHash, password string) error {
	return p.delegate.Verify(encodedHash, password)
}
