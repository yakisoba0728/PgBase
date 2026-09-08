package recordaccess

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/webisnoteasy/PgBase/internal/identity"
	"github.com/webisnoteasy/PgBase/internal/serviceauth"
)

// AuthorizeSubject checks the current admission grant under transaction locks.
// Service credentials carry explicit collection/action grants; public requests
// require the collection opt-in gate and are subsequently filtered by rules.
func AuthorizeSubject(ctx context.Context, tx pgx.Tx, collectionID uuid.UUID, subject identity.Subject, action string) error {
	switch subject.Kind {
	case identity.Administrator:
		return nil
	case identity.User:
		return AuthorizeUser(ctx, tx, collectionID, subject.ID)
	case identity.Service:
		if action == "list" || action == "view" {
			action = "read"
		}
		err := serviceauth.Authorize(ctx, tx, subject, collectionID, action)
		if errors.Is(err, serviceauth.ErrInvalidToken) {
			return ErrDenied
		}
		return err
	case identity.Anonymous:
		var enabled bool
		err := tx.QueryRow(ctx, `SELECT enabled FROM pgbase_meta.collection_access WHERE collection_id=$1 FOR SHARE`, collectionID).Scan(&enabled)
		if errors.Is(err, pgx.ErrNoRows) || err == nil && !enabled {
			return ErrDenied
		}
		return err
	}
	return ErrDenied
}
