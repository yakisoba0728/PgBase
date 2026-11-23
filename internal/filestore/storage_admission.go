package filestore

import (
	"context"
	"sync"
)

// maximumStorageOperationsPerSubject is how many requests of one subject may
// hold or queue for the controller's maximumStorageOperations shared slots at
// once. Further requests of that subject wait for one of its own to finish
// before joining the shared queue, so one caller cannot fill every slot while
// others wait behind it.
const maximumStorageOperationsPerSubject = 16

type storageAdmissionContextKey struct{}

// storageAdmission is the per-request charge of storage operations to a
// subject. A request is charged one subject slot however many operations it
// starts: a ranged variant download holds its body open while it reads the
// prefix, and charging each would let a subject's own requests deadlock
// waiting for each other's slots.
type storageAdmission struct {
	subject string

	mu      sync.Mutex
	holders int
	release func()
}

// WithStorageAdmissionSubject names the caller that storage operations begun
// under ctx are charged to: an authenticated subject, or an anonymous client
// network. Call it once per request. Operations without a subject (background
// work, embedded callers) are limited only by the shared bound.
func WithStorageAdmissionSubject(ctx context.Context, subject string) context.Context {
	if subject == "" {
		return ctx
	}
	return context.WithValue(ctx, storageAdmissionContextKey{}, &storageAdmission{subject: subject})
}

// StorageAdmissionSubject reports the subject named by
// WithStorageAdmissionSubject, or "" when ctx has none.
func StorageAdmissionSubject(ctx context.Context) string {
	if admission, ok := ctx.Value(storageAdmissionContextKey{}).(*storageAdmission); ok {
		return admission.subject
	}
	return ""
}

// storageSubjectSlots bounds the requests each subject has in storage
// operations. Entries exist only while a subject has a request admitted or
// queued, so the map is bounded by the callers currently in flight.
type storageSubjectSlots struct {
	limit   int
	mu      sync.Mutex
	entries map[string]*storageSubjectEntry
}

type storageSubjectEntry struct {
	slots      chan struct{}
	references int // Holders plus waiters; guarded by storageSubjectSlots.mu.
}

// admit charges the request in ctx to its subject, waiting until the subject
// has a free slot or ctx ends. A request that already holds a slot is not
// charged again, and a context without a subject is not limited.
func (s *storageSubjectSlots) admit(ctx context.Context) (func(), error) {
	admission, _ := ctx.Value(storageAdmissionContextKey{}).(*storageAdmission)
	if admission == nil {
		return func() {}, nil
	}
	admission.mu.Lock()
	defer admission.mu.Unlock()
	if admission.holders == 0 {
		// Holding admission.mu while waiting serializes only this request's
		// own concurrent operations, which share the one slot anyway.
		release, err := s.acquire(ctx, admission.subject)
		if err != nil {
			return nil, err
		}
		admission.release = release
	}
	admission.holders++
	var once sync.Once
	return func() {
		once.Do(func() {
			admission.mu.Lock()
			defer admission.mu.Unlock()
			admission.holders--
			if admission.holders == 0 {
				admission.release()
				admission.release = nil
			}
		})
	}, nil
}

// acquire waits until subject has a free slot or ctx ends.
func (s *storageSubjectSlots) acquire(ctx context.Context, subject string) (func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	if s.entries == nil {
		s.entries = map[string]*storageSubjectEntry{}
	}
	entry := s.entries[subject]
	if entry == nil {
		limit := s.limit
		if limit < 1 {
			limit = maximumStorageOperationsPerSubject
		}
		entry = &storageSubjectEntry{slots: make(chan struct{}, limit)}
		s.entries[subject] = entry
	}
	entry.references++
	s.mu.Unlock()
	select {
	case entry.slots <- struct{}{}:
		return func() {
			<-entry.slots
			s.drop(subject, entry)
		}, nil
	case <-ctx.Done():
		s.drop(subject, entry)
		return nil, ctx.Err()
	}
}

func (s *storageSubjectSlots) drop(subject string, entry *storageSubjectEntry) {
	s.mu.Lock()
	entry.references--
	if entry.references == 0 {
		delete(s.entries, subject)
	}
	s.mu.Unlock()
}

// active reports the requests subject has admitted; read by tests.
func (s *storageSubjectSlots) active(subject string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	if entry := s.entries[subject]; entry != nil {
		return len(entry.slots)
	}
	return 0
}
