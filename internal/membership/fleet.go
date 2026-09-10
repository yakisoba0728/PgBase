package membership

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/yakisoba0728/PgBase/internal/database"
)

// ActiveWriterFleet reports the unexpired record-writer leases in the exact
// order and shape a rollout proof has to name them in. It is the read half of
// the operator surface: a transition re-reads this same set under its own
// locks and requires an exact match, so what an operator copies from here is
// what the transition will demand back.
//
// An empty fleet is reported as an empty slice rather than as the failure the
// transitions raise for it. Nothing has been decided at a read, and "no writer
// holds a lease" is the answer an operator needs in order to see that a
// transition would be refused.
func (s *Service) ActiveWriterFleet(ctx context.Context) ([]FleetMember, error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	var members []FleetMember
	err := database.Retry(ctx, func() error {
		return s.pool.InTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly}, func(tx pgx.Tx) error {
			now, err := databaseTime(ctx, tx)
			if err != nil {
				return err
			}
			members, err = activeWriterFleet(ctx, tx, now)
			if errors.Is(err, ErrWriterFleetTooOld) {
				members = nil
				return nil
			}
			return err
		})
	}, nil)
	if err != nil {
		return nil, err
	}
	return members, nil
}
