package backup

import (
	"context"
	"errors"
	"os"
	"sync"

	"github.com/jackc/pgx/v5"
)

var ErrActivationOutcomeUnknown = errors.New("restore activation outcome is unknown; retain both targets")

type targetOwnership struct {
	mu                          sync.Mutex
	controlURL, name, directory string
	discarded                   bool
}

// DiscardTarget removes only a target created by this process's RestoreNew.
// Targets read from configuration have no ownership capability and cannot be
// destroyed through this method. Call only after a failed activation is proven
// not to have published the target, and after closing its candidate App.
func DiscardTarget(ctx context.Context, target Target) error {
	ownership := target.ownership
	if ownership == nil {
		return errors.New("restore target is not owned by this operation")
	}
	ownership.mu.Lock()
	defer ownership.mu.Unlock()
	if ownership.discarded {
		return nil
	}
	conn, err := connectDatabase(ctx, ownership.controlURL)
	if err != nil {
		return err
	}
	_, dropErr := conn.Exec(ctx, "DROP DATABASE IF EXISTS "+pgx.Identifier{ownership.name}.Sanitize()+" WITH (FORCE)")
	err = errors.Join(dropErr, conn.Close(context.Background()))
	if err != nil {
		return err
	}
	if err = os.RemoveAll(ownership.directory); err != nil {
		return err
	}
	ownership.discarded = true
	return nil
}
