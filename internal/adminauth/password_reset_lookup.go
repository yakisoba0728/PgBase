package adminauth

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// PasswordResetAdministratorID checks eligibility without consuming a token or
// holding account locks across password hashing. Final redemption still runs
// under the administrator lifecycle fence and rechecks one-time consumption.
func (s *Store) PasswordResetAdministratorID(ctx context.Context, tokenHash []byte, now time.Time) (uuid.UUID, error) {
	var administratorID uuid.UUID
	err := s.db.QueryRow(ctx, `select resets.admin_id
		from pgbase_meta.admin_password_resets resets
		join pgbase_meta.admins admins on admins.id=resets.admin_id
		where resets.token_hash=$1 and resets.consumed_at is null
		  and resets.expires_at>$2 and admins.active`, tokenHash, now).Scan(&administratorID)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, ErrInvalidCredentials
	}
	if err != nil {
		return uuid.Nil, fmt.Errorf("read administrator password reset: %w", err)
	}
	return administratorID, nil
}
