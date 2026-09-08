package userauth

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"net/mail"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/webisnoteasy/PgBase/internal/adminauth"
	"github.com/webisnoteasy/PgBase/internal/database"
)

const (
	minimumPasswordBytes = 12
	maximumPasswordBytes = 1024
	maximumTokenBytes    = 128
	loginNetworkLimit    = 20
	loginAccountLimit    = 5
	registerNetworkLimit = 5
)

var dummyPasswordHash = "$argon2id$v=19$m=65536,t=3,p=4$cGdiYXNlLWR1bW15LXYxIQ$AnRqzUvZayQh6Siwo8bh8R9TAt8al8dpPzH25DzI5Tk"
var dummyPassword = "pgbase dummy credential 2026"

func (s *Service) Register(ctx context.Context, email, password, clientAddress string) (Session, error) {
	if err := s.ready(); err != nil {
		return Session{}, err
	}
	release, err := admit(s.credentialSlots)
	if err != nil {
		return Session{}, err
	}
	defer release()
	normalized, err := normalizeEmail(email)
	if err != nil {
		return Session{}, err
	}
	if err := validatePassword(password); err != nil {
		return Session{}, err
	}
	if err := validateClientAddress(clientAddress); err != nil {
		return Session{}, err
	}
	now, err := s.databaseNow(ctx)
	if err != nil {
		return Session{}, err
	}
	if err := s.maybeCleanup(ctx); err != nil {
		return Session{}, err
	}
	bucket := s.subjectHash("register", "network", clientAddress)
	if err := s.consumeRateLimit(ctx, bucket, registerNetworkLimit, now); err != nil {
		return Session{}, err
	}
	releaseHash, err := admit(s.passwordSlots)
	if err != nil {
		return Session{}, err
	}
	defer releaseHash()
	passwordHash, err := s.passwords.Hash(password)
	if err != nil {
		return Session{}, err
	}
	user := User{ID: uuid.New(), Email: normalized, CreatedAt: now}
	issued, err := s.createUserAndSession(ctx, user, passwordHash, now)
	if database.Classify(err) == database.KindConflict {
		return Session{}, ErrConflict
	}
	return issued, err
}

func (s *Service) Login(ctx context.Context, email, password, clientAddress string) (Session, error) {
	if err := s.ready(); err != nil {
		return Session{}, err
	}
	release, err := admit(s.credentialSlots)
	if err != nil {
		return Session{}, err
	}
	defer release()
	normalized, emailErr := normalizeEmail(email)
	if emailErr != nil {
		normalized = ""
	}
	if err := validateClientAddress(clientAddress); err != nil {
		return Session{}, err
	}
	passwordErr := validatePassword(password)
	now, err := s.databaseNow(ctx)
	if err != nil {
		return Session{}, err
	}
	if err := s.maybeCleanup(ctx); err != nil {
		return Session{}, err
	}
	accountSubject := normalized
	if emailErr != nil {
		// All malformed emails share one fixed-size bucket. Valid unknown emails
		// use their normalized form exactly like existing accounts, so account
		// admission does not disclose whether a user exists.
		accountSubject = "<invalid>"
	}
	buckets := []rateLimitBucket{
		{hash: s.subjectHash("login", "account", accountSubject), limit: loginAccountLimit},
		{hash: s.subjectHash("login", "network", clientAddress), limit: loginNetworkLimit},
	}
	if err := s.ensureBucketsNotBlocked(ctx, buckets, now); err != nil {
		return Session{}, err
	}
	var user User
	var hash string
	lookupErr := error(ErrInvalidCredentials)
	if emailErr == nil && passwordErr == nil {
		user, hash, lookupErr = s.userByEmail(ctx, normalized, false)
		if lookupErr != nil && !errors.Is(lookupErr, ErrInvalidCredentials) {
			return Session{}, lookupErr
		}
	}
	releaseHash, err := admit(s.passwordSlots)
	if err != nil {
		return Session{}, err
	}
	shapeValid := emailErr == nil && passwordErr == nil && lookupErr == nil
	verifyHash, verifyPassword := hash, password
	if !shapeValid {
		verifyHash, verifyPassword = dummyPasswordHash, dummyPassword
	}
	verifyErr := s.passwords.Verify(verifyHash, verifyPassword)
	releaseHash()
	if verifyErr != nil && !isPasswordMismatch(verifyErr) {
		return Session{}, verifyErr
	}
	if !shapeValid || verifyErr != nil {
		if err := s.recordFailures(ctx, buckets, now); err != nil {
			return Session{}, err
		}
		return Session{}, ErrInvalidCredentials
	}
	issued, err := s.issueCheckedSession(ctx, user, hash, now)
	if errors.Is(err, ErrInvalidCredentials) {
		_ = s.recordFailures(ctx, buckets, now)
	}
	return issued, err
}

