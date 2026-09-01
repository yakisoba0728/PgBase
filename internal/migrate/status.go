package migrate

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/webisnoteasy/PgBase/internal/observability"
)

// Status describes the embedded system schema migration state.
type Status struct {
	Current    int64
	Latest     int64
	Dirty      bool
	Compatible bool
	Issue      *HistoryIssue
}

// Status returns the applied and latest embedded migration versions. Dirty is
// true when recorded migration history no longer matches this binary.
func (r *Runner) Status(ctx context.Context) (status Status, err error) {
	started := time.Now()
	defer r.finishOperation(ctx, observability.OperationMigrationStatus, started, &err)
	if r == nil || r.pool == nil {
		return Status{}, fmt.Errorf("migration pool is nil")
	}
	policy, err := validateHistoryPolicy(r.migrations, r.compatibility)
	if err != nil {
		return Status{}, err
	}
	empty := inspectHistory(r.migrations, nil)
	status.Latest = empty.latest
	applied, present, err := readAppliedIfPresent(ctx, r.pool)
	if err != nil {
		var issue *HistoryIssue
		if errors.As(err, &issue) {
			status.Current = issue.appliedVersion
			status.Dirty = true
			status.Issue = issue
			return status, nil
		}
		return Status{}, err
	}
	if !present {
		status.Compatible = policy.compatible(empty.state)
		return status, nil
	}
	history := inspectHistory(r.migrations, applied)
	if history.err != nil {
		return Status{}, history.err
	}
	status.Current = history.state.Version
	status.Dirty = history.state.Dirty
	status.Compatible = policy.compatible(history.state)
	status.Issue = history.issue
	return status, nil
}
