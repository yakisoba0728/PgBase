//go:build integration

package userauth

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/yakisoba0728/PgBase/internal/adminauth"
)

func TestManagedUserLifecycle(t *testing.T) {
	pool := newUserAuthPool(t)
	service := NewService(pool, adminauth.NewArgon2idPasswords(), []byte("0123456789abcdef0123456789abcdef"), time.Hour)
	ctx := context.Background()
	password := "correct horse battery staple"
	registered, err := service.Register(ctx, "managed@example.com", password, "managed-register")
	if err != nil {
		t.Fatal(err)
	}
	second, err := service.Login(ctx, registered.User.Email, password, "managed-login")
	if err != nil {
		t.Fatal(err)
	}

	revoked, err := service.RevokeUserSessions(ctx, registered.User.ID)
	if err != nil || revoked != 2 {
		t.Fatalf("RevokeUserSessions = %d, %v; want 2", revoked, err)
	}
	for _, token := range []string{registered.RawToken, second.RawToken} {
		if _, err := service.Authenticate(ctx, token); !errors.Is(err, ErrInvalidCredentials) {
			t.Fatalf("Authenticate revoked token = %v", err)
		}
	}
	if revoked, err := service.RevokeUserSessions(ctx, registered.User.ID); err != nil || revoked != 0 {
		t.Fatalf("repeat RevokeUserSessions = %d, %v; want 0", revoked, err)
	}

	fresh, err := service.Login(ctx, registered.User.Email, password, "managed-fresh-login")
	if err != nil {
		t.Fatal(err)
	}
	inactive, err := service.SetUserActive(ctx, registered.User.ID, false)
	if err != nil || inactive.Active || !inactive.UpdatedAt.After(time.Time{}) {
		t.Fatalf("SetUserActive(false) = %#v, %v", inactive, err)
	}
	if _, err := service.Authenticate(ctx, fresh.RawToken); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("Authenticate after deactivation = %v", err)
	}
	if _, err := service.Login(ctx, registered.User.Email, password, "managed-inactive-login"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("Login inactive user = %v", err)
	}
	active, err := service.SetUserActive(ctx, registered.User.ID, true)
	if err != nil || !active.Active {
		t.Fatalf("SetUserActive(true) = %#v, %v", active, err)
	}
	if _, err := service.Authenticate(ctx, fresh.RawToken); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("reactivation revived token: %v", err)
	}
	if _, err := service.Login(ctx, registered.User.Email, password, "managed-reactivated-login"); err != nil {
		t.Fatalf("fresh login after reactivation = %v", err)
	}
	missing := uuid.New()
	if _, err := service.SetUserActive(ctx, missing, false); !errors.Is(err, ErrNotFound) {
		t.Fatalf("SetUserActive missing user = %v", err)
	}
	if _, err := service.RevokeUserSessions(ctx, missing); !errors.Is(err, ErrNotFound) {
		t.Fatalf("RevokeUserSessions missing user = %v", err)
	}
	if err := service.ChangePassword(ctx, missing, password, "replacement horse battery"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("ChangePassword missing user = %v", err)
	}
}

func TestChangePasswordRequiresCurrentAndRevokesEverySession(t *testing.T) {
	pool := newUserAuthPool(t)
	service := NewService(pool, adminauth.NewArgon2idPasswords(), []byte("0123456789abcdef0123456789abcdef"), time.Hour)
	ctx := context.Background()
	oldPassword, newPassword := "old correct horse battery", "new correct horse battery"
	first, err := service.Register(ctx, "password@example.com", oldPassword, "password-register")
	if err != nil {
		t.Fatal(err)
	}
	second, err := service.Login(ctx, first.User.Email, oldPassword, "password-login")
	if err != nil {
		t.Fatal(err)
	}
	if err := service.ChangePassword(ctx, first.User.ID, "incorrect current password", newPassword); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("ChangePassword incorrect current = %v", err)
	}
	if _, err := service.Authenticate(ctx, first.RawToken); err != nil {
		t.Fatalf("failed change revoked token: %v", err)
	}
	preserved, err := service.Login(ctx, first.User.Email, oldPassword, "password-preserved-login")
	if err != nil {
		t.Fatalf("failed change replaced old password: %v", err)
	}

	if err := service.ChangePassword(ctx, first.User.ID, oldPassword, newPassword); err != nil {
		t.Fatal(err)
	}
	for _, token := range []string{first.RawToken, second.RawToken, preserved.RawToken} {
		if _, err := service.Authenticate(ctx, token); !errors.Is(err, ErrInvalidCredentials) {
			t.Fatalf("Authenticate token after password change = %v", err)
		}
	}
	if _, err := service.Login(ctx, first.User.Email, oldPassword, "password-old-login"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("Login old password = %v", err)
	}
	if _, err := service.Login(ctx, first.User.Email, newPassword, "password-new-login"); err != nil {
		t.Fatalf("Login new password = %v", err)
	}
}

