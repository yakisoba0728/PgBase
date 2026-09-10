package backup

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
)

var ErrBusy = errors.New("another backup operation is running")
var ErrNotFound = errors.New("backup or job not found")

type Entry struct {
	ID              string    `json:"id"`
	CreatedAt       time.Time `json:"createdAt"`
	Size            int64     `json:"size"`
	PostgreSQLMajor int       `json:"postgresqlMajor"`
	SHA256          string    `json:"sha256,omitempty"`
	StorageFormat   string    `json:"storageFormat,omitempty"`
}
type Job struct {
	ID        string    `json:"id"`
	Operation string    `json:"operation"`
	BackupID  string    `json:"backupId"`
	Status    string    `json:"status"`
	Phase     string    `json:"phase"`
	Error     string    `json:"error,omitempty"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}
type ManagerOptions struct {
	LogError   func(context.Context, error)
	Directory  string
	Create     func(context.Context, string) error
	Restore    func(context.Context, string) error
	Repository Repository
	Retention  int
	Lifecycle  Lifecycle
}

type LifecycleEvent struct {
	Operation string
	BackupID  string
	Job       Job
}
type Lifecycle interface {
	BeginBackupOperation(context.Context, *LifecycleEvent) (func(error), error)
}

// Manager owns a private local archive directory and one serialized operation at
// a time. Job receipts survive database restore and process restart. Callbacks
// receive verified directories, never user-selected paths or database URLs.
type Manager struct {
	options             ManagerOptions
	mu                  sync.Mutex
	busy                bool
	closed              bool
	jobs                map[string]Job
	cancel              context.CancelFunc
	ctx                 context.Context
	wg                  sync.WaitGroup
	retentionOverride   atomic.Int64
	retentionConfigured atomic.Bool
}

func NewManager(options ManagerOptions) (*Manager, error) {
	if options.Directory == "" || options.Create == nil || options.Restore == nil {
		return nil, errors.New("backup directory and operation callbacks are required")
	}
	absolute, err := filepath.Abs(options.Directory)
	if err != nil {
		return nil, err
	}
	options.Directory = absolute
	if err = os.MkdirAll(absolute, 0700); err != nil {
		return nil, err
	}
	info, err := os.Lstat(absolute)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("backup directory must be a real private directory")
	}
	if err = os.Chmod(absolute, 0700); err != nil {
		return nil, err
	}
	if options.Repository == nil {
		options.Repository = NewLocalRepository(absolute)
	}
	if err = ensureRepositoryIdentity(absolute, options.Repository); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	m := &Manager{options: options, jobs: map[string]Job{}, ctx: ctx, cancel: cancel}
	paths, err := filepath.Glob(filepath.Join(absolute, "*.job.json"))
	if err != nil {
		cancel()
		return nil, err
	}
	for _, path := range paths {
		raw, err := os.ReadFile(path)
		if err != nil {
			cancel()
			return nil, err
		}
		var job Job
		if json.Unmarshal(raw, &job) != nil || !validID(job.ID) {
			continue
		}
		if job.Status == "running" {
			job.Status = "failed"
			job.Phase = "interrupted"
			job.Error = "The process stopped before completion. Inspect the active target before retrying restore."
			job.UpdatedAt = time.Now().UTC()
			if err = m.persistJob(job); err != nil {
				cancel()
				return nil, err
			}
		}
		m.jobs[job.ID] = job
	}
	return m, nil
}
func validID(id string) bool {
	u, err := uuid.Parse(id)
	return err == nil && u != uuid.Nil && u.String() == id
}
func writeAtomic(path string, value any) error {
	raw, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), ".receipt-")
	if err != nil {
		return err
	}
	name := temporary.Name()
	defer func() { _ = os.Remove(name) }()
	_, writeErr := temporary.Write(append(raw, '\n'))
	err = errors.Join(writeErr, temporary.Sync(), temporary.Close())
	if err != nil {
		return err
	}
	return DurableRename(name, path)
}
func (m *Manager) persistJob(job Job) error {
	return writeAtomic(filepath.Join(m.options.Directory, job.ID+".job.json"), job)
}
func (m *Manager) List(ctx context.Context) ([]Entry, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return m.options.Repository.List(ctx)
}
func (m *Manager) Job(ctx context.Context, id string) (Job, error) {
	if err := ctx.Err(); err != nil {
		return Job{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	job, ok := m.jobs[id]
	if !ok {
		return Job{}, ErrNotFound
	}
	return job, nil
}
func (m *Manager) reserve() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return errors.New("backup manager is closed")
	}
	if m.busy {
		return ErrBusy
	}
	m.busy = true
	m.wg.Add(1)
	return nil
}
func (m *Manager) release() {
	m.mu.Lock()
	m.busy = false
	m.mu.Unlock()
	m.wg.Done()
}
func (m *Manager) Create(ctx context.Context) (Job, error) {
	return m.start(ctx, "create", uuid.NewString())
}
func (m *Manager) Restore(ctx context.Context, id string) (Job, error) {
	if !validID(id) {
		return Job{}, ErrNotFound
	}
	if _, err := m.options.Repository.Get(ctx, id); err != nil {
		return Job{}, ErrNotFound
	}
	return m.start(ctx, "restore", id)
}
func (m *Manager) start(ctx context.Context, operation, id string) (Job, error) {
	if err := ctx.Err(); err != nil {
		return Job{}, err
	}
	event := &LifecycleEvent{Operation: operation, BackupID: id}
	finish := func(error) {}
	if m.options.Lifecycle != nil {
		var err error
		finish, err = m.options.Lifecycle.BeginBackupOperation(ctx, event)
		if err != nil {
			finish(err)
			return Job{}, err
		}
	}
	if err := m.reserve(); err != nil {
		finish(err)
		return Job{}, err
	}
	now := time.Now().UTC()
	job := Job{ID: uuid.NewString(), Operation: operation, BackupID: id, Status: "running", Phase: operation, CreatedAt: now, UpdatedAt: now}
	if err := m.persistJob(job); err != nil {
		m.release()
		finish(err)
		return Job{}, err
	}
	m.mu.Lock()
	m.jobs[job.ID] = job
	m.mu.Unlock()
	go m.execute(job, event, finish)
	return job, nil
}
func (m *Manager) execute(job Job, event *LifecycleEvent, finish func(error)) {
	defer m.release()
	ctx, cancel := context.WithTimeout(m.ctx, 30*time.Minute)
	defer cancel()
	ctx = m.progressContext(ctx, job.ID)
	err := m.executeOperation(ctx, job)
	if err != nil && m.options.LogError != nil {
		m.options.LogError(ctx, err)
	}
	job.UpdatedAt = time.Now().UTC()
	job.Status = "completed"
	job.Phase = "completed"
	if err != nil {
		job.Status = "failed"
		job.Phase = "failed"
		job.Error = "Backup operation failed; the previous target was retained. Check the server diagnostic log."
	}
	m.mu.Lock()
	if receiptErr := m.persistJob(job); receiptErr != nil {
		job.Status = "failed"
		job.Error = "Unable to persist the final backup receipt."
		err = errors.Join(err, receiptErr)
	}
	m.jobs[job.ID] = job
	m.mu.Unlock()
	event.Job = job
	finish(err)
}
func (m *Manager) executeOperation(ctx context.Context, job Job) error {
	temp, err := os.MkdirTemp(m.options.Directory, ".job-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(temp) }()
	directory := filepath.Join(temp, "backup")
	if job.Operation == "create" {
		if err = m.options.Create(ctx, directory); err != nil {
			return err
		}
		if err = Progress(ctx, "packing_archive"); err != nil {
			return err
		}
		_, err = m.publish(ctx, directory, job.BackupID)
		return err
	}
	file, err := m.options.Repository.Open(ctx, job.BackupID)
	if err != nil {
		return err
	}
	if err = Progress(ctx, "validating_archive"); err != nil {
		_ = file.Close()
		return err
	}
	entry, metadataErr := m.options.Repository.Get(ctx, job.BackupID)
	if metadataErr != nil {
		_ = file.Close()
		return metadataErr
	}
	verified := newVerifiedReadCloser(file, entry)
	err = Unpack(ctx, verified, directory)
	err = errors.Join(err, verified.Close())
	if err != nil {
		return err
	}
	return m.options.Restore(ctx, directory)
}
func (m *Manager) publish(ctx context.Context, directory, id string) (Entry, error) {
	manifest, err := VerifyContext(ctx, directory)
	if err != nil {
		return Entry{}, err
	}
	file, err := os.CreateTemp(m.options.Directory, ".archive-")
	if err != nil {
		return Entry{}, err
	}
	name := file.Name()
	defer func() { _ = os.Remove(name) }()
	digest := sha256.New()
	err = Pack(ctx, directory, io.MultiWriter(file, digest))
	err = errors.Join(err, file.Sync(), file.Close())
	if err != nil {
		return Entry{}, err
	}
	info, err := os.Stat(name)
	if err != nil {
		return Entry{}, err
	}
	if info.Size() > MaximumArchiveBytes {
		return Entry{}, errors.New("backup archive exceeds limit")
	}
	entry := Entry{ID: id, CreatedAt: manifest.CreatedAt, Size: info.Size(), PostgreSQLMajor: manifest.PostgreSQLMajor, SHA256: hex.EncodeToString(digest.Sum(nil)), StorageFormat: s3StorageFormat}
	archive, err := os.Open(name)
	if err != nil {
		return Entry{}, err
	}
	err = m.options.Repository.Publish(ctx, entry, archive)
	err = errors.Join(err, archive.Close())
	if err != nil {
		return Entry{}, err
	}
	if err = m.prune(ctx, id); err != nil {
		return Entry{}, err
	}
	return entry, nil
}

func (m *Manager) prune(ctx context.Context, currentID string) error {
	retention := m.options.Retention
	if m.retentionConfigured.Load() {
		retention = int(m.retentionOverride.Load())
	}
	if provider, ok := m.options.Repository.(interface{ Retention() int }); ok {
		if !m.retentionConfigured.Load() {
			retention = provider.Retention()
		}
	}
	if retention <= 0 {
		return nil
	}
	entries, err := m.options.Repository.List(ctx)
	if err != nil {
		return err
	}
	if len(entries) <= retention {
		return nil
	}
	remove := len(entries) - retention
	for index := len(entries) - 1; index >= 0 && remove > 0; index-- {
		entry := entries[index]
		if entry.ID == currentID {
			continue
		}
		if err := m.options.Repository.Delete(ctx, entry.ID); err != nil {
			return err
		}
		remove--
	}
	return nil
}

// SetRetention changes the pruning window used by future backups. Zero keeps
// every archive; positive values prune after each successful publish.
func (m *Manager) SetRetention(value int) {
	if value < 0 {
		return
	}
	m.retentionOverride.Store(int64(value))
	m.retentionConfigured.Store(true)
}
func (m *Manager) Upload(ctx context.Context, source io.Reader) (Entry, error) {
	if err := m.reserve(); err != nil {
		return Entry{}, err
	}
	defer m.release()
	temp, err := os.MkdirTemp(m.options.Directory, ".upload-")
	if err != nil {
		return Entry{}, err
	}
	defer func() { _ = os.RemoveAll(temp) }()
	directory := filepath.Join(temp, "backup")
	if err = Unpack(ctx, source, directory); err != nil {
		return Entry{}, err
	}
	return m.publish(ctx, directory, uuid.NewString())
}
func (m *Manager) Open(ctx context.Context, id string) (io.ReadCloser, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !validID(id) {
		return nil, ErrNotFound
	}
	entry, err := m.options.Repository.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	source, err := m.options.Repository.Open(ctx, id)
	if err != nil {
		return nil, err
	}
	return newVerifiedReadCloser(source, entry), nil
}
func (m *Manager) Delete(ctx context.Context, id string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !validID(id) {
		return ErrNotFound
	}
	if err := m.reserve(); err != nil {
		return err
	}
	defer m.release()
	finish := func(error) {}
	if m.options.Lifecycle != nil {
		event := &LifecycleEvent{Operation: "delete", BackupID: id}
		var err error
		finish, err = m.options.Lifecycle.BeginBackupOperation(ctx, event)
		if err != nil {
			finish(err)
			return err
		}
	}
	err := m.options.Repository.Delete(ctx, id)
	finish(err)
	return err
}
func (m *Manager) Shutdown(ctx context.Context) error {
	m.mu.Lock()
	m.closed = true
	m.cancel()
	m.mu.Unlock()
	done := make(chan struct{})
	go func() { m.wg.Wait(); close(done) }()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
