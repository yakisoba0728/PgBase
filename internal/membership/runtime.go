package membership

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
)

const (
	writerHeartbeatInterval = 20 * time.Second
	// A heartbeat that lost a renewal retries at this interval instead of
	// waiting out a whole period, so a released lock is picked up promptly.
	writerHeartbeatRetryInterval = 2 * time.Second
	// The lease refreshWriter writes, and how long renewal may keep failing.
	// The gap between them is what stops retrying from running the lease out.
	writerLeaseTTL       = time.Minute
	writerLeaseTolerance = 50 * time.Second
)

// Runtime binds one process identity to its durable writer lease. Writer
// handles can only be obtained from a successfully registered runtime.
type Runtime struct {
	service           *Service
	instanceID        uuid.UUID
	version           WriterVersion
	heartbeatInterval time.Duration
	leaseTolerance    time.Duration
}

// RegisterWriter creates the process identity and performs the mandatory
// initial heartbeat before the caller can construct a writer. Release A0 is
// allowed to start on the exact v6 history, where the lease table does not yet
// exist; its Run loop switches to a durable lease as soon as v7 appears.
func RegisterWriter(ctx context.Context, service *Service, version WriterVersion) (*Runtime, error) {
	if service == nil || version < WriterA0 || version > WriterC {
		return nil, ErrInvalidState
	}
	instanceID, err := uuid.NewV7()
	if err != nil {
		return nil, err
	}
	runtime := &Runtime{
		service: service, instanceID: instanceID, version: version,
		heartbeatInterval: writerHeartbeatInterval, leaseTolerance: writerLeaseTolerance,
	}
	if err := runtime.refresh(ctx); err != nil {
		return nil, err
	}
	return runtime, nil
}

func (r *Runtime) refresh(ctx context.Context) error {
	if r == nil || r.service == nil || r.instanceID == uuid.Nil ||
		r.version < WriterA0 || r.version > WriterC {
		return ErrInvalidState
	}
	installation, err := inspectInstallation(ctx, r.service.pool)
	if err != nil {
		return err
	}
	switch installation {
	case installationPreMigration:
		if r.version != WriterA0 {
			return ErrUnavailable
		}
		return nil
	case installationComplete:
		return r.service.refreshWriter(ctx, r.instanceID, r.version)
	default:
		return ErrInvalidState
	}
}

// Writer returns a transaction fence bound to this registered process.
func (r *Runtime) Writer() (Writer, error) {
	if r == nil || r.service == nil || r.instanceID == uuid.Nil ||
		r.version < WriterA0 || r.version > WriterC {
		return Writer{}, ErrInvalidState
	}
	return Writer{instanceID: r.instanceID, version: r.version}, nil
}

// WriterVersion exposes only the immutable release generation bound to this
// registered runtime. Invalid runtimes return an out-of-range version.
func (r *Runtime) WriterVersion() WriterVersion {
	if r == nil || r.service == nil || r.instanceID == uuid.Nil ||
		r.version < WriterA0 || r.version > WriterC {
		return WriterVersion(-1)
	}
	return r.version
}

// FleetMember returns the exact identity/version pair used by rollout proofs.
func (r *Runtime) FleetMember() FleetMember {
	if r == nil {
		return FleetMember{}
	}
	return FleetMember{InstanceID: r.instanceID, Version: r.version}
}

// Run keeps the durable lease current. Losing one renewal to contention — a
// rollout transition holding the capability row, a saturated pool — is ordinary
// and is retried while the lease already held is still safely in the future; a
// lease about to run out, or a refusal no lease time could excuse, still fails
// closed. No write depends on this loop: the fence re-reads lease_expires_at
// and minimum_writer_version inside the writing transaction.
func (r *Runtime) Run(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if r.heartbeatInterval <= 0 || r.leaseTolerance <= 0 {
		return ErrInvalidState
	}
	// Read before the round trip: the database stamps the expiry from its own
	// clock at or after this instant, so this is a lower bound on the lease.
	tolerated := time.Now().Add(r.leaseTolerance)
	if err := r.refresh(ctx); err != nil {
		return err
	}
	ticker := time.NewTicker(r.heartbeatInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			attempted := time.Now()
			err := r.refresh(ctx)
			switch {
			case err == nil:
				tolerated = attempted.Add(r.leaseTolerance)
				ticker.Reset(r.heartbeatInterval)
			case ctx.Err() != nil || heartbeatFailsClosed(err) || !time.Now().Before(tolerated):
				return err
			default:
				ticker.Reset(writerHeartbeatRetryInterval)
			}
		case <-ctx.Done():
			return context.Cause(ctx)
		}
	}
}

// heartbeatFailsClosed reports the renewal failures that must stop this writer
// at once whatever the lease says: below the fleet minimum writer version, or
// no longer the installation it registered against. The rest is contention.
func heartbeatFailsClosed(err error) bool {
	return errors.Is(err, ErrWriterTooOld) || errors.Is(err, ErrInvalidState) ||
		errors.Is(err, ErrUnavailable)
}

func (r *Runtime) Ready(ctx context.Context) error {
	if err := r.refresh(ctx); err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return err
		}
		return ErrUnavailable
	}
	return nil
}
