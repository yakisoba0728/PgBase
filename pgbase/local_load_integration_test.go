//go:build integration && load

package pgbase

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"sync"
	"testing"
	"time"
)

// TestLocalLoadStages records a repeatable short local workload. It is not an
// operating SLO, a multi-hour soak, or a substitute for a production workload.
func TestLocalLoadStages(t *testing.T) {
	app := newPublicUsersApp(t, true)
	admin, csrf := setupPublicUsersAdministrator(t, app)
	createPublicUsersCollection(t, app, admin, csrf, "load_notes")
	assertPublicUsersStatus(t, publicUsersRequest(t, app, "PUT", "/api/admin/collections/load_notes/user-access", map[string]any{"enabled": true}, "", admin, map[string]string{"X-PgBase-CSRF": csrf}), 200)
	user := registerPublicUser(t, app, "load@example.test", publicUserOnePass)
	for i := 0; i < 100; i++ {
		assertPublicUsersStatus(t, publicUsersRequest(t, app, "POST", "/api/v1/user/collections/load_notes/records", map[string]any{"title": fmt.Sprintf("note-%03d", i)}, user.Token, nil, nil), 201)
	}
	client := &http.Client{Timeout: 10 * time.Second}
	for _, stage := range []struct {
		name     string
		workers  int
		duration time.Duration
	}{{"baseline", 1, 5 * time.Second}, {"short_soak", 4, 15 * time.Second}, {"spike", 16, 5 * time.Second}, {"recovery", 4, 5 * time.Second}} {
		ctx, cancel := context.WithTimeout(context.Background(), stage.duration)
		var mu sync.Mutex
		var wg sync.WaitGroup
		var samples []time.Duration
		failures := 0
		started := time.Now()
		for worker := 0; worker < stage.workers; worker++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for ctx.Err() == nil {
					request, err := http.NewRequest("GET", app.server.URL+"/api/v1/user/collections/load_notes/records?limit=50", nil)
					if err != nil {
						return
					}
					request.Host = "pgbase.test"
					request.Header.Set("Authorization", "Bearer "+user.Token)
					begin := time.Now()
					response, err := client.Do(request)
					failed := err != nil
					if err == nil {
						var page struct {
							Items []map[string]any `json:"items"`
						}
						raw, readErr := io.ReadAll(io.LimitReader(response.Body, 1<<20))
						response.Body.Close()
						failed = response.StatusCode != 200 || readErr != nil || json.Unmarshal(raw, &page) != nil || len(page.Items) != 50
					}
					mu.Lock()
					samples = append(samples, time.Since(begin))
					if failed {
						failures++
					}
					mu.Unlock()
				}
			}()
		}
		wg.Wait()
		cancel()
		elapsed := time.Since(started)
		sort.Slice(samples, func(i, j int) bool { return samples[i] < samples[j] })
		if len(samples) == 0 {
			t.Fatal("no load samples")
		}
		t.Logf("stage=%s workers=%d elapsed=%.2fs requests=%d rps=%.1f failures=%d p95=%s p99=%s", stage.name, stage.workers, elapsed.Seconds(), len(samples), float64(len(samples))/elapsed.Seconds(), failures, samples[(len(samples)-1)*95/100], samples[(len(samples)-1)*99/100])
		if failures != 0 {
			t.Fatalf("stage %s failed %d requests", stage.name, failures)
		}
	}
}
