package observability

import (
	"sync"
	"time"
)

// RequestLog retains a bounded, instance-local diagnostic window. Its typed
// input deliberately has no headers, URLs, bodies, user IDs or error strings.
type RequestLog struct {
	mu       sync.RWMutex
	entries  [1000]RequestEntry
	sequence uint64
}

type RequestEntry struct {
	ID         uint64    `json:"id"`
	At         time.Time `json:"at"`
	Method     string    `json:"method"`
	Route      string    `json:"route"`
	Status     int       `json:"status"`
	DurationMS int64     `json:"durationMs"`
}

type RequestPage struct {
	Items      []RequestEntry `json:"items"`
	NextBefore uint64         `json:"nextBefore,omitempty"`
	OldestID   uint64         `json:"oldestId"`
}

func (l *RequestLog) Record(method, route string, status int, elapsed time.Duration) {
	if l == nil {
		return
	}
	// Bound even synthetic callers; HTTP routing supplies the registered pattern.
	if len(method) > 16 {
		method = "OTHER"
	}
	if len(route) > 256 {
		route = "unmatched"
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.sequence++
	l.entries[(l.sequence-1)%1000] = RequestEntry{l.sequence, time.Now().UTC(), method, route, status, elapsed.Milliseconds()}
}

// Page scans at most the 1,000 retained entries and returns at most 50, newest
// first. A cursor cannot pin expired entries; OldestID exposes that boundary.
func (l *RequestLog) Page(before uint64, statusClass int) RequestPage {
	l.mu.RLock()
	defer l.mu.RUnlock()
	page := RequestPage{Items: make([]RequestEntry, 0, 50)}
	if l.sequence == 0 {
		return page
	}
	oldest := uint64(1)
	if l.sequence > 1000 {
		oldest = l.sequence - 999
	}
	page.OldestID = oldest
	for id := l.sequence; id >= oldest; id-- {
		if before != 0 && id >= before {
			continue
		}
		entry := l.entries[(id-1)%1000]
		if statusClass != 0 && entry.Status/100 != statusClass {
			continue
		}
		if len(page.Items) == 50 {
			page.NextBefore = page.Items[49].ID
			break
		}
		page.Items = append(page.Items, entry)
	}
	return page
}
