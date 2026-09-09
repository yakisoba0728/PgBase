package sqlconsole

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

const (
	MaximumGlobalRunning  = 4
	MaximumSessionRunning = 2
	MaximumPending        = 16
	AdmissionTimeout      = 250 * time.Millisecond
)

type SessionKey [sha256.Size]byte

type Actor struct {
	AdministratorID uuid.UUID
	SessionKey      SessionKey
}

func DeriveSessionKey(secret, rawSession []byte) SessionKey {
	hash := hmac.New(sha256.New, secret)
	writeDigestField(hash, []byte("sql-console-session\x00"))
	writeDigestField(hash, rawSession)
	var result SessionKey
	copy(result[:], hash.Sum(nil))
	return result
}

type Executor interface {
	Validate(Request) error
	Run(context.Context, RunInput) (Result, error)
}

type executorCloser interface {
	Close()
}

type Service struct {
	executor     Executor
	ledger       *Ledger
	secret       []byte
	writeEnabled bool

	ctx       context.Context
	cancel    context.CancelFunc
	global    chan struct{}
	pending   chan struct{}
	wait      sync.WaitGroup
	closeOnce sync.Once

	mu        sync.Mutex
	closed    bool
	jobs      map[uuid.UUID]*executionJob
	sessions  map[SessionKey]*sessionLimiter
	closeDone chan struct{}
}

type sessionLimiter struct {
	semaphore  chan struct{}
	references int
}

type executionJob struct {
	actor       Actor
	identity    LedgerIdentity
	execution   Execution
	ctx         context.Context
	cancel      context.CancelFunc
	limiter     *sessionLimiter
	pendingOnce sync.Once
	canceled    bool
}

func NewService(ctx context.Context, executor Executor, ledger *Ledger, secret []byte, writeEnabled bool) (*Service, error) {
	if ctx == nil || executor == nil || ledger == nil || len(secret) < 32 {
		return nil, errors.New("SQL console service configuration is incomplete")
	}
	if err := ledger.Reconcile(ctx); err != nil {
		return nil, fmt.Errorf("reconcile SQL console ledger: %w", err)
	}
	serviceContext, cancel := context.WithCancel(context.Background())
	return &Service{
		executor: executor, ledger: ledger, secret: append([]byte(nil), secret...), writeEnabled: writeEnabled,
		ctx: serviceContext, cancel: cancel, global: make(chan struct{}, MaximumGlobalRunning),
		pending: make(chan struct{}, MaximumPending), jobs: make(map[uuid.UUID]*executionJob),
		sessions: make(map[SessionKey]*sessionLimiter), closeDone: make(chan struct{}),
	}, nil
}

func (service *Service) Submit(ctx context.Context, actor Actor, request Request) (Execution, error) {
	if err := validateActor(actor); err != nil {
		return Execution{}, err
	}
	normalized, err := NormalizeRequest(request, service.writeEnabled)
	if err != nil {
		return Execution{}, err
	}
	if err := service.reservePending(ctx); err != nil {
		return Execution{}, err
	}
	reserved := true
	defer func() {
		if reserved {
			<-service.pending
		}
	}()

	identity := LedgerIdentity{
		AdministratorID:  actor.AdministratorID,
		Request:          normalized,
		RequestHash:      RequestDigest(service.secret, normalized),
		QueryFingerprint: QueryFingerprint(service.secret, normalized.Statements),
		IdempotencyHash:  IdempotencyDigest(service.secret, normalized.IdempotencyKey),
	}
	pending, created, err := service.ledger.CreatePending(ctx, identity)
	if err != nil {
		return Execution{}, err
	}
	if !created {
		return service.replayed(actor, pending)
	}

	jobContext, cancel := context.WithCancel(service.ctx)
	job := &executionJob{actor: actor, identity: identity, execution: pending, ctx: jobContext, cancel: cancel}
	service.mu.Lock()
	if service.closed {
		service.mu.Unlock()
		cancel()
		pending.Status = StatusCanceled
		pending.Failure = &Failure{Code: "canceled", Message: failureMessage("canceled")}
		_ = service.ledger.Finish(context.Background(), actor.AdministratorID, pending)
		return Execution{}, ErrClosed
	}
	limiter := service.sessions[actor.SessionKey]
	if limiter == nil {
		limiter = &sessionLimiter{semaphore: make(chan struct{}, MaximumSessionRunning)}
		service.sessions[actor.SessionKey] = limiter
	}
	limiter.references++
	job.limiter = limiter
	service.jobs[pending.ID] = job
	service.wait.Add(1)
	service.mu.Unlock()

	if err := service.executor.Validate(normalized); err != nil {
		reserved = false
		service.releasePending(job)
		execution := service.finishWithoutRun(job, StatusFailed, failureFromError(err, "rejected"), nil)
		service.releaseSession(job)
		job.cancel()
		service.wait.Done()
		return execution, nil
	}

	reserved = false
	go service.run(job)
	return CloneExecution(pending), nil
}