func TestListUsersUsesUUIDKeysetAndContainsNoCredentials(t *testing.T) {
	pool := newUserAuthPool(t)
	service := NewService(pool, adminauth.NewArgon2idPasswords(), []byte("0123456789abcdef0123456789abcdef"), time.Hour)
	ctx := context.Background()
	for index, email := range []string{"list-a@example.com", "list-b@example.com", "list-c@example.com"} {
		if _, err := service.Register(ctx, email, "correct horse battery staple", "list-register-"+string(rune('a'+index))); err != nil {
			t.Fatal(err)
		}
	}
	first, err := service.ListUsers(ctx, UserQuery{Limit: 2, Total: true})
	if err != nil || len(first.Items) != 2 || first.NextAfter == uuid.Nil || first.Total != 3 || first.TotalCapped {
		t.Fatalf("first page = %#v, %v", first, err)
	}
	if first.Items[0].ID.String() >= first.Items[1].ID.String() {
		t.Fatalf("first page not UUID ascending: %s, %s", first.Items[0].ID, first.Items[1].ID)
	}
	second, err := service.ListUsers(ctx, UserQuery{After: first.NextAfter, Limit: 2})
	if err != nil || len(second.Items) != 1 || second.NextAfter != uuid.Nil || second.Items[0].ID.String() <= first.NextAfter.String() {
		t.Fatalf("second page = %#v, %v", second, err)
	}
	if _, err := service.ListUsers(ctx, UserQuery{}); !errors.Is(err, ErrValidation) {
		t.Fatalf("ListUsers invalid limit = %v", err)
	}
	realm := uuid.New()
	if _, err := service.CreateAuthCollection(ctx, CreateAuthCollectionInput{ID: realm, Name: "list_" + realm.String()[:8], RegistrationEnabled: true}); err != nil {
		t.Fatal(err)
	}
	other, err := service.RegisterInCollection(ctx, realm, "list-b@example.com", "correct horse battery staple", "list-register-realm")
	if err != nil {
		t.Fatal(err)
	}
	byEmail, err := service.ListUsers(ctx, UserQuery{Limit: 10, Search: " LIST-B@example.com ", Total: true})
	if err != nil || len(byEmail.Items) != 2 || byEmail.Total != 2 {
		t.Fatalf("email search across realms = %#v, %v", byEmail, err)
	}
	inRealm, err := service.ListUsers(ctx, UserQuery{Limit: 10, AuthCollectionID: realm, Search: "list-b@example.com"})
	if err != nil || len(inRealm.Items) != 1 || inRealm.Items[0].ID != other.User.ID || inRealm.Items[0].AuthCollectionID != realm {
		t.Fatalf("email search in realm = %#v, %v", inRealm, err)
	}
	byID, err := service.ListUsers(ctx, UserQuery{Limit: 1, Search: other.User.ID.String()})
	if err != nil || len(byID.Items) != 1 || byID.Items[0].Email != "list-b@example.com" {
		t.Fatalf("id search = %#v, %v", byID, err)
	}
	if none, err := service.ListUsers(ctx, UserQuery{Limit: 10, Search: "list"}); err != nil || len(none.Items) != 4 {
		t.Fatalf("partial email search = %#v, %v", none, err)
	}
	records, next, err := service.ListAuthRecords(ctx, DefaultCollectionID, uuid.Nil, 2)
	if err != nil || len(records) != 2 || next != records[1].ID {
		t.Fatalf("first record page = %d %s %v", len(records), next, err)
	}
	records, next, err = service.ListAuthRecords(ctx, DefaultCollectionID, next, 2)
	if err != nil || len(records) != 1 || next != uuid.Nil {
		t.Fatalf("last record page = %d %s %v", len(records), next, err)
	}
}

