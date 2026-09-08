package schema

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/webisnoteasy/PgBase/internal/database"
)

func (s *Service) buildCompanionIndexes(ctx context.Context, companions []IndexSpec) (returnErr error) {
	connection, err := s.pool.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("acquire schema companion index maintenance connection: %w", err)
	}
	var inheritedTimeout string
	lockUncertain, lockHeld := false, false
	defer func() {
		var cleanupErr error
		if lockHeld {
			cleanupCtx, cancel := database.BoundedCleanupContext(context.Background())
			if _, err := connection.Exec(cleanupCtx,
				"select pg_advisory_unlock($1)", companionIndexAdvisoryKey); err != nil {
				cleanupErr = fmt.Errorf("release schema companion index maintenance lock: %w", err)
			}
			if _, err := connection.Exec(cleanupCtx,
				"select set_config('statement_timeout', $1, false)", inheritedTimeout); err != nil {
				cleanupErr = errors.Join(cleanupErr,
					fmt.Errorf("restore statement timeout after companion index maintenance: %w", err))
			}
			cancel()
		}
		if !lockUncertain && cleanupErr == nil {
			connection.Release()
			return
		}
		closeCtx, cancel := database.BoundedCleanupContext(context.Background())
		cleanupErr = errors.Join(cleanupErr, connection.Hijack().Close(closeCtx))
		cancel()
		returnErr = errors.Join(returnErr, cleanupErr)
	}()

	// Read the inherited setting before taking the session lock. If this probe
	// fails the connection is still clean and can be returned without a repair.
	if err := connection.QueryRow(ctx, "show statement_timeout").Scan(&inheritedTimeout); err != nil {
		return fmt.Errorf("read statement timeout for companion index maintenance: %w", err)
	}

	var acquired bool
	lockUncertain = true
	if err := connection.QueryRow(ctx,
		"select pg_try_advisory_lock($1)", companionIndexAdvisoryKey).Scan(&acquired); err != nil {
		return fmt.Errorf("acquire schema companion index maintenance lock: %w", err)
	}
	lockUncertain = false
	if !acquired {
		return ErrConflict
	}
	lockHeld = true
	// A concurrent build is bounded by the table, not by the request timeout it
	// inherited, and it must not abandon an index half-built on a slow lock. The
	// pool applies its timeout per connection at session scope, so the previous
	// value has to be captured and put back by hand: RESET would fall through to
	// the server default and quietly leave the next borrower with no timeout.
	if _, err := connection.Exec(ctx, "set statement_timeout = 0"); err != nil {
		return fmt.Errorf("disable statement timeout for companion index maintenance: %w", err)
	}

	observed, err := loadCompanionIndexes(ctx, connection, companions)
	if err != nil {
		return err
	}
	for _, companion := range companions {
		found, present := observed[companionKey(companion)]
		if present && found.valid && companionShapeMatches(companion, found) {
			continue
		}
		if present {
			// IF NOT EXISTS would silently accept invalid residue, so the drop is
			// unconditional once the catalog says this name is not usable.
			if _, err := connection.Exec(ctx, "drop index concurrently "+
				pgx.Identifier{companion.Namespace, companion.Name}.Sanitize()); err != nil {
				return fmt.Errorf("drop invalid companion index: %w", err)
			}
		}
		if _, err := connection.Exec(ctx, createCompanionIndexStatement(companion, true)); err != nil {
			return fmt.Errorf("build companion index: %w", err)
		}
	}
	return nil
}