func (s *Service) Authenticate(ctx context.Context, rawToken string) (Principal, error) {
	if err := s.ready(); err != nil {
		return Principal{}, err
	}
	if err := validateToken(rawToken); err != nil {
		return Principal{}, ErrInvalidCredentials
	}
	release, err := admit(s.sessionSlots)
	if err != nil {
		return Principal{}, err
	}
	defer release()
	var p Principal
	err = s.pool.QueryRow(ctx, `select u.id,u.email,u.created_at,s.expires_at from pgbase_meta.user_sessions s join pgbase_meta.users u on u.id=s.user_id where s.token_hash=$1 and s.revoked_at is null and s.expires_at>CURRENT_TIMESTAMP and u.active`, s.tokenHash(rawToken)).Scan(&p.User.ID, &p.User.Email, &p.User.CreatedAt, &p.ExpiresAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Principal{}, ErrInvalidCredentials
	}
	if err != nil {
		return Principal{}, fmt.Errorf("authenticate user session: %w", err)
	}
	p.User.CreatedAt = p.User.CreatedAt.UTC()
	p.ExpiresAt = p.ExpiresAt.UTC()
	return p, nil
}

func (s *Service) Logout(ctx context.Context, rawToken string) error {
	if err := s.ready(); err != nil {
		return err
	}
	if err := validateToken(rawToken); err != nil {
		return ErrValidation
	}
	release, err := admit(s.sessionSlots)
	if err != nil {
		return err
	}
	defer release()
	_, err = s.pool.Exec(ctx, `update pgbase_meta.user_sessions set revoked_at=coalesce(revoked_at,CURRENT_TIMESTAMP) where token_hash=$1`, s.tokenHash(rawToken))
	if err != nil {
		return fmt.Errorf("revoke user session: %w", err)
	}
	return nil
}

func (s *Service) createUserAndSession(ctx context.Context, user User, passwordHash string, now time.Time) (Session, error) {
	var issued Session
	err := s.pool.InTx(ctx, pgx.TxOptions{}, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `insert into pgbase_meta.users(id,email,password_hash,active,created_at,updated_at) values($1,$2,$3,true,$4,$4)`, user.ID, user.Email, passwordHash, now)
		if err != nil {
			return fmt.Errorf("create user: %w", err)
		}
		var e error
		issued, e = s.insertSession(ctx, tx, user, now)
		return e
	})
	return issued, err
}

func (s *Service) issueCheckedSession(ctx context.Context, user User, expectedHash string, now time.Time) (Session, error) {
	var issued Session
	var opErr error
	err := s.pool.InTx(ctx, pgx.TxOptions{}, func(tx pgx.Tx) error {
		current, hash, err := userByEmailDB(ctx, tx, user.Email, true)
		if err != nil {
			return err
		}
		if current.ID != user.ID || !hmac.Equal([]byte(hash), []byte(expectedHash)) {
			opErr = ErrInvalidCredentials
			return nil
		}
		issued, err = s.insertSession(ctx, tx, current, now)
		return err
	})
	if err != nil {
		return Session{}, err
	}
	if opErr != nil {
		return Session{}, opErr
	}
	return issued, nil
}

func (s *Service) insertSession(ctx context.Context, db database.DBTX, user User, now time.Time) (Session, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return Session{}, fmt.Errorf("generate user session token: %w", err)
	}
	token := base64.RawURLEncoding.EncodeToString(raw)
	expires := now.Add(s.ttl)
	_, err := db.Exec(ctx, `insert into pgbase_meta.user_sessions(id,user_id,token_hash,expires_at,revoked_at,created_at) values($1,$2,$3,$4,null,$5)`, uuid.New(), user.ID, s.tokenHash(token), expires, now)
	if err != nil {
		return Session{}, fmt.Errorf("create user session: %w", err)
	}
	return Session{User: user, RawToken: token, ExpiresAt: expires}, nil
}