type pausingPasswords struct {
	delegate Passwords
	entered  chan struct{}
	release  chan struct{}
	once     sync.Once
}

type twoPartyPasswords struct {
	delegate Passwords
	mu       sync.Mutex
	count    int
	entered  chan struct{}
	release  chan struct{}
}

func (p *twoPartyPasswords) Hash(password string) (string, error) { return p.delegate.Hash(password) }
func (p *twoPartyPasswords) Verify(hash, password string) error {
	p.mu.Lock()
	p.count++
	if p.count == 2 {
		close(p.entered)
	}
	p.mu.Unlock()
	<-p.release
	return p.delegate.Verify(hash, password)
}

func (p *pausingPasswords) Hash(password string) (string, error) { return p.delegate.Hash(password) }
func (p *pausingPasswords) Verify(hash, password string) error {
	p.once.Do(func() { close(p.entered); <-p.release })
	return p.delegate.Verify(hash, password)
}

func TestLoginCannotIssueSessionAfterConcurrentDeactivation(t *testing.T) {
	pool := newUserAuthPool(t)
	real := adminauth.NewArgon2idPasswords()
	setup := NewService(pool, real, []byte("0123456789abcdef0123456789abcdef"), time.Hour)
	registered, err := setup.Register(context.Background(), "race@example.com", "correct horse battery staple", "race-register")
	if err != nil {
		t.Fatal(err)
	}
	pausing := &pausingPasswords{delegate: real, entered: make(chan struct{}), release: make(chan struct{})}
	loginService := NewService(pool, pausing, []byte("0123456789abcdef0123456789abcdef"), time.Hour)
	loginDone := make(chan error, 1)
	go func() {
		_, err := loginService.Login(context.Background(), registered.User.Email, "correct horse battery staple", "race-login")
		loginDone <- err
	}()
	<-pausing.entered
	if _, err := setup.SetUserActive(context.Background(), registered.User.ID, false); err != nil {
		t.Fatal(err)
	}
	close(pausing.release)
	if err := <-loginDone; !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("concurrent Login = %v, want ErrInvalidCredentials", err)
	}
	var activeSessions int
	if err := pool.QueryRow(context.Background(), `select count(*) from pgbase_meta.user_sessions where user_id=$1 and revoked_at is null`, registered.User.ID).Scan(&activeSessions); err != nil {
		t.Fatal(err)
	}
	if activeSessions != 0 {
		t.Fatalf("active sessions after deactivation race = %d", activeSessions)
	}
}

func TestRevokeUserSessionsWaitsForUserRowLock(t *testing.T) {
	pool := newUserAuthPool(t)
	service := NewService(pool, adminauth.NewArgon2idPasswords(), []byte("0123456789abcdef0123456789abcdef"), time.Hour)
	registered, err := service.Register(context.Background(), "revoke-race@example.com", "correct horse battery staple", "revoke-race-register")
	if err != nil {
		t.Fatal(err)
	}
	connection, err := pool.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Release()
	tx, err := connection.Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	if _, err := tx.Exec(context.Background(), `select 1 from pgbase_meta.users where id=$1 for update`, registered.User.ID); err != nil {
		t.Fatal(err)
	}
	blockedContext, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if _, err := service.RevokeUserSessions(blockedContext, registered.User.ID); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("RevokeUserSessions under user lock = %v, want deadline", err)
	}
	if err := tx.Rollback(context.Background()); err != nil {
		t.Fatal(err)
	}
	if revoked, err := service.RevokeUserSessions(context.Background(), registered.User.ID); err != nil || revoked != 1 {
		t.Fatalf("RevokeUserSessions after lock release = %d, %v; want 1", revoked, err)
	}
}

