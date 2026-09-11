package userauth

import (
	"context"
	"errors"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func (s *Service) ResolveAuthCollection(ctx context.Context, nameOrID string) (AuthCollection, error) {
	value := strings.TrimSpace(nameOrID)
	if id, err := uuid.Parse(value); err == nil {
		var collection AuthCollection
		err = scanAuthCollection(s.pool.QueryRow(ctx, `select `+authCollectionColumns+` from pgbase_meta.auth_collections where id=$1`, id), &collection)
		if errors.Is(err, pgx.ErrNoRows) {
			return AuthCollection{}, ErrNotFound
		}
		return collection, err
	}
	return s.GetAuthCollection(ctx, value)
}

// IssueAuthResponseSession creates a session for an identity already verified
// by a trusted application extension. It preserves the same active, authRule,
// MFA and session-policy checks as the built-in credential flows.
func (s *Service) IssueAuthResponseSession(ctx context.Context, collectionID, userID uuid.UUID, origin string) (Session, error) {
	if err := s.ready(); err != nil || collectionID == uuid.Nil || userID == uuid.Nil || validateClientAddress(origin) != nil {
		return Session{}, ErrValidation
	}
	now, err := s.databaseNow(ctx)
	if err != nil {
		return Session{}, err
	}
	var issued Session
	var authErr error
	err = s.pool.InTx(ctx, pgx.TxOptions{}, func(tx pgx.Tx) error {
		var user User
		err := tx.QueryRow(ctx, `select id,auth_collection_id,email,verified,created_at from pgbase_meta.users where id=$1 and auth_collection_id=$2 and active for update`, userID, collectionID).Scan(&user.ID, &user.AuthCollectionID, &user.Email, &user.Verified, &user.CreatedAt)
		if errors.Is(err, pgx.ErrNoRows) {
			authErr = ErrInvalidCredentials
			return nil
		}
		if err != nil {
			return err
		}
		if required, err := s.mfaRequiredTx(ctx, tx, user); err != nil {
			return err
		} else if required {
			challenge, err := s.insertMFAChallenge(ctx, tx, user.ID, now)
			if err != nil {
				return err
			}
			authErr = challenge
			return nil
		}
		issued, err = s.insertSession(ctx, tx, user, now)
		return err
	})
	if err != nil {
		return Session{}, err
	}
	if authErr != nil {
		return Session{}, authErr
	}
	if added, _ := s.recordAuthOrigin(ctx, issued.User.ID, origin, now); added {
		s.sendAuthAlert(ctx, issued.User, "New sign-in origin: "+origin)
	}
	return issued, nil
}

// GetVisibleAuthRecord is the administrator-safe record representation. It
// strips password fields and every collection field marked hidden.
func (s *Service) GetVisibleAuthRecord(ctx context.Context, collectionID, id uuid.UUID) (AuthRecord, error) {
	fields, _, err := s.authRecordRules(ctx, collectionID)
	if err != nil {
		return AuthRecord{}, err
	}
	record, err := s.GetAuthRecord(ctx, collectionID, id)
	if err != nil {
		return AuthRecord{}, err
	}
	return visibleAuthRecord(record, fields), nil
}