func (s *Service) userByEmail(ctx context.Context, email string, lock bool) (User, string, error) {
	return userByEmailDB(ctx, s.pool, email, lock)
}
func userByEmailDB(ctx context.Context, db database.DBTX, email string, lock bool) (User, string, error) {
	q := `select id,email,created_at,password_hash from pgbase_meta.users where email=$1 and active`
	if lock {
		q += " for update"
	}
	var u User
	var hash string
	err := db.QueryRow(ctx, q, email).Scan(&u.ID, &u.Email, &u.CreatedAt, &hash)
	if errors.Is(err, pgx.ErrNoRows) {
		return User{}, "", ErrInvalidCredentials
	}
	if err != nil {
		return User{}, "", fmt.Errorf("read user: %w", err)
	}
	u.CreatedAt = u.CreatedAt.UTC()
	return u, hash, nil
}

func (s *Service) consumeRateLimit(ctx context.Context, hash []byte, limit int, now time.Time) error {
	if err := s.ensureNotBlocked(ctx, hash, now); err != nil {
		return err
	}
	return s.recordFailure(ctx, hash, limit, now)
}

type rateLimitBucket struct {
	hash  []byte
	limit int
}

func (s *Service) ensureBucketsNotBlocked(ctx context.Context, buckets []rateLimitBucket, now time.Time) error {
	for _, bucket := range buckets {
		if err := s.ensureNotBlocked(ctx, bucket.hash, now); err != nil {
			return err
		}
	}
	return nil
}

