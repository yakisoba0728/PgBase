package backup

import (
	"context"
	"time"
)

type progressKey struct{}

// Progress updates the current durable job phase when called inside a manager
// operation. Direct CLI/module calls have no job receipt and incur no work.
func Progress(ctx context.Context, phase string) error {
	update, _ := ctx.Value(progressKey{}).(func(string) error)
	if update == nil {
		return nil
	}
	return update(phase)
}
func (m *Manager) progressContext(ctx context.Context, jobID string) context.Context {
	return context.WithValue(ctx, progressKey{}, func(phase string) error {
		m.mu.Lock()
		defer m.mu.Unlock()
		job := m.jobs[jobID]
		job.Phase = phase
		job.UpdatedAt = time.Now().UTC()
		if err := m.persistJob(job); err != nil {
			return err
		}
		m.jobs[jobID] = job
		return nil
	})
}
