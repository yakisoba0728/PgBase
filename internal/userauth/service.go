package userauth

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"net/mail"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/yakisoba0728/PgBase/internal/adminauth"
	"github.com/yakisoba0728/PgBase/internal/database"
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
	return s.RegisterInCollection(ctx, DefaultCollectionID, email, password, clientAddress)
}

func (s *Service) RegisterInCollection(ctx context.Context, collectionID uuid.UUID, email, password, clientAddress string) (Session, error) {
	return s.RegisterInCollectionWithFields(ctx, collectionID, email, password, clientAddress, nil)
}

func (s *Service) RegisterWithFields(ctx context.Context, email, password, clientAddress string, fields map[string]json.RawMessage) (Session, error) {
	return s.RegisterInCollectionWithFields(ctx, DefaultCollectionID, email, password, clientAddress, fields)
}

func (s *Service) RegisterInCollectionWithFields(ctx context.Context, collectionID uuid.UUID, email, password, clientAddress string, fields map[string]json.RawMessage) (Session, error) {
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
	policy, err := s.authPolicy(ctx, collectionID)
	if err != nil || !passwordMatchesPolicy(password, policy) {
		return Session{}, ErrValidation
	}
	if err := validateClientAddress(clientAddress); err != nil {
		return Session{}, err
	}
	now, err := s.databaseNow(ctx)
	if err != nil {
		return Session{}, err
	}
	ratePolicy := s.currentRatePolicy()
	var registrationEnabled bool
	if err := s.pool.QueryRow(ctx, `select registration_enabled from pgbase_meta.auth_collections where id=$1`, collectionID).Scan(&registrationEnabled); errors.Is(err, pgx.ErrNoRows) {
		return Session{}, ErrNotFound
	} else if err != nil {
		return Session{}, err
	}
	if !registrationEnabled {
		return Session{}, ErrInvalidCredentials
	}
	if collectionID == DefaultCollectionID && !s.defaultRegistration {
		return Session{}, ErrInvalidCredentials
	}
	bucket := s.subjectHash("register", "network", clientAddress)
	if err := s.consumeRateLimit(ctx, bucket, ratePolicy.RegistrationNetworkMaxFailures, now, ratePolicy.Window, ratePolicy.Block); err != nil {
		return Session{}, err
	}
	releaseHash, err := admit(s.passwordSlots)
	if err != nil {
		return Session{}, err
	}
	passwordHash, err := s.passwords.Hash(password)
	releaseHash()
	if err != nil {
		return Session{}, err
	}
	user := User{ID: uuid.New(), AuthCollectionID: collectionID, Email: normalized, CreatedAt: now}
	issued, err := s.createUserAndSession(ctx, user, passwordHash, now, fields, password)
	if database.Classify(err) == database.KindConflict {
		return Session{}, ErrConflict
	}
	if err == nil {
		if added, _ := s.recordAuthOrigin(ctx, user.ID, clientAddress, now); added {
			s.sendAuthAlert(ctx, user, "New sign-in origin: "+clientAddress)
		}
	}
	return issued, err
}

func (s *Service) Login(ctx context.Context, email, password, clientAddress string) (Session, error) {
	return s.LoginInCollection(ctx, DefaultCollectionID, email, password, clientAddress)
}