// recordFailures advances every applicable failure bucket in one transaction.
// Stable non-blocking advisory locks serialize decisions across server
// instances without allowing one contended subject to hold another lock while
// waiting. Successful authentication deliberately does not clear account
// history; signals decay when the 15-minute window rolls over.
func (s *Service) recordFailures(ctx context.Context, buckets []rateLimitBucket, now time.Time) error {
	ordered := append([]rateLimitBucket(nil), buckets...)
	sort.Slice(ordered, func(i, j int) bool { return advisoryKey(ordered[i].hash) < advisoryKey(ordered[j].hash) })
	var blocked bool
	err := s.pool.InTx(ctx, pgx.TxOptions{}, func(tx pgx.Tx) error {
		for _, bucket := range ordered {
			var acquired bool
			if err := tx.QueryRow(ctx, `select pg_try_advisory_xact_lock($1)`, advisoryKey(bucket.hash)).Scan(&acquired); err != nil {
				return err
			}
			if !acquired {
				return ErrRateLimited
			}
		}
		for _, bucket := range buckets {
			bucketBlocked, err := recordFailureInTx(ctx, tx, bucket.hash, bucket.limit, now)
			if err != nil {
				return err
			}
			blocked = blocked || bucketBlocked
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("record user rate limits: %w", err)
	}
	if blocked {
		return ErrRateLimited
	}
	return nil
}
func (s *Service) ensureNotBlocked(ctx context.Context, hash []byte, now time.Time) error {
	var blocked bool
	err := s.pool.QueryRow(ctx, `select coalesce((select blocked_until>$2 from pgbase_meta.user_auth_rate_limits where subject_hash=$1),false)`, hash, now).Scan(&blocked)
	if err != nil {
		return fmt.Errorf("read user rate limit: %w", err)
	}
	if blocked {
		return ErrRateLimited
	}
	return nil
}
func (s *Service) recordFailure(ctx context.Context, hash []byte, limit int, now time.Time) error {
	var blocked bool
	err := s.pool.InTx(ctx, pgx.TxOptions{}, func(tx pgx.Tx) error {
		var ok bool
		if err := tx.QueryRow(ctx, `select pg_try_advisory_xact_lock($1)`, advisoryKey(hash)).Scan(&ok); err != nil {
			return err
		}
		if !ok {
			return ErrRateLimited
		}
		var err error
		blocked, err = recordFailureInTx(ctx, tx, hash, limit, now)
		return err
	})
	if err != nil {
		return fmt.Errorf("record user rate limit: %w", err)
	}
	if blocked {
		return ErrRateLimited
	}
	return nil
}

func recordFailureInTx(ctx context.Context, tx pgx.Tx, hash []byte, limit int, now time.Time) (bool, error) {
	var blocked bool
	err := tx.QueryRow(ctx, `insert into pgbase_meta.user_auth_rate_limits as r(subject_hash,window_started_at,failures,blocked_until,updated_at) values($1,$2,1,null,$2) on conflict(subject_hash) do update set window_started_at=case when r.window_started_at<=$2-interval '15 minutes' then $2 else r.window_started_at end,failures=case when r.window_started_at<=$2-interval '15 minutes' then 1 else r.failures+1 end,blocked_until=case when (case when r.window_started_at<=$2-interval '15 minutes' then 1 else r.failures+1 end)>=$3 then $2+interval '15 minutes' else r.blocked_until end,updated_at=$2 returning coalesce(blocked_until>$2,false)`, hash, now, limit).Scan(&blocked)
	return blocked, err
}

func advisoryKey(hash []byte) int64 { return int64(binary.BigEndian.Uint64(hash[:8])) }

func (s *Service) ready() error {
	if s == nil || s.pool == nil || s.passwords == nil || len(s.secret) < 32 || s.ttl <= 0 {
		return ErrValidation
	}
	return nil
}

func (s *Service) databaseNow(ctx context.Context) (time.Time, error) {
	var now time.Time
	if err := s.pool.QueryRow(ctx, "select CURRENT_TIMESTAMP").Scan(&now); err != nil {
		return time.Time{}, fmt.Errorf("read authentication time: %w", err)
	}
	return now.UTC(), nil
}

// maybeCleanup bounds persistent authentication garbage without requiring a
// background goroutine or an application lifecycle hook. Each sweep deletes
// at most 100 rows from each table and runs once per 64 credential requests
// (including the first request after process start).
func (s *Service) maybeCleanup(ctx context.Context) error {
	if (s.cleanupCounter.Add(1)-1)%64 != 0 {
		return nil
	}
	if _, err := s.pool.Exec(ctx, `
		with stale as (
			select id from pgbase_meta.user_sessions
			where expires_at <= CURRENT_TIMESTAMP
			   or revoked_at <= CURRENT_TIMESTAMP - interval '15 minutes'
			order by expires_at
			limit 100 for update skip locked
		)
		delete from pgbase_meta.user_sessions as sessions
		using stale where sessions.id = stale.id`); err != nil {
		return fmt.Errorf("delete stale user sessions: %w", err)
	}
	if _, err := s.pool.Exec(ctx, `
		with stale as (
			select subject_hash from pgbase_meta.user_auth_rate_limits
			where updated_at <= CURRENT_TIMESTAMP - interval '30 minutes'
			order by updated_at
			limit 100 for update skip locked
		)
		delete from pgbase_meta.user_auth_rate_limits as limits
		using stale where limits.subject_hash = stale.subject_hash`); err != nil {
		return fmt.Errorf("delete stale user rate limits: %w", err)
	}
	return nil
}
func admit(ch chan struct{}) (func(), error) {
	select {
	case ch <- struct{}{}:
		return func() { <-ch }, nil
	default:
		return nil, ErrRateLimited
	}
}
func (s *Service) digest(domain, value string) []byte {
	m := hmac.New(sha256.New, s.secret)
	m.Write([]byte(domain))
	m.Write([]byte(value))
	return m.Sum(nil)
}
func (s *Service) tokenHash(token string) []byte { return s.digest("user-session\x00", token) }
func (s *Service) subjectHash(op, axis, value string) []byte {
	return s.digest("user-rate-limit\x00"+op+"\x00"+axis+"\x00", value)
}
func normalizeEmail(email string) (string, error) {
	if len(email) > 254 || !utf8.ValidString(email) {
		return "", ErrValidation
	}
	v := strings.ToLower(strings.TrimSpace(email))
	if v == "" {
		return "", ErrValidation
	}
	for i := range len(v) {
		if v[i] >= utf8.RuneSelf {
			return "", ErrValidation
		}
	}
	a, e := mail.ParseAddress(v)
	if e != nil || a.Name != "" || a.Address != v {
		return "", ErrValidation
	}
	return v, nil
}
func validatePassword(v string) error {
	if !utf8.ValidString(v) || len(v) < minimumPasswordBytes || len(v) > maximumPasswordBytes {
		return ErrValidation
	}
	return nil
}
func validateClientAddress(v string) error {
	if v == "" || len(v) > 256 || !utf8.ValidString(v) {
		return ErrValidation
	}
	return nil
}
func validateToken(v string) error {
	if v == "" || len(v) > maximumTokenBytes {
		return ErrValidation
	}
	decoded, e := base64.RawURLEncoding.Strict().DecodeString(v)
	if e != nil || len(decoded) != 32 {
		return ErrValidation
	}
	return nil
}

// Passwords intentionally matches adminauth.Passwords so its Argon2id
// implementation can be injected. Translate that implementation's public
// mismatch sentinel while preserving unexpected verifier failures.
func isPasswordMismatch(err error) bool {
	return errors.Is(err, ErrInvalidCredentials) || errors.Is(err, adminauth.ErrInvalidCredentials)
}
