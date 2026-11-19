package sqlconsole

import (
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestCompletedResultCacheBounds(t *testing.T) {
	now := time.Now()
	service := &Service{jobs: make(map[uuid.UUID]*executionJob)}
	add := func(session byte, cost int) uuid.UUID {
		id := uuid.New()
		job := &executionJob{
			actor: Actor{SessionKey: SessionKey{session}}, resultCost: cost,
			execution: Execution{ID: id, ResultUntil: now.Add(ResultTTL)},
		}
		service.jobs[id] = job
		service.cacheCompleted(job, now)
		return id
	}
	other := add(1, 1)
	old := add(2, sessionResultBudget)
	newest := add(2, sessionResultBudget)
	if service.jobs[old] != nil || service.jobs[other] == nil || service.jobs[newest] == nil {
		t.Fatal("session limit must evict the oldest result from that session")
	}
	for session := byte(3); session <= 6; session++ {
		add(session, sessionResultBudget)
	}
	if service.jobs[newest] != nil || len(service.completed) != 4 {
		t.Fatal("process limit must evict the oldest results across sessions")
	}
	oldest := service.completed[0].execution.ID
	for range maximumCompletedResults {
		add(1, 0)
	}
	if service.jobs[oldest] != nil || len(service.jobs) != maximumCompletedResults {
		t.Fatal("count limit must apply even when jobs have no raw results")
	}
	service.expireCompleted(now.Add(ResultTTL))
	if len(service.completed) != 0 || len(service.jobs) != 0 {
		t.Fatal("expired results must leave the cache")
	}
}