func (s *Service) LoginInCollection(ctx context.Context, collectionID uuid.UUID, email, password, clientAddress string) (Session, error) {
	if err := s.ready(); err != nil {
		return Session{}, err
	}
	policy, policyErr := s.authPolicy(ctx, collectionID)
	if policyErr != nil || !passwordMatchesPolicy(password, policy) {
		return Session{}, ErrInvalidCredentials
	}
	release, err := admit(s.credentialSlots)
	if err != nil {
		return Session{}, err
	}
	defer release()
	identity := strings.TrimSpace(email)
	normalizedEmail, emailErr := normalizeEmail(identity)
	accountIdentity := identity
	if emailErr == nil && slices.Contains(policy.IdentityFields, "email") {
		accountIdentity = normalizedEmail
	}
	if len(policy.IdentityFields) != 1 || policy.IdentityFields[0] != "email" {
		emailErr = nil
	}
	if err := validateClientAddress(clientAddress); err != nil {
		return Session{}, err
	}
	passwordErr := validatePassword(password)
	now, err := s.databaseNow(ctx)
	if err != nil {
		return Session{}, err
	}
	ratePolicy := s.currentRatePolicy()
	accountSubject := collectionID.String() + "\x00" + accountIdentity
	if emailErr != nil {
		// All malformed emails share one fixed-size bucket. Valid unknown emails
		// use their normalized form exactly like existing accounts, so account
		// admission does not disclose whether a user exists.
		accountSubject = collectionID.String() + "\x00<invalid>"
	}
	buckets := []rateLimitBucket{
		{hash: s.subjectHash("login", "account", accountSubject), limit: ratePolicy.AccountMaxFailures, window: ratePolicy.Window, block: ratePolicy.Block},
		{hash: s.subjectHash("login", "network", clientAddress), limit: ratePolicy.NetworkMaxFailures, window: ratePolicy.Window, block: ratePolicy.Block},
	}
	// The attempt is charged before the password is checked and refunded
	// unless it fails, so concurrent wrong passwords cannot overshoot either
	// budget.
	reservation, err := s.reserveFailures(ctx, buckets, now)
	if err != nil {
		return Session{}, err
	}
	defer reservation.refund(ctx)
	var user User
	var hash string
	lookupErr := error(ErrInvalidCredentials)
	if emailErr == nil && passwordErr == nil && identity != "" {
		user, hash, lookupErr = userByIdentityDB(ctx, s.pool, collectionID, policy.IdentityFields, identity, normalizedEmail)
		if lookupErr != nil && !errors.Is(lookupErr, ErrInvalidCredentials) {
			return Session{}, lookupErr
		}
	}
	releaseHash, err := admit(s.passwordSlots)
	if err != nil {
		return Session{}, err
	}
	shapeValid := emailErr == nil && passwordErr == nil && lookupErr == nil && user.passwordEnabled
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
		if err := reservation.fail(); err != nil {
			return Session{}, err
		}
		return Session{}, ErrInvalidCredentials
	}
	issued, err := s.issueCheckedSession(ctx, user, hash, now, password)
	if errors.Is(err, ErrInvalidCredentials) {
		_ = reservation.fail()
	}
	if err == nil {
		if added, _ := s.recordAuthOrigin(ctx, user.ID, clientAddress, now); added {
			s.sendAuthAlert(ctx, user, "New sign-in origin: "+clientAddress)
		}
	}
	return issued, err
}

// userByIdentityDB permits email plus declared text profile fields.  Field names
// are read from validated policy rather than interpolated into SQL, keeping the
// JSON lookup parameterized and preventing profile-field SQL injection.
func userByIdentityDB(ctx context.Context, db database.DBTX, collectionID uuid.UUID, fields []string, identity, normalizedEmail string) (User, string, error) {
	for _, field := range fields {
		if field == "email" {
			if normalizedEmail == "" {
				continue
			}
			u, hash, err := userByEmailDB(ctx, db, collectionID, normalizedEmail, false)
			if err == nil {
				return u, hash, nil
			}
			if !errors.Is(err, ErrInvalidCredentials) {
				return User{}, "", err
			}
			continue
		}
		var u User
		var hash string
		err := db.QueryRow(ctx, `select u.id,u.auth_collection_id,u.email,u.email_visibility,u.verified,u.created_at,u.password_hash,u.password_enabled from pgbase_meta.users u join pgbase_meta.user_auth_profiles p on p.user_id=u.id where u.auth_collection_id=$1 and u.active and p.attributes ->> $2=$3`, collectionID, field, identity).Scan(&u.ID, &u.AuthCollectionID, &u.Email, &u.EmailVisibility, &u.Verified, &u.CreatedAt, &hash, &u.passwordEnabled)
		if err == nil {
			u.CreatedAt = u.CreatedAt.UTC()
			return u, hash, nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return User{}, "", fmt.Errorf("read user identity: %w", err)
		}
	}
	return User{}, "", ErrInvalidCredentials
}

func passwordMatchesPolicy(password string, policy AuthPolicy) bool {
	if !policy.PasswordEnabled || len(password) < policy.MinimumPasswordBytes || len(password) > policy.MaximumPasswordBytes {
		return false
	}
	if policy.PasswordPattern == "" {
		return true
	}
	re, err := regexp.Compile(policy.PasswordPattern)
	return err == nil && re.MatchString(password)
}