func TestConcurrentPasswordChangesRecheckLockedCredential(t *testing.T) {
	pool := newUserAuthPool(t)
	real := adminauth.NewArgon2idPasswords()
	setup := NewService(pool, real, []byte("0123456789abcdef0123456789abcdef"), time.Hour)
	registered, err := setup.Register(context.Background(), "change-race@example.com", "correct horse battery staple", "change-race-register")
	if err != nil {
		t.Fatal(err)
	}
	barrier := &twoPartyPasswords{delegate: real, entered: make(chan struct{}), release: make(chan struct{})}
	service := NewService(pool, barrier, []byte("0123456789abcdef0123456789abcdef"), time.Hour)
	results := make(chan error, 2)
	for _, replacement := range []string{"replacement password alpha", "replacement password bravo"} {
		go func(password string) {
			results <- service.ChangePassword(context.Background(), registered.User.ID, "correct horse battery staple", password)
		}(replacement)
	}
	<-barrier.entered
	close(barrier.release)
	succeeded, rejected := 0, 0
	for range 2 {
		err := <-results
		switch {
		case err == nil:
			succeeded++
		case errors.Is(err, ErrInvalidCredentials):
			rejected++
		default:
			t.Fatalf("concurrent ChangePassword = %v", err)
		}
	}
	if succeeded != 1 || rejected != 1 {
		t.Fatalf("concurrent changes succeeded/rejected = %d/%d, want 1/1", succeeded, rejected)
	}
}

func TestChangePasswordSharedAccountLimitPreservesCredentialAndSessions(t *testing.T) {
	pool := newUserAuthPool(t)
	secret := []byte("0123456789abcdef0123456789abcdef")
	passwords := adminauth.NewArgon2idPasswords()
	firstService := NewService(pool, passwords, secret, time.Hour)
	secondService := NewService(pool, passwords, secret, time.Hour)
	ctx := context.Background()
	currentPassword := "correct horse battery staple"
	registered, err := firstService.Register(ctx, "limited-change@example.com", currentPassword, "limited-change-register")
	if err != nil {
		t.Fatal(err)
	}
	secondSession, err := firstService.Login(ctx, registered.User.Email, currentPassword, "limited-change-login")
	if err != nil {
		t.Fatal(err)
	}
	var originalHash string
	if err := pool.QueryRow(ctx, `select password_hash from pgbase_meta.users where id=$1`, registered.User.ID).Scan(&originalHash); err != nil {
		t.Fatal(err)
	}

	for attempt := 1; attempt <= loginAccountLimit; attempt++ {
		service := firstService
		if attempt%2 == 0 {
			service = secondService
		}
		err := service.ChangePassword(ctx, registered.User.ID, "incorrect current password", "replacement horse battery")
		if attempt < loginAccountLimit && !errors.Is(err, ErrInvalidCredentials) {
			t.Fatalf("attempt %d = %v, want ErrInvalidCredentials", attempt, err)
		}
		if attempt == loginAccountLimit && !errors.Is(err, ErrRateLimited) {
			t.Fatalf("threshold attempt = %v, want ErrRateLimited", err)
		}
	}
	if err := secondService.ChangePassword(ctx, registered.User.ID, currentPassword, "replacement horse battery"); !errors.Is(err, ErrRateLimited) {
		t.Fatalf("correct password during account block = %v, want ErrRateLimited", err)
	}

	var currentHash string
	if err := pool.QueryRow(ctx, `select password_hash from pgbase_meta.users where id=$1`, registered.User.ID).Scan(&currentHash); err != nil {
		t.Fatal(err)
	}
	if currentHash != originalHash {
		t.Fatal("failed password-change attempts mutated the credential hash")
	}
	for _, token := range []string{registered.RawToken, secondSession.RawToken} {
		if _, err := firstService.Authenticate(ctx, token); err != nil {
			t.Fatalf("failed password-change attempts revoked an original session: %v", err)
		}
	}
}
