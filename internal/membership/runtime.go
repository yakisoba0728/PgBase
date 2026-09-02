package membership

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
)

const writerHeartbeatInterval = 20 * time.Second

// Runtime binds one process identity to its durable writer lease. Writer
// handles can only be obtained from a successfully registered runtime.
type Runtime struct {
	service           *Service
	instanceID        uuid.UUID
	version           WriterVersion
	heartbeatInterval time.Duration
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
		heartbeatInterval: writerHeartbeatInterval,
	}
	if err := runtime.refresh(ctx); err != nil {
		return nil, err
	}
	if err := runtime.bootstrapFreshInstall(ctx); err != nil {
		return nil, err
	}
	return runtime, nil
}

// bootstrapFreshInstall is where a new install is taken to the retired
// terminal state. Registration is the one place both pgbase.App and a direct
// library caller reach, so putting it here is what lets a caller that builds a
// record service by hand list records on a database it just migrated.
//
// Only the current release may do it. The terminal state fixes
// minimum_writer_version at Release B, so a runtime that registered as an
// earlier release would promote the database out from under itself, and a
// process that is deliberately impersonating a mid-rollout artifact must not
// carry the database past the release it is pretending to be.
func (r *Runtime) bootstrapFreshInstall(ctx context.Context) error {
	if r.version != WriterC {
		return nil
	}
	_, err := r.service.BootstrapFreshInstall(ctx)
	return err
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

// Run keeps the durable lease current and fails closed on any heartbeat error.
func (r *Runtime) Run(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := r.refresh(ctx); err != nil {
		return err
	}
	if r.heartbeatInterval <= 0 {
		return ErrInvalidState
	}
	ticker := time.NewTicker(r.heartbeatInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			if err := r.refresh(ctx); err != nil {
				return err
			}
		case <-ctx.Done():
			return context.Cause(ctx)
		}
	}
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
