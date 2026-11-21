package userauth

import (
	"context"
	"crypto/hmac"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/yakisoba0728/PgBase/internal/database"
)

const (
	emailActionDeliveryLimit  = 5
	emailActionDeliveryWindow = 15 * time.Minute
	emailActionResendCooldown = time.Minute
)

// emailActionDeliveryBuckets applies an aggregate actor budget even when the
// recipient or purpose changes. Recipient budgets and cooldowns are shared
// across users but scoped to (purpose, auth collection, recipient), so an
// email_change request or an account in another auth collection cannot spend
// the verification capacity of the address owner. Accounts in the same
// collection can still fill an address's email_change capacity by requesting
// changes to it; that bound exists to protect the mailbox owner. One address
// can therefore receive up to emailActionDeliveryLimit messages per window for
// each purpose and collection. These reservations are separate from
// password-failure history and optional HTTP rate limits.
func (s *Service) emailActionDeliveryBuckets(userID, collectionID uuid.UUID, purpose, recipient string) []rateLimitBucket {
	recipientKey := purpose + "\x00" + collectionID.String() + "\x00" + recipient
	return []rateLimitBucket{
		{hash: s.subjectHash("email-action-delivery", "actor", userID.String()), limit: emailActionDeliveryLimit, window: emailActionDeliveryWindow},
		{hash: s.subjectHash("email-action-delivery", "recipient", recipientKey), limit: emailActionDeliveryLimit, window: emailActionDeliveryWindow},
		{hash: s.subjectHash("email-action-cooldown", "actor", purpose+"\x00"+userID.String()), limit: 1, window: emailActionResendCooldown},
		{hash: s.subjectHash("email-action-cooldown", "recipient", recipientKey), limit: 1, window: emailActionResendCooldown},
	}
}

// reserveEmailActionDelivery runs in the same transaction as action creation.
// Failure rolls back every reservation; after commit, SMTP errors do not refund
// a reservation because delivery may have succeeded before the transport failed.
func (s *Service) reserveEmailActionDelivery(ctx context.Context, tx pgx.Tx, userID, collectionID uuid.UUID, purpose, recipient string) error {
	buckets := s.emailActionDeliveryBuckets(userID, collectionID, purpose, recipient)
	sort.Slice(buckets, func(i, j int) bool { return advisoryKey(buckets[i].hash) < advisoryKey(buckets[j].hash) })
	for _, bucket := range buckets {
		var acquired bool
		if err := tx.QueryRow(ctx, `select pg_try_advisory_xact_lock($1)`, advisoryKey(bucket.hash)).Scan(&acquired); err != nil {
			return err
		}
		if !acquired {
			return ErrRateLimited
		}
	}
	for _, bucket := range buckets {
		tag, err := tx.Exec(ctx, `
			insert into pgbase_meta.user_auth_rate_limits as limits
				(subject_hash,window_started_at,failures,blocked_until,updated_at)
			values ($1,CURRENT_TIMESTAMP,1,null,CURRENT_TIMESTAMP)
			on conflict(subject_hash) do update set
				window_started_at = case
					when limits.window_started_at <= CURRENT_TIMESTAMP - make_interval(secs=>$3) then CURRENT_TIMESTAMP
					else limits.window_started_at end,
				failures = case
					when limits.window_started_at <= CURRENT_TIMESTAMP - make_interval(secs=>$3) then 1
					else limits.failures + 1 end,
				updated_at = CURRENT_TIMESTAMP
			where limits.window_started_at <= CURRENT_TIMESTAMP - make_interval(secs=>$3) or limits.failures < $2`, bucket.hash, bucket.limit, int64(bucket.window.Seconds()))
		if err != nil {
			return err
		}
		if tag.RowsAffected() != 1 {
			return ErrRateLimited
		}
	}
	return nil
}

func (s *Service) preflightPasswordReset(ctx context.Context, rawToken, password string) error {
	return s.pool.InTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly}, func(tx pgx.Tx) error {
		action, err := s.readEmailAction(ctx, tx, "password_reset", rawToken, false)
		if err != nil {
			return err
		}
		policy, err := authPolicyTx(ctx, tx, action.collectionID)
		if err != nil {
			return err
		}
		if !passwordMatchesPolicy(password, policy) {
			return ErrValidation
		}
		return nil
	})
}

// readEmailAction shares eligibility and credential binding between the cheap
// preflight and final consumption. Only the final transaction locks policy,
// account, and action rows; it must recheck after password work completes.
func (s *Service) readEmailAction(ctx context.Context, db database.DBTX, purpose, rawToken string, lock bool) (emailAction, error) {
	hash := s.actionTokenHash(purpose, rawToken)
	if version, _, ok := splitPolicyToken(rawToken); ok {
		query := `select key_ciphertext from pgbase_meta.auth_token_policies where key_version=$1 and purpose=$2`
		if lock {
			query += " for share"
		}
		var cipher []byte
		if err := db.QueryRow(ctx, query, version, purpose).Scan(&cipher); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return emailAction{}, ErrInvalidCredentials
			}
			return emailAction{}, fmt.Errorf("read user email action policy: %w", err)
		}
		key, err := s.decrypt("auth-token-policy:"+purpose, cipher)
		if err != nil {
			return emailAction{}, ErrInvalidCredentials
		}
		hash = keyedDigest(key, "user-email-action\x00"+purpose+"\x00", rawToken)
	}
	if lock {
		// Use the same account-before-action lock order as resend replacement.
		var userID uuid.UUID
		err := db.QueryRow(ctx, `select u.id from pgbase_meta.users u join pgbase_meta.user_email_actions a on a.user_id=u.id where a.token_hash=$1 and a.purpose=$2 and a.consumed_at is null and a.expires_at>CURRENT_TIMESTAMP and u.active for update of u`, hash, purpose).Scan(&userID)
		if errors.Is(err, pgx.ErrNoRows) {
			return emailAction{}, ErrInvalidCredentials
		}
		if err != nil {
			return emailAction{}, fmt.Errorf("lock user email action account: %w", err)
		}
	}
	query := `select a.id,a.user_id,u.auth_collection_id,a.purpose,a.payload_email,a.credential_hash,a.expires_at,u.password_hash,u.email from pgbase_meta.user_email_actions a join pgbase_meta.users u on u.id=a.user_id where a.token_hash=$1 and a.purpose=$2 and a.consumed_at is null and a.expires_at>CURRENT_TIMESTAMP and u.active`
	if lock {
		query += " for update of a"
	}
	var action emailAction
	var currentHash, currentEmail string
	err := db.QueryRow(ctx, query, hash, purpose).Scan(&action.id, &action.userID, &action.collectionID, &action.purpose, &action.payloadEmail, &action.credentialHash, &action.expiresAt, &currentHash, &currentEmail)
	if errors.Is(err, pgx.ErrNoRows) {
		return emailAction{}, ErrInvalidCredentials
	}
	if err != nil {
		return emailAction{}, fmt.Errorf("read user email action: %w", err)
	}
	if !hmac.Equal(action.credentialHash, s.emailActionCredentialHash(purpose, currentHash, currentEmail)) {
		return emailAction{}, ErrInvalidCredentials
	}
	return action, nil
}
