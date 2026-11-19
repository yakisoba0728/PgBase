package sqlconsole

import "time"

const (
	maximumCompletedResults = 256
	processResultBudget     = 64 << 20
	sessionResultBudget     = 16 << 20
)

// A result's wire length excludes the Go slices, cells and string headers
// retained by CloneExecution. Budget the cloned representation instead.
func resultMemoryCost(result *Result) int {
	if result == nil {
		return 0
	}
	cost := 128
	for _, statement := range result.Statements {
		cost += 128 + len(statement.Command)
		for _, column := range statement.Columns {
			cost += 64 + len(column.Name) + len(column.TypeName)
		}
		for _, row := range statement.Rows {
			cost += 32
			for _, cell := range row {
				cost += 48 + len(cell.Kind) + len(cell.Text)
			}
		}
	}
	return cost
}

// Cache operations hold service.mu. The bounded list is in completion order.
func (service *Service) cacheCompleted(job *executionJob, now time.Time) {
	service.expireCompleted(now)
	service.completed = append(service.completed, job)
	totalCost, sessionCost := 0, 0
	for _, entry := range service.completed {
		totalCost += entry.resultCost
		if entry.actor.SessionKey == job.actor.SessionKey {
			sessionCost += entry.resultCost
		}
	}
	for i := 0; sessionCost > sessionResultBudget && i < len(service.completed); {
		entry := service.completed[i]
		if entry.actor.SessionKey != job.actor.SessionKey {
			i++
			continue
		}
		sessionCost -= entry.resultCost
		totalCost -= entry.resultCost
		service.evictCompleted(i)
	}
	for totalCost > processResultBudget || len(service.completed) > maximumCompletedResults {
		totalCost -= service.completed[0].resultCost
		service.evictCompleted(0)
	}
}

func (service *Service) expireCompleted(now time.Time) {
	for i := 0; i < len(service.completed); {
		if now.Before(service.completed[i].execution.ResultUntil) {
			i++
			continue
		}
		service.evictCompleted(i)
	}
}

func (service *Service) evictCompleted(index int) {
	job := service.completed[index]
	delete(service.jobs, job.execution.ID)
	copy(service.completed[index:], service.completed[index+1:])
	service.completed[len(service.completed)-1] = nil
	service.completed = service.completed[:len(service.completed)-1]
}