func (s *Service) AuthenticateForCollection(ctx context.Context, collectionID uuid.UUID, rawToken string) (Principal, error) {
	p, err := s.Authenticate(ctx, rawToken)
	if err != nil {
		return Principal{}, err
	}
	if collectionID == uuid.Nil || p.User.AuthCollectionID != collectionID {
		return Principal{}, ErrInvalidCredentials
	}
	return p, nil
}

func (s *Service) Authenticate(ctx context.Context, rawToken string) (Principal, error) {
	if err := s.ready(); err != nil {
		return Principal{}, err
	}
	release, err := admit(s.sessionSlots)
	if err != nil {
		return Principal{}, err
	}
	defer release()
	hash, err := s.sessionHash(ctx, s.pool, rawToken)
	if errors.Is(err, ErrValidation) {
		return Principal{}, ErrInvalidCredentials
	}
	if err != nil {
		return Principal{}, err
	}
	var p Principal
	var publicID *string
	// The auth identity is joined on its primary key (collection_kind,
	// collection_id, internal_id) so session authentication costs one round
	// trip; projectUserFromJoin keeps the former PublicAuth error behaviour.
	err = s.pool.QueryRow(ctx, `select u.id,u.auth_collection_id,u.email,u.email_visibility,u.verified,u.created_at,s.expires_at,s.session_kind='impersonation',coalesce(s.impersonating_admin_id,'00000000-0000-0000-0000-000000000000'),ri.external_id from pgbase_meta.user_sessions s join pgbase_meta.users u on u.id=s.user_id left join pgbase_meta.admin_sessions ads on ads.id=s.impersonating_admin_session_id left join pgbase_meta.admins a on a.id=s.impersonating_admin_id left join pgbase_meta.record_identities ri on ri.collection_kind='auth' and ri.collection_id=u.auth_collection_id and ri.internal_id=u.id where s.token_hash=$1 and s.revoked_at is null and s.expires_at>CURRENT_TIMESTAMP and u.active and (s.session_kind='user' or (ads.revoked_at is null and ads.expires_at>CURRENT_TIMESTAMP and a.active))`, hash).Scan(&p.User.ID, &p.User.AuthCollectionID, &p.User.Email, &p.User.EmailVisibility, &p.User.Verified, &p.User.CreatedAt, &p.ExpiresAt, &p.Impersonated, &p.ImpersonatingAdministratorID, &publicID)
	if errors.Is(err, pgx.ErrNoRows) {
		return Principal{}, ErrInvalidCredentials
	}
	if err != nil {
		return Principal{}, fmt.Errorf("authenticate user session: %w", err)
	}
	p.User.CreatedAt = p.User.CreatedAt.UTC()
	p.ExpiresAt = p.ExpiresAt.UTC()
	if err := projectUserFromJoin(&p.User, publicID); err != nil {
		return Principal{}, err
	}
	return p, nil
}

func (s *Service) Logout(ctx context.Context, rawToken string) error {
	if err := s.ready(); err != nil {
		return err
	}
	release, err := admit(s.sessionSlots)
	if err != nil {
		return err
	}
	defer release()
	hash, err := s.sessionHash(ctx, s.pool, rawToken)
	if errors.Is(err, ErrInvalidCredentials) {
		return ErrValidation
	}
	if err != nil {
		return err
	}
	_, err = s.pool.Exec(ctx, `update pgbase_meta.user_sessions set revoked_at=coalesce(revoked_at,CURRENT_TIMESTAMP) where token_hash=$1`, hash)
	if err != nil {
		return fmt.Errorf("revoke user session: %w", err)
	}
	return nil
}

