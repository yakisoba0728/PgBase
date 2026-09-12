// Package authpolicy applies the database effects of authentication-policy
// transitions inside a caller-owned transaction.
package authpolicy

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Policy contains only the values that alter already-issued authentication
// material. Callers remain responsible for validating and storing the full
// policy before applying its transition in the same transaction.
type Policy struct {
	PasswordEnabled      bool
	MinimumPasswordBytes int
	MFAEnabled           bool
	OTPEnabled           bool
}

// ApplyTransition makes a stored policy transition effective for existing
// sessions and authentication material. The caller must hold the policy row
// lock and pass the transaction that stored next.
func ApplyTransition(ctx context.Context, tx pgx.Tx, collectionID uuid.UUID, previous, next Policy) error {
	var err error
	if previous.PasswordEnabled && !next.PasswordEnabled ||
		next.MinimumPasswordBytes > previous.MinimumPasswordBytes ||
		!previous.MFAEnabled && next.MFAEnabled ||
		previous.OTPEnabled && !next.OTPEnabled {
		_, err = tx.Exec(ctx, `update pgbase_meta.user_sessions set revoked_at=coalesce(revoked_at,CURRENT_TIMESTAMP) where user_id in (select id from pgbase_meta.users where auth_collection_id=$1)`, collectionID)
	}
	if err == nil && !next.MFAEnabled {
		_, err = tx.Exec(ctx, `delete from pgbase_meta.user_totp_recovery_codes where user_id in (select id from pgbase_meta.users where auth_collection_id=$1)`, collectionID)
		if err == nil {
			_, err = tx.Exec(ctx, `delete from pgbase_meta.user_totp_factors where user_id in (select id from pgbase_meta.users where auth_collection_id=$1)`, collectionID)
		}
	}
	if err == nil && previous.OTPEnabled && !next.OTPEnabled {
		_, err = tx.Exec(ctx, `update pgbase_meta.user_email_otps set consumed_at=CURRENT_TIMESTAMP where auth_collection_id=$1 and consumed_at is null`, collectionID)
	}
	return err
}
