package observability

import (
	"sync"
	"testing"
	"time"
)

func TestRequestLogBoundedPagesAndFilter(t *testing.T) {
	var log RequestLog
	for i := 0; i < 1200; i++ {
		status := 200
		if i%2 == 0 {
			status = 500
		}
		log.Record("GET", "/records/{id}", status, time.Millisecond)
	}
	first := log.Page(0, 5)
	if len(first.Items) != 50 || first.Items[0].ID != 1199 || first.OldestID != 201 || first.NextBefore != 1101 {
		t.Fatalf("first page: %#v", first)
	}
	next := log.Page(first.NextBefore, 5)
	if len(next.Items) != 50 || next.Items[0].ID != 1099 {
		t.Fatalf("next page: %#v", next)
	}
	if len(log.Page(201, 0).Items) != 0 {
		t.Fatal("expired entries returned")
	}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Go(func() {
			for j := 0; j < 100; j++ {
				log.Record("POST", "/records", 201, time.Millisecond)
				log.Page(0, 0)
			}
		})
	}
	wg.Wait()
	if got := log.Page(0, 0); len(got.Items) != 50 || got.Items[0].ID != 2000 {
		t.Fatalf("concurrent page: %#v", got)
	}
}