func (service *Service) reservePending(ctx context.Context) error {
	service.mu.Lock()
	closed := service.closed
	service.mu.Unlock()
	if closed {
		return ErrClosed
	}
	timer := time.NewTimer(AdmissionTimeout)
	defer timer.Stop()
	select {
	case service.pending <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-service.ctx.Done():
		return ErrClosed
	case <-timer.C:
		return ErrBusy
	}
}

func (service *Service) replayed(actor Actor, durable Execution) (Execution, error) {
	service.mu.Lock()
	defer service.mu.Unlock()
	job := service.jobs[durable.ID]
	if job == nil {
		return durable, nil
	}
	if job.actor.AdministratorID != actor.AdministratorID || job.actor.SessionKey != actor.SessionKey {
		return durable, nil
	}
	return CloneExecution(job.execution), nil
}

func (service *Service) run(job *executionJob) {
	defer service.wait.Done()
	defer service.releaseSession(job)
	if !acquireSemaphore(job.ctx, job.limiter.semaphore) {
		service.releasePending(job)
		service.finishWithoutRun(job, StatusCanceled, failureFromError(job.ctx.Err(), "canceled"), nil)
		return
	}
	defer func() { <-job.limiter.semaphore }()
	if !acquireSemaphore(job.ctx, service.global) {
		service.releasePending(job)
		service.finishWithoutRun(job, StatusCanceled, failureFromError(job.ctx.Err(), "canceled"), nil)
		return
	}
	defer func() { <-service.global }()
	service.releasePending(job)

	running, err := service.ledger.MarkRunning(job.ctx, job.actor.AdministratorID, job.execution.ID)
	if err != nil {
		service.finishWithoutRun(job, StatusFailed, failureFromError(err, "start_log_failed"), nil)
		return
	}
	service.mu.Lock()
	job.execution = running
	service.mu.Unlock()

	result, runErr := service.executor.Run(job.ctx, RunInput{
		AdministratorID: job.actor.AdministratorID,
		Request:         job.identity.Request,
		RequestHash:     job.identity.RequestHash,
		IdempotencyHash: job.identity.IdempotencyHash,
	})
	status, failure := service.runOutcome(job, runErr)
	service.finishWithoutRun(job, status, failure, &result)
}

func (service *Service) runOutcome(job *executionJob, err error) (Status, *Failure) {
	if err == nil {
		return StatusSucceeded, nil
	}
	if errors.Is(err, ErrCommitUnknown) {
		return StatusUnknown, failureFromError(err, "commit_unknown")
	}
	service.mu.Lock()
	canceled := job.canceled
	service.mu.Unlock()
	if canceled || errors.Is(err, context.Canceled) {
		return StatusCanceled, failureFromError(err, "canceled")
	}
	if errors.Is(err, context.DeadlineExceeded) || SQLState(err) == "57014" {
		return StatusTimedOut, failureFromError(err, "timed_out")
	}
	return StatusFailed, failureFromError(err, failureCode(err))
}

func (service *Service) finishWithoutRun(job *executionJob, status Status, failure *Failure, result *Result) Execution {
	now := time.Now().UTC()
	service.mu.Lock()
	job.execution.Status = status
	job.execution.FinishedAt = now
	job.execution.ResultUntil = now.Add(ResultTTL)
	job.execution.Failure = failure
	if result != nil {
		job.execution.Result = result
	}
	execution := CloneExecution(job.execution)
	service.mu.Unlock()

	finishContext, cancel := context.WithTimeout(context.Background(), time.Second)
	_ = service.ledger.Finish(finishContext, job.actor.AdministratorID, execution)
	cancel()
	service.scheduleExpiry(job)
	return execution
}

func (service *Service) scheduleExpiry(job *executionJob) {
	time.AfterFunc(ResultTTL, func() {
		service.mu.Lock()
		defer service.mu.Unlock()
		if current := service.jobs[job.execution.ID]; current == job && terminalStatus(current.execution.Status) {
			delete(service.jobs, job.execution.ID)
		}
	})
}

