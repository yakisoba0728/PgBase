//go:build integration && load

package pgbase

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"runtime/metrics"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestOpenLoopHTTPQualification uses fixed scheduled arrivals, independent
// mutations and a bounded queue. End-to-end latency starts at the intended
// arrival, including generator lag and queueing; it does not hide an overloaded
// server by waiting for a response before scheduling the next request.
// All state is private to the disposable test database. Resource counters cover
// both the embedded server and the in-process client, not isolated server RSS.
func TestOpenLoopHTTPQualification(t *testing.T) {
	rate := openLoopSetting(t, "PGBASE_OPEN_LOOP_RPS", 200, 20, 1000)
	seconds := openLoopSetting(t, "PGBASE_OPEN_LOOP_SECONDS", 60, 5, 600)
	count := rate * seconds
	if count%20 != 0 {
		t.Fatal("arrival count must be divisible by 20 for the balanced owner/workload mix")
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
		planAndApplySchemaChange(t, fixture, admin, csrf, map[string]any{"operation": "create", "name": name, "fields": fields}, http.StatusCreated, nil)
		assertPublicUsersStatus(t, publicUsersRequest(t, fixture, http.MethodPut,
			"/api/admin/collections/"+name+"/user-access", map[string]any{"enabled": true}, "", admin,
			map[string]string{"X-PgBase-CSRF": csrf}), http.StatusOK)
	}
	apply("open_groups", []map[string]any{{"name": "title", "type": "text", "required": true}})
	apply("open_notes", []map[string]any{
		{"name": "title", "type": "text", "required": true, "indexed": true},
		{"name": "rank", "type": "integer", "required": true, "indexed": true},
		{"name": "group", "type": "relation", "relation": map[string]any{"targetCollection": "open_groups", "cardinality": "single", "minSelect": 0, "maxSelect": 1, "onDelete": "restrict"}},
	})
	const base = "/api/v1/user/collections/open_notes/records"
	type owner struct{ token, prefix, group, first string }
	owners := make([]owner, 2)
	for index := range owners {
		prefix := fmt.Sprintf("owner-%d-", index)
		user := registerPublicUser(t, fixture, prefix+"open@example.test", publicUserOnePass)
		response := publicUsersRequest(t, fixture, http.MethodPost, "/api/v1/user/collections/open_groups/records", map[string]any{"title": prefix + "group"}, user.Token, nil, nil)
		assertPublicUsersStatus(t, response, http.StatusCreated)
		var record map[string]any
		decodePublicUsersJSON(t, response, &record)
		owners[index] = owner{token: user.Token, prefix: prefix, group: record["id"].(string)}
		for ordinal := range 128 {
			response = publicUsersRequest(t, fixture, http.MethodPost, base, map[string]any{"title": fmt.Sprintf("%snote-%03d", prefix, ordinal), "rank": ordinal, "group": owners[index].group}, user.Token, nil, nil)
			assertPublicUsersStatus(t, response, http.StatusCreated)
			if ordinal == 0 {
				decodePublicUsersJSON(t, response, &record)
				owners[index].first = record["id"].(string)
			}
		}
	}
	type arrival struct {
		index, owner, status         int
		kind, method, path, id, etag string
		body                         map[string]any
		headers                      map[string]string
	}
	arrivals := make([]arrival, count)
	pagePath := base + "?" + url.Values{"limit": {"50"}, "filter": {"rank < 1000"}, "sort": {"rank,title"}, "expand": {"group"}}.Encode()
	for i := range arrivals {
		ownerIndex := (i / 10) % len(owners)
		identity := owners[ownerIndex]
		request := arrival{index: i, owner: ownerIndex, status: http.StatusOK, kind: "page", method: http.MethodGet, path: pagePath}
		switch i % 10 {
		case 0:
			request.kind, request.method, request.path, request.status = "create", http.MethodPost, base, http.StatusCreated
			request.body = map[string]any{"title": fmt.Sprintf("%screated-%06d", identity.prefix, i), "rank": 1000000, "group": identity.group}
			request.headers = map[string]string{"Idempotency-Key": fmt.Sprintf("open-loop-create-request-%08d", i)}
		case 1, 2:
			// Distinct targets remove inter-request dependencies. Every mutation is
			// still a real authenticated Record Operation with a version guard.
			response := publicUsersRequest(t, fixture, http.MethodPost, base, map[string]any{"title": fmt.Sprintf("%starget-%06d", identity.prefix, i), "rank": 1000000, "group": identity.group}, identity.token, nil, nil)
			assertPublicUsersStatus(t, response, http.StatusCreated)
			var record map[string]any
			decodePublicUsersJSON(t, response, &record)
			request.id, request.etag = record["id"].(string), response.Header().Get("ETag")
			if request.etag == "" {
				t.Fatal("seed mutation has no ETag")
			}
			request.path, request.headers = base+"/"+request.id, map[string]string{"If-Match": request.etag}
			if i%10 == 1 {
				request.kind, request.method = "update", http.MethodPatch
				request.body = map[string]any{"title": fmt.Sprintf("%supdated-%06d", identity.prefix, i)}
			} else {
				request.kind, request.method, request.status = "delete", http.MethodDelete, http.StatusNoContent
			}
		case 3:
			request.kind, request.path, request.id = "get", base+"/"+identity.first, identity.first
		}
		arrivals[i] = request
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.MaxIdleConns, transport.MaxIdleConnsPerHost = 64, 64
	t.Cleanup(transport.CloseIdleConnections)
	client := &http.Client{Transport: transport, Timeout: 10 * time.Second}
	validate := func(request arrival, status int, headers http.Header, raw []byte) error {
		if status != request.status {
			var failure struct {
				Code string `json:"code"`
			}
			_ = json.Unmarshal(raw, &failure)
			return fmt.Errorf("%s status=%d want=%d code=%s", request.kind, status, request.status, failure.Code)
		}
		if request.kind == "delete" {
			return nil
		}
		var record map[string]any
		if err := json.Unmarshal(raw, &record); err != nil {
			return err
		}
		identity := owners[request.owner]
		switch request.kind {
		case "create", "update":
			id, _ := record["id"].(string)
			if id == "" || headers.Get("ETag") == "" || record["title"] != request.body["title"] || record["rank"] != float64(1000000) || record["group"] != identity.group || (request.kind == "update" && (id != request.id || headers.Get("ETag") == request.etag)) {
				return fmt.Errorf("%s identity/version/value mismatch", request.kind)
			}
		case "get":
			if record["id"] != request.id || record["title"] != identity.prefix+"note-000" {
				return fmt.Errorf("point read identity/owner mismatch")
			}
		case "page":
			items, ok := record["items"].([]any)
			cursor, hasCursor := record["nextCursor"].(string)
			if !ok || len(items) != 50 || !hasCursor || cursor == "" {
				return fmt.Errorf("page shape mismatch")
			}
			seen := make(map[string]bool, 50)
			for rank, value := range items {
				item, _ := value.(map[string]any)
				id, _ := item["id"].(string)
				expand, _ := item["expand"].(map[string]any)
				group, _ := expand["group"].(map[string]any)
				if id == "" || seen[id] || item["rank"] != float64(rank) || item["title"] != fmt.Sprintf("%snote-%03d", identity.prefix, rank) || group["id"] != identity.group {
					return fmt.Errorf("page order/filter/owner/relation mismatch at %d", rank)
				}
				seen[id] = true
			}
		}
		return nil
	}
	// Warm application, pool and client connections without changing data.
	for i := range 100 {
		request := arrivals[(i/2*10+4)%count]
		status, headers, raw, err := mixedLoadRequest(t.Context(), client, fixture.server.URL, request.method, request.path, owners[request.owner].token, nil, nil)
		if err == nil {
			err = validate(request, status, headers, raw)
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	type observation struct {
		queue, service, total time.Duration
		status                int
		err                   error
	}
	observations := make([]observation, count)
	queue := make(chan arrival, 256)
	ctx, cancel := context.WithTimeout(t.Context(), time.Duration(seconds)*time.Second+30*time.Second)
	defer cancel()
	var workers sync.WaitGroup
	start := time.Now().Add(50 * time.Millisecond)
	interval := time.Second / time.Duration(rate)
	for range 32 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for request := range queue {
				scheduled := start.Add(time.Duration(request.index) * interval)
				began := time.Now()
				status, headers, raw, err := mixedLoadRequest(ctx, client, fixture.server.URL, request.method, request.path, owners[request.owner].token, request.body, request.headers)
				service := time.Since(began)
				if err == nil {
					err = validate(request, status, headers, raw)
				}
				observations[request.index] = observation{queue: began.Sub(scheduled), service: service, total: time.Since(scheduled), status: status, err: err}
			}
		}()
	}
	poolBefore := fixture.app.pool.Stats()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	cpu := []metrics.Sample{{Name: "/cpu/classes/total:cpu-seconds"}, {Name: "/cpu/classes/idle:cpu-seconds"}, {Name: "/cpu/classes/gc/total:cpu-seconds"}}
	metrics.Read(cpu)
	cpuBefore := []float64{cpu[0].Value.Float64(), cpu[1].Value.Float64(), cpu[2].Value.Float64()}
	dropped, enqueued, unscheduled := 0, 0, 0
	timer := time.NewTimer(time.Until(start))
	defer timer.Stop()
	var scheduleErr error
schedule:
	for _, request := range arrivals {
		scheduled := start.Add(time.Duration(request.index) * interval)
		if wait := time.Until(scheduled); wait > 0 {
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			timer.Reset(wait)
			select {
			case <-timer.C:
			case <-ctx.Done():
				scheduleErr = ctx.Err()
				for index := request.index; index < len(arrivals); index++ {
					observations[index].err = fmt.Errorf("arrival not scheduled: %w", scheduleErr)
					unscheduled++
				}
				break schedule
			}
		}
		select {
		case queue <- request:
			enqueued++
		default:
			dropped++
			observations[request.index].err = fmt.Errorf("arrival queue full")
		}
	}
	close(queue)
	workers.Wait()
	if scheduleErr != nil {
		t.Errorf("arrival scheduler: %v", scheduleErr)
	}
	elapsed := time.Since(start)
	runtime.ReadMemStats(&after)
	metrics.Read(cpu)
	poolAfter := fixture.app.pool.Stats()
	latencies, services, waits := make([]time.Duration, 0, count), make([]time.Duration, 0, count), make([]time.Duration, 0, count)
	failed, completed, attempted := 0, 0, 0
	for i, result := range observations {
		if result.err != nil {
			failed++
			if failed <= 5 {
				t.Logf("request=%d kind=%s error=%v", i, arrivals[i].kind, result.err)
			}
		}
		if result.status != 0 {
			completed++
		}
		// Include attempted requests that time out before HTTP headers.
		if result.service > 0 {
			attempted++
			latencies = append(latencies, result.total)
			services = append(services, result.service)
			waits = append(waits, result.queue)
		}
	}
	quantile := func(values []time.Duration, percentile int) time.Duration {
		if len(values) == 0 {
			return 0
		}
		slices.Sort(values)
		return values[(len(values)*percentile+99)/100-1]
	}
	for _, series := range []struct {
		name   string
		values []time.Duration
	}{{"scheduled_to_validated", latencies}, {"http_service", services}, {"generator_and_queue", waits}} {
		t.Logf("model=open-loop metric=%s samples=%d p50=%s p95=%s p99=%s", series.name, len(series.values), quantile(series.values, 50), quantile(series.values, 95), quantile(series.values, 99))
	}
	cpuTotal, cpuIdle := cpu[0].Value.Float64()-cpuBefore[0], cpu[1].Value.Float64()-cpuBefore[1]
	t.Logf("offered_rps=%d seconds=%d intended=%d enqueued=%d attempted=%d completed=%d dropped=%d unscheduled=%d errors=%d error_rate=%.6f achieved_rps=%.2f elapsed=%s pool_max=%d pool_waits=%d pool_wait=%s pool_canceled=%d bytes_per_request=%.1f allocs_per_request=%.1f gc_cycles=%d gc_pause=%s runtime_busy_cpu_seconds=%.6f runtime_gc_cpu_seconds=%.6f heap_before=%d heap_after=%d",
		rate, seconds, count, enqueued, attempted, completed, dropped, unscheduled, failed, float64(failed)/float64(count), float64(completed)/elapsed.Seconds(), elapsed, poolAfter.MaxConns, poolAfter.EmptyAcquireCount-poolBefore.EmptyAcquireCount, poolAfter.EmptyAcquireWaitTime-poolBefore.EmptyAcquireWaitTime, poolAfter.CanceledAcquireCount-poolBefore.CanceledAcquireCount, float64(after.TotalAlloc-before.TotalAlloc)/float64(count), float64(after.Mallocs-before.Mallocs)/float64(count), after.NumGC-before.NumGC, time.Duration(after.PauseTotalNs-before.PauseTotalNs), cpuTotal-cpuIdle, cpu[2].Value.Float64()-cpuBefore[2], before.HeapAlloc, after.HeapAlloc)
	if directory := os.Getenv("PGBASE_LOAD_EVIDENCE_DIR"); directory != "" {
		if err := os.MkdirAll(directory, 0700); err != nil {
			t.Fatal(err)
		}
		file, err := os.Create(filepath.Join(directory, "open-loop-samples.csv"))
		if err != nil {
			t.Fatal(err)
		}
		writer := csv.NewWriter(file)
		_ = writer.Write([]string{"index", "kind", "owner", "scheduled_offset_ns", "generator_queue_ns", "http_service_ns", "scheduled_to_validated_ns", "status", "error"})
		for i, result := range observations {
			message := ""
			if result.err != nil {
				message = result.err.Error()
			}
			_ = writer.Write([]string{strconv.Itoa(i), arrivals[i].kind, strconv.Itoa(arrivals[i].owner), strconv.FormatInt(int64(time.Duration(i)*interval), 10), strconv.FormatInt(int64(result.queue), 10), strconv.FormatInt(int64(result.service), 10), strconv.FormatInt(int64(result.total), 10), strconv.Itoa(result.status), message})
		}
		writer.Flush()
		writeErr, closeErr := writer.Error(), file.Close()
		if writeErr != nil {
			t.Fatal(writeErr)
		}
		if closeErr != nil {
			t.Fatal(closeErr)
		}
	}
	// Requests may have committed even if a response was lost. Check physical
	// state and each identity after draining, rather than trusting status totals.
	var rows, creates, updates int
	if err := fixture.app.pool.QueryRow(t.Context(), `SELECT count(*),count(*) FILTER(WHERE title LIKE '%created-%'),count(*) FILTER(WHERE title LIKE '%updated-%') FROM app.open_notes`).Scan(&rows, &creates, &updates); err != nil {
		t.Fatal(err)
	}
	if rows != 256+count/5 || creates != count/10 || updates != count/10 {
		t.Errorf("durable rows/create/update=%d/%d/%d want=%d/%d/%d", rows, creates, updates, 256+count/5, count/10, count/10)
	}
	for index, identity := range owners {
		assertPublicUsersStatus(t, publicUsersRequest(t, fixture, http.MethodGet, base+"/"+owners[1-index].first, nil, identity.token, nil, nil), http.StatusNotFound)
		seen, cursor := map[string]bool{}, ""
		ownerCreates, ownerUpdates := 0, 0
		for pageNumber := 0; pageNumber <= (128+count/10)/200+1; pageNumber++ {
			path := base + "?limit=200&total=true"
			if cursor != "" {
				path += "&cursor=" + url.QueryEscape(cursor)
			}
			response := publicUsersRequest(t, fixture, http.MethodGet, path, nil, identity.token, nil, nil)
			assertPublicUsersStatus(t, response, http.StatusOK)
			var page struct {
				Items       []map[string]any `json:"items"`
				Total       int              `json:"totalItems"`
				TotalCapped bool             `json:"totalCapped"`
				Next        string           `json:"nextCursor"`
			}
			decodePublicUsersJSON(t, response, &page)
			// The public total is deliberately capped at 10,000. Verify that
			// contract while still traversing and counting every actual record
			// below; the count hint is never the data-integrity oracle.
			wantTotal := 128 + count/10
			if page.Total != min(wantTotal, 10000) || page.TotalCapped != (wantTotal > 10000) {
				t.Errorf("owner=%d total=%d capped=%t want=%d/%t", index, page.Total, page.TotalCapped, min(wantTotal, 10000), wantTotal > 10000)
			}
			for _, record := range page.Items {
				title, _ := record["title"].(string)
				id, _ := record["id"].(string)
				if id == "" || seen[id] || !strings.HasPrefix(title, identity.prefix) || record["group"] != identity.group {
					t.Error("owner/relation/unique identity mismatch after load")
				}
				seen[id] = true
				if strings.HasPrefix(title, identity.prefix+"created-") {
					ownerCreates++
				}
				if strings.HasPrefix(title, identity.prefix+"updated-") {
					ownerUpdates++
				}
			}
			cursor = page.Next
			if cursor == "" {
				break
			}
		}
		if cursor != "" || len(seen) != 128+count/10 || ownerCreates != count/20 || ownerUpdates != count/20 {
			t.Errorf("owner=%d post-load rows/created/updated=%d/%d/%d continuation=%t", index, len(seen), ownerCreates, ownerUpdates, cursor != "")
		}
	}
	if completed != count || dropped != 0 || failed != 0 {
		t.Errorf("incomplete/error workload: completed=%d/%d dropped=%d failed=%d", completed, count, dropped, failed)
	}
	if p99 := quantile(latencies, 99); p99 > 250*time.Millisecond {
		t.Errorf("preregistered local fixture p99 exceeded250ms: %s", p99)
	}
}

func openLoopSetting(t *testing.T, name string, fallback, minimum, maximum int) int {
	t.Helper()
	if raw := os.Getenv(name); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value < minimum || value > maximum {
			t.Fatalf("%s must be in [%d,%d]", name, minimum, maximum)
		}
		return value
	}
	return fallback
}