func (s *Service) createUserAndSession(ctx context.Context, user User, passwordHash string, now time.Time, fields map[string]json.RawMessage, password string) (Session, error) {
	var issued Session
	err := s.pool.InTx(ctx, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if user.AuthCollectionID == uuid.Nil {
			user.AuthCollectionID = DefaultCollectionID
		}
		currentPolicy, err := lockedAuthPolicyTx(ctx, tx, user.AuthCollectionID)
		if err != nil {
			return err
		}
		if !passwordMatchesPolicy(password, currentPolicy) {
			return ErrValidation
		}

		policy, err := authRecordIDPolicy(ctx, tx, user.AuthCollectionID)
		if err != nil {
			return err
		}
		if _, err := s.recordIdentities.ReserveAuth(ctx, tx, user.AuthCollectionID, user.ID, policy, nil); err != nil {
			return fmt.Errorf("create user identity: %w", err)
		}
		_, err = tx.Exec(ctx, `insert into pgbase_meta.users(id,auth_collection_id,email,password_hash,active,created_at,updated_at) values($1,$2,$3,$4,true,$5,$5)`, user.ID, user.AuthCollectionID, user.Email, passwordHash, now)
		if err != nil {
			return fmt.Errorf("create user: %w", err)
		}
		if err := s.completePublicRegistration(ctx, tx, user, fields); err != nil {
			return err
		}
		var e error
		issued, e = s.insertSession(ctx, tx, user, now)
		return e
	})
	return issued, err
}