func (service *Service) releasePending(job *executionJob) {
	job.pendingOnce.Do(func() { <-service.pending })
}

func (service *Service) releaseSession(job *executionJob) {
	service.mu.Lock()
	defer service.mu.Unlock()
	job.limiter.references--
	if job.limiter.references == 0 {
		delete(service.sessions, job.actor.SessionKey)
	}
}

func acquireSemaphore(ctx context.Context, semaphore chan struct{}) bool {
	select {
	case semaphore <- struct{}{}:
		return true
	case <-ctx.Done():
		return false
	}
}

func (service *Service) Get(ctx context.Context, actor Actor, executionID uuid.UUID) (Execution, error) {
	if err := validateActor(actor); err != nil {
		return Execution{}, err
	}
	service.mu.Lock()
	job := service.jobs[executionID]
	if job != nil {
		if job.actor.AdministratorID != actor.AdministratorID || job.actor.SessionKey != actor.SessionKey {
			service.mu.Unlock()
			return Execution{}, ErrForbidden
		}
		execution := CloneExecution(job.execution)
		service.mu.Unlock()
		if !execution.ResultUntil.IsZero() && time.Now().After(execution.ResultUntil) {
			return service.ledger.Get(ctx, actor.AdministratorID, executionID)
		}
		return execution, nil
	}
	service.mu.Unlock()
	execution, err := service.ledger.Get(ctx, actor.AdministratorID, executionID)
	if errors.Is(err, pgx.ErrNoRows) {
		return Execution{}, ErrNotFound
	}
	return execution, err
}

func (service *Service) Cancel(_ context.Context, actor Actor, executionID uuid.UUID) (Execution, error) {
	if err := validateActor(actor); err != nil {
		return Execution{}, err
	}
	service.mu.Lock()
	defer service.mu.Unlock()
	job := service.jobs[executionID]
	if job == nil {
		return Execution{}, ErrNotFound
	}
	if job.actor.AdministratorID != actor.AdministratorID || job.actor.SessionKey != actor.SessionKey {
		return Execution{}, ErrForbidden
	}
	if terminalStatus(job.execution.Status) {
		return CloneExecution(job.execution), nil
	}
	job.canceled = true
	job.cancel()
	return CloneExecution(job.execution), nil
}

func (service *Service) List(ctx context.Context, actor Actor, cursor Cursor) (Page, error) {
	if err := validateActor(actor); err != nil {
		return Page{}, err
	}
	return service.ledger.List(ctx, actor.AdministratorID, cursor)
}

func (service *Service) Close(ctx context.Context) error {
	service.closeOnce.Do(func() {
		service.mu.Lock()
		service.closed = true
		for _, job := range service.jobs {
			if !terminalStatus(job.execution.Status) {
				job.canceled = true
				job.cancel()
			}
		}
		service.mu.Unlock()
		service.cancel()
		go func() {
			service.wait.Wait()
			if closer, ok := service.executor.(executorCloser); ok {
				closer.Close()
			}
			close(service.closeDone)
		}()
	})
	select {
	case <-service.closeDone:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func validateActor(actor Actor) error {
	if actor.AdministratorID == uuid.Nil || actor.SessionKey == (SessionKey{}) {
		return ErrForbidden
	}
	return nil
}

func failureFromError(err error, code string) *Failure {
	failure := &Failure{Code: code, Message: failureMessage(code)}
	var rejection *Rejection
	if errors.As(err, &rejection) {
		failure.StatementIndex = rejection.StatementIndex
		failure.Position = rejection.Position
		failure.HasStatement = true
	}
	var statementError *StatementExecutionError
	if errors.As(err, &statementError) {
		failure.StatementIndex = statementError.Index
		failure.HasStatement = true
	}
	var postgresError *pgconn.PgError
	if errors.As(err, &postgresError) {
		failure.SQLState = postgresError.Code
		failure.Position = int(postgresError.Position)
	}
	return failure
}

func failureCode(err error) string {
	switch {
	case errors.Is(err, ErrResultLimit):
		return "result_limit"
	case errors.Is(err, ErrAffectedLimit):
		return "affected_limit"
	case errors.Is(err, ErrTargetChanged):
		return "target_changed"
	case SQLState(err) != "":
		return "postgres_error"
	default:
		return "failed"
	}
}
