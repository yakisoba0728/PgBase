//go:build integration && load

package pgbase

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestMixedHTTPPoolQualification measures a bounded closed-loop workload on a
// disposable server/database. Each ten-request cycle contains create, update,
// delete, get and six filtered relation-page reads, including a continuation.
// Mutation records belong to each worker; this is not a hot-row contention or
// open-loop capacity test. Memory totals include the in-process HTTP client.
func TestMixedHTTPPoolQualification(t *testing.T) {
	cycles := 100
	if raw := os.Getenv("PGBASE_MIXED_CYCLES"); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value < 32 || value > 10000 {
			t.Fatal("PGBASE_MIXED_CYCLES must be in [32, 10000]")
		}
		cycles = value
	}
	fixture := newPublicUsersAppOptions(t, true, func(options *Options) {
		parsed, err := url.Parse(options.DatabaseURL)
		if err != nil {
			t.Fatal(err)
		}
		query := parsed.Query()
		query.Set("pool_max_conns", "4")
		parsed.RawQuery = query.Encode()
		options.DatabaseURL = parsed.String()
	})
	admin, csrf := setupPublicUsersAdministrator(t, fixture)
	apply := func(name string, fields []map[string]any) {
		t.Helper()
		change := map[string]any{"operation": "create", "name": name, "fields": fields}
		planAndApplySchemaChange(t, fixture, admin, csrf, change, http.StatusCreated, nil)
		assertPublicUsersStatus(t, publicUsersRequest(t, fixture, http.MethodPut,
			"/api/admin/collections/"+name+"/user-access", map[string]any{"enabled": true}, "", admin,
			map[string]string{"X-PgBase-CSRF": csrf}), http.StatusOK)
	}
	apply("mixed_groups", []map[string]any{{"name": "title", "type": "text", "required": true}})
	apply("mixed_notes", []map[string]any{
		{"name": "title", "type": "text", "required": true, "indexed": true},
		{"name": "rank", "type": "integer", "required": true, "indexed": true},
		{"name": "group", "type": "relation", "relation": map[string]any{
			"targetCollection": "mixed_groups", "cardinality": "single", "minSelect": 0,
			"maxSelect": 1, "onDelete": "restrict",
		}},
	})
	const recordsPerOwner = 128
	type ownerFixture struct {
		token, prefix, group, seededID string
	}
	owners := make([]ownerFixture, 2)
	for index := range owners {
		prefix := fmt.Sprintf("owner-%d-", index)
		user := registerPublicUser(t, fixture, prefix+"load@example.test", publicUserOnePass)
		group := publicUsersRequest(t, fixture, http.MethodPost, "/api/v1/user/collections/mixed_groups/records",
			map[string]any{"title": prefix + "group"}, user.Token, nil, nil)
		assertPublicUsersStatus(t, group, http.StatusCreated)
		var created map[string]any
		decodePublicUsersJSON(t, group, &created)
		owners[index] = ownerFixture{token: user.Token, prefix: prefix, group: created["id"].(string)}
		for ordinal := range recordsPerOwner {
			record := publicUsersRequest(t, fixture, http.MethodPost, "/api/v1/user/collections/mixed_notes/records",
				map[string]any{"title": fmt.Sprintf("%snote-%03d", prefix, ordinal), "rank": ordinal,
					"group": owners[index].group}, user.Token, nil, nil)
			assertPublicUsersStatus(t, record, http.StatusCreated)
			if ordinal == 0 {
				decodePublicUsersJSON(t, record, &created)
				owners[index].seededID = created["id"].(string)
			}
		}
	}
	base := "/api/v1/user/collections/mixed_notes/records"
	query := url.Values{"limit": {"50"}, "filter": {"rank < 1000"}, "sort": {"rank,title"}, "expand": {"group"}}
	pagePath := base + "?" + query.Encode()
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.MaxIdleConns, transport.MaxIdleConnsPerHost = 64, 64
	t.Cleanup(transport.CloseIdleConnections)
	client := &http.Client{Transport: transport, Timeout: 10 * time.Second}
	checkIsolation := func(t *testing.T) {
		t.Helper()
		for index, owner := range owners {
			other := owners[1-index]
			assertPublicUsersStatus(t, publicUsersRequest(t, fixture, http.MethodGet,
				base+"/"+other.seededID, nil, owner.token, nil, nil), http.StatusNotFound)
			response := publicUsersRequest(t, fixture, http.MethodGet,
				base+"?limit=200&total=true", nil, owner.token, nil, nil)
			assertPublicUsersStatus(t, response, http.StatusOK)
			var page struct {
				Items      []map[string]any `json:"items"`
				TotalItems int              `json:"totalItems"`
			}
			decodePublicUsersJSON(t, response, &page)
			if len(page.Items) != recordsPerOwner || page.TotalItems != recordsPerOwner {
				t.Fatalf("owner count changed: items=%d total=%d", len(page.Items), page.TotalItems)
			}
			for _, item := range page.Items {
				if title, _ := item["title"].(string); !strings.HasPrefix(title, owner.prefix) {
					t.Fatal("another owner's record was returned")
				}
			}
		}
		var count int
		if err := fixture.app.pool.QueryRow(t.Context(), `SELECT count(*) FROM app.mixed_notes`).Scan(&count); err != nil || count != 2*recordsPerOwner {
			t.Fatalf("post-mutation physical row count=%d err=%v", count, err)
		}
	}
	checkIsolation(t)
	t.Logf("model=closed-loop mixed-http rows=%d owners=2 fields=3 relations=single pool=4 cycles=%d go=%s GOMAXPROCS=%d", 2*recordsPerOwner, cycles, runtime.Version(), runtime.GOMAXPROCS(0))
	for _, stage := range []struct {
		name    string
		workers int
	}{{"baseline", 1}, {"concurrent", 8}, {"saturated", 32}, {"recovery", 4}} {
		t.Run(stage.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Minute)
			defer cancel()
			type sample struct {
				kind    string
				latency time.Duration
				err     error
			}
			samples := make(chan sample, cycles*10)
			var next atomic.Int64
			var workers sync.WaitGroup
			start := make(chan struct{})
			for worker := range stage.workers {
				workers.Add(1)
				go func() {
					defer workers.Done()
					owner := owners[worker%len(owners)]
					<-start
					for {
						cycle := int(next.Add(1) - 1)
						if cycle >= cycles || ctx.Err() != nil {
							return
						}
						var recordID, etag, nextCursor string
						seen := map[string]bool{}
						for operation := range 10 {
							method, path, kind, want := http.MethodGet, pagePath, "page", http.StatusOK
							var body map[string]any
							headers := map[string]string{}
							switch operation {
							case 0:
								method, path, kind, want = http.MethodPost, base, "create", http.StatusCreated
								body = map[string]any{"title": owner.prefix + "transient", "rank": 1000000, "group": owner.group}
								headers["Idempotency-Key"] = fmt.Sprintf("mixed-%s-%d-%d-create", stage.name, worker, cycle)
							case 1:
								method, path, kind = http.MethodPatch, base+"/"+recordID, "update"
								body = map[string]any{"title": owner.prefix + "updated"}
								headers["If-Match"] = etag
							case 2:
								method, path, kind, want = http.MethodDelete, base+"/"+recordID, "delete", http.StatusNoContent
								headers["If-Match"] = etag
							case 3:
								path, kind = base+"/"+owner.seededID, "get"
							case 5:
								path, kind = pagePath+"&cursor="+url.QueryEscape(nextCursor), "cursor"
							}
							began := time.Now()
							status, responseHeaders, raw, err := mixedLoadRequest(ctx, client, fixture.server.URL, method, path, owner.token, body, headers)
							elapsed := time.Since(began)
							if err == nil && status != want {
								err = fmt.Errorf("unexpected HTTP status=%d want=%d", status, want)
							}
							if err == nil && kind != "delete" {
								var decoded map[string]any
								err = json.Unmarshal(raw, &decoded)
								if err == nil {
									switch kind {
									case "create", "update":
										newID, _ := decoded["id"].(string)
										newETag := responseHeaders.Get("ETag")
										if newID == "" || newETag == "" || (kind == "update" && (newID != recordID || newETag == etag || decoded["title"] != owner.prefix+"updated")) {
											err = fmt.Errorf("mutation response violated identity/version/value contract")
										}
										recordID, etag = newID, newETag
									case "get":
										if decoded["id"] != owner.seededID {
											err = fmt.Errorf("get returned a different record")
										}
									case "page", "cursor":
										items, ok := decoded["items"].([]any)
										if !ok || len(items) != 50 {
											err = fmt.Errorf("page length=%d want=50", len(items))
											break
										}
										for _, value := range items {
											item, _ := value.(map[string]any)
											id, _ := item["id"].(string)
											title, _ := item["title"].(string)
											expand, _ := item["expand"].(map[string]any)
											group, _ := expand["group"].(map[string]any)
											if id == "" || !strings.HasPrefix(title, owner.prefix) || group["id"] != owner.group || (kind == "cursor" && seen[id]) {
												err = fmt.Errorf("page violated owner/relation/continuation contract")
												break
											}
											if operation == 4 {
												seen[id] = true
											}
										}
										if operation == 4 {
											nextCursor, _ = decoded["nextCursor"].(string)
											if nextCursor == "" {
												err = fmt.Errorf("first page omitted continuation")
											}
										}
									}
								}
							}
							samples <- sample{kind: kind, latency: elapsed, err: err}
							if err != nil {
								return
							}
						}
					}
				}()
			}
			var before, after runtime.MemStats
			runtime.ReadMemStats(&before)
			poolBefore := fixture.app.pool.Stats()
			began := time.Now()
			close(start)
			workers.Wait()
			elapsed := time.Since(began)
			poolAfter := fixture.app.pool.Stats()
			runtime.ReadMemStats(&after)
			close(samples)
			latencies := map[string][]time.Duration{"all": {}}
			failures := 0
			for result := range samples {
				latencies["all"] = append(latencies["all"], result.latency)
				latencies[result.kind] = append(latencies[result.kind], result.latency)
				if result.err != nil {
					failures++
					t.Errorf("%s: %v", result.kind, result.err)
				}
			}
			requests := len(latencies["all"])
			if requests == 0 {
				t.Fatal("no completed HTTP samples")
			}
			for _, kind := range []string{"all", "create", "update", "delete", "get", "page", "cursor"} {
				values := latencies[kind]
				if len(values) == 0 {
					continue
				}
				slices.Sort(values)
				quantile := func(percent int) time.Duration { return values[(len(values)*percent+99)/100-1] }
				t.Logf("kind=%s requests=%d p50=%s p95=%s p99=%s", kind, len(values), quantile(50), quantile(95), quantile(99))
			}
			t.Logf("workers=%d elapsed=%s requests=%d rps=%.2f unexpected_errors=%d error_rate=%.6f allocated_bytes_per_request=%.1f allocations_per_request=%.1f heap_before=%d heap_after=%d gc_cycles=%d gc_pause=%s pool_max=%d pool_wait_count=%d pool_wait=%s pool_acquire=%s pool_canceled=%d", stage.workers, elapsed, requests,
				float64(requests)/elapsed.Seconds(), failures, float64(failures)/float64(requests),
				float64(after.TotalAlloc-before.TotalAlloc)/float64(requests), float64(after.Mallocs-before.Mallocs)/float64(requests),
				before.HeapAlloc, after.HeapAlloc, after.NumGC-before.NumGC, time.Duration(after.PauseTotalNs-before.PauseTotalNs),
				poolAfter.MaxConns, poolAfter.EmptyAcquireCount-poolBefore.EmptyAcquireCount,
				poolAfter.EmptyAcquireWaitTime-poolBefore.EmptyAcquireWaitTime,
				poolAfter.AcquireDuration-poolBefore.AcquireDuration, poolAfter.CanceledAcquireCount-poolBefore.CanceledAcquireCount)
			if failures != 0 || requests != cycles*10 || ctx.Err() != nil {
				t.Fatalf("incomplete workload: requests=%d want=%d failures=%d context=%v", requests, cycles*10, failures, ctx.Err())
			}
			checkIsolation(t)
		})
	}
}

func mixedLoadRequest(ctx context.Context, client *http.Client, origin, method, path, bearer string, body map[string]any, headers map[string]string) (int, http.Header, []byte, error) {
	var encoded []byte
	var err error
	if body != nil {
		encoded, err = json.Marshal(body)
		if err != nil {
			return 0, nil, nil, err
		}
	}
	request, err := http.NewRequestWithContext(ctx, method, origin+path, bytes.NewReader(encoded))
	if err != nil {
		return 0, nil, nil, err
	}
	request.Host = "pgbase.test"
	request.Header.Set("Authorization", "Bearer "+bearer)
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	if method != http.MethodGet {
		request.Header.Set("Origin", publicUsersOrigin)
	}
	for name, value := range headers {
		request.Header.Set(name, value)
	}
	response, err := client.Do(request)
	if err != nil {
		return 0, nil, nil, err
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(response.Body, (1<<20)+1))
	if err == nil && len(raw) > 1<<20 {
		err = fmt.Errorf("load response exceeded 1 MiB fixture limit")
	}
	return response.StatusCode, response.Header, raw, err
}