func (s *Service) issueCheckedSession(ctx context.Context, user User, expectedHash string, now time.Time, password string) (Session, error) {
	var issued Session
	var opErr error
	err := s.pool.InTx(ctx, pgx.TxOptions{}, func(tx pgx.Tx) error {
		collectionID := user.AuthCollectionID
		if collectionID == uuid.Nil {
			collectionID = DefaultCollectionID
		}
		policy, err := lockedAuthPolicyTx(ctx, tx, collectionID)
		if err != nil {
			return err
		}
		if !passwordMatchesPolicy(password, policy) {
			return ErrInvalidCredentials
		}

		current, hash, err := userByEmailDB(ctx, tx, collectionID, user.Email, true)
		if err != nil {
			return err
		}
		if current.ID != user.ID || !current.passwordEnabled || !hmac.Equal([]byte(hash), []byte(expectedHash)) {
			opErr = ErrInvalidCredentials
			return nil
		}
		mfaActive, err := s.mfaRequiredTx(ctx, tx, current)
		if err != nil {
			return err
		}
		if mfaActive {
			challenge, err := s.insertMFAChallenge(ctx, tx, current.ID, now)
			if err != nil {
				return err
			}
			opErr = challenge
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
	if allowed, err := s.authorizeSessionTx(ctx, db, user); err != nil {
		return Session{}, fmt.Errorf("authorize session: %w", err)
	} else if !allowed {
		return Session{}, fmt.Errorf("auth rule denied: %w", ErrInvalidCredentials)
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return Session{}, fmt.Errorf("generate user session token: %w", err)
	}
	token := base64.RawURLEncoding.EncodeToString(raw)
	tokenHash := s.tokenHash(token)
	ttl := s.ttl
	var configured int
	var version string
	var cipher []byte
	if err := db.QueryRow(ctx, `select ttl_seconds,key_version,key_ciphertext from pgbase_meta.auth_token_policies where auth_collection_id=$1 and purpose='session' for share`, user.AuthCollectionID).Scan(&configured, &version, &cipher); err == nil {
		ttl = time.Duration(configured) * time.Second
		key, err := s.decrypt("auth-token-policy:session", cipher)
		if err != nil {
			return Session{}, err
		}
		token = user.AuthCollectionID.String() + "." + version + "." + token
		tokenHash = keyedDigest(key, "user-session\x00", token)
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return Session{}, err
	}
	expires := now.Add(ttl)
	_, err := db.Exec(ctx, `insert into pgbase_meta.user_sessions(id,user_id,token_hash,expires_at,revoked_at,created_at) values($1,$2,$3,$4,null,$5)`, uuid.New(), user.ID, tokenHash, expires, now)
	if err != nil {
		return Session{}, fmt.Errorf("create user session: %w", err)
	}
	if err := s.projectUser(ctx, db, &user); err != nil {
		return Session{}, err
	}
	return Session{User: user, RawToken: token, ExpiresAt: expires}, nil
}

const sessionUserColumns = "u.id,u.auth_collection_id,u.email,u.email_visibility,u.verified,u.created_at"

func scanSessionUser(row pgx.Row, user *User, trailing ...any) error {
	return row.Scan(append([]any{&user.ID, &user.AuthCollectionID, &user.Email, &user.EmailVisibility, &user.Verified, &user.CreatedAt}, trailing...)...)
}

func userByEmailDB(ctx context.Context, db database.DBTX, collectionID uuid.UUID, email string, lock bool) (User, string, error) {
	q := `select id,auth_collection_id,email,email_visibility,verified,created_at,password_hash,password_enabled from pgbase_meta.users where auth_collection_id=$1 and email=$2 and active`
	if lock {
		q += " for update"
	}
	var u User
	var hash string
	err := db.QueryRow(ctx, q, collectionID, email).Scan(&u.ID, &u.AuthCollectionID, &u.Email, &u.EmailVisibility, &u.Verified, &u.CreatedAt, &hash, &u.passwordEnabled)
	if errors.Is(err, pgx.ErrNoRows) {
		return User{}, "", ErrInvalidCredentials
	}
	if err != nil {
		return User{}, "", fmt.Errorf("read user: %w", err)
	}
	u.CreatedAt = u.CreatedAt.UTC()
	return u, hash, nil
}

func (s *Service) consumeRateLimit(ctx context.Context, hash []byte, limit int, now time.Time, durations ...time.Duration) error {
	if err := s.ensureNotBlocked(ctx, hash, now); err != nil {
		return err
	}
	return s.recordFailure(ctx, hash, limit, now, durations...)
}

type rateLimitBucket struct {
	hash          []byte
	limit         int
	window, block time.Duration
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
// history; signals decay when the configured window rolls over.
func (s *Service) recordFailures(ctx context.Context, buckets []rateLimitBucket, now time.Time) error {
	policy := s.currentRatePolicy()
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
			window, block := bucket.window, bucket.block
			if window <= 0 {
				window = policy.Window
			}
			if block <= 0 {
				block = policy.Block
			}
			bucketBlocked, err := recordFailureInTx(ctx, tx, bucket.hash, bucket.limit, now, window, block)
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

// failureReservation is one credential attempt's charge against its failure
// buckets, taken before the credential is checked. Checking a budget first and
// recording the failure afterwards lets every concurrent attempt that passed
// the check be evaluated, so a burst of wrong guesses overshoots the limit;
// charging first means no more than limit attempts can be in flight or failed
// before the bucket blocks. The attempt then either keeps the charge (it
// failed) or refunds it, which also lifts a block that only the charge itself
// set.
type failureReservation struct {
	service *Service
	now     time.Time
	charges []failureCharge
	blocked bool
	settled bool
}

type failureCharge struct {
	hash            []byte
	windowStartedAt time.Time
	// blockedUntil is the block this charge set, nil when it set none;
	// previousBlock is the expired block it replaced.
	blockedUntil, previousBlock *time.Time
}

// reserveFailures charges one failure to every bucket in one transaction, or
// returns ErrRateLimited without charging any when a bucket is blocked. The
// charge that reaches a bucket's limit blocks it at once, so concurrent
// attempts are refused while it is in flight. Rows are locked in a stable
// order, so attempts sharing buckets cannot deadlock.
func (s *Service) reserveFailures(ctx context.Context, buckets []rateLimitBucket, now time.Time) (*failureReservation, error) {
	policy := s.currentRatePolicy()
	ordered := append([]rateLimitBucket(nil), buckets...)
	sort.Slice(ordered, func(i, j int) bool { return advisoryKey(ordered[i].hash) < advisoryKey(ordered[j].hash) })
	reservation := &failureReservation{service: s, now: now}
	err := s.pool.InTx(ctx, pgx.TxOptions{}, func(tx pgx.Tx) error {
		reservation.charges, reservation.blocked = nil, false
		for _, bucket := range ordered {
			window, block := bucket.window, bucket.block
			if window <= 0 {
				window = policy.Window
			}
			if block <= 0 {
				block = policy.Block
			}
			if _, err := tx.Exec(ctx, `insert into pgbase_meta.user_auth_rate_limits(subject_hash,window_started_at,failures,blocked_until,updated_at) values($1,$2,0,null,$2) on conflict(subject_hash) do nothing`, bucket.hash, now); err != nil {
				return err
			}
			var started time.Time
			var failures int
			var blockedUntil *time.Time
			if err := tx.QueryRow(ctx, `select window_started_at,failures,blocked_until from pgbase_meta.user_auth_rate_limits where subject_hash=$1 for update`, bucket.hash).Scan(&started, &failures, &blockedUntil); err != nil {
				return err
			}
			if blockedUntil != nil && blockedUntil.After(now) {
				return ErrRateLimited
			}
			if !started.After(now.Add(-window)) {
				started, failures = now, 0
			}
			failures++
			charge := failureCharge{hash: bucket.hash, windowStartedAt: started.Truncate(time.Microsecond), previousBlock: blockedUntil}
			next := blockedUntil
			if failures >= bucket.limit {
				until := now.Add(block).Truncate(time.Microsecond)
				next, charge.blockedUntil = &until, &until
				reservation.blocked = true
			}
			if _, err := tx.Exec(ctx, `update pgbase_meta.user_auth_rate_limits set window_started_at=$2,failures=$3,blocked_until=$4,updated_at=$5 where subject_hash=$1`, bucket.hash, charge.windowStartedAt, failures, next, now); err != nil {
				return err
			}
			reservation.charges = append(reservation.charges, charge)
		}
		return nil
	})
	if errors.Is(err, ErrRateLimited) {
		return nil, ErrRateLimited
	}
	if err != nil {
		return nil, fmt.Errorf("reserve user rate limits: %w", err)
	}
	return reservation, nil
}

// fail keeps the charge as a recorded failure. Like recordFailures it returns
// ErrRateLimited when that failure reached a bucket's limit.
func (r *failureReservation) fail() error {
	if r == nil || r.settled {
		return nil
	}
	r.settled = true
	if r.blocked {
		return ErrRateLimited
	}
	return nil
}

// refund withdraws the charge of an attempt that did not fail: a success, a
// step change such as an MFA challenge, or an internal error. It is a no-op
// after fail or a previous refund, so callers can defer it. A refund that
// cannot be written leaves the charge counted, which only errs toward the
// limit.
func (r *failureReservation) refund(ctx context.Context) {
	if r == nil || r.settled {
		return
	}
	r.settled = true
	refundCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	_ = r.service.pool.InTx(refundCtx, pgx.TxOptions{}, func(tx pgx.Tx) error {
		for _, charge := range r.charges {
			// A window that rolled over since the charge already forgot it.
			if _, err := tx.Exec(refundCtx, `update pgbase_meta.user_auth_rate_limits set failures=greatest(failures-1,0),blocked_until=case when $3::timestamptz is not null and blocked_until=$3 then $4 else blocked_until end,updated_at=$5 where subject_hash=$1 and window_started_at=$2`, charge.hash, charge.windowStartedAt, charge.blockedUntil, charge.previousBlock, r.now); err != nil {
				return err
			}
		}
		return nil
	})
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
func (s *Service) recordFailure(ctx context.Context, hash []byte, limit int, now time.Time, durations ...time.Duration) error {
	policy := s.currentRatePolicy()
	window, block := policy.Window, policy.Block
	if len(durations) == 2 {
		if durations[0] > 0 {
			window = durations[0]
		}
		if durations[1] > 0 {
			block = durations[1]
		}
	}
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
		blocked, err = recordFailureInTx(ctx, tx, hash, limit, now, window, block)
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

func recordFailureInTx(ctx context.Context, tx pgx.Tx, hash []byte, limit int, now time.Time, durations ...time.Duration) (bool, error) {
	window, block := 15*time.Minute, 15*time.Minute
	if len(durations) == 2 {
		if durations[0] > 0 {
			window = durations[0]
		}
		if durations[1] > 0 {
			block = durations[1]
		}
	}
	var blocked bool
	err := tx.QueryRow(ctx, `insert into pgbase_meta.user_auth_rate_limits as r(subject_hash,window_started_at,failures,blocked_until,updated_at) values($1,$2,1,null,$2) on conflict(subject_hash) do update set window_started_at=case when r.window_started_at<=$2-make_interval(secs=>$4) then $2 else r.window_started_at end,failures=case when r.window_started_at<=$2-make_interval(secs=>$4) then 1 else r.failures+1 end,blocked_until=case when (case when r.window_started_at<=$2-make_interval(secs=>$4) then 1 else r.failures+1 end)>=$3 then $2+make_interval(secs=>$5) else r.blocked_until end,updated_at=$2 returning coalesce(blocked_until>$2,false)`, hash, now, limit, int64(window.Seconds()), int64(block.Seconds())).Scan(&blocked)
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
