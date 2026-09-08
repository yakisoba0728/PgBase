package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestSetupStatusCacheCoalescesConcurrentProbes(t *testing.T) {
	cache := newSetupStatusCache()
	cache.now = func() time.Time { return time.Unix(1, 0) }
	var calls atomic.Int32
	entered := make(chan struct{})
	release := make(chan struct{})
	probe := func(context.Context) (bool, error) {
		if calls.Add(1) == 1 {
			close(entered)
		}
		<-release
		return true, nil
	}

	const workers = 32
	start := make(chan struct{})
	errorsByWorker := make(chan error, workers)
	var workersDone sync.WaitGroup
	workersDone.Add(workers)
	for range workers {
		go func() {
			defer workersDone.Done()
			<-start
			required, err := cache.get(context.Background(), probe)
			if err != nil {
				errorsByWorker <- err
				return
			}
			if !required {
				errorsByWorker <- errors.New("setup was not reported as required")
			}
		}()
	}
	close(start)
	<-entered
	close(release)
	workersDone.Wait()
	close(errorsByWorker)
	for err := range errorsByWorker {
		t.Error(err)
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("SetupRequired calls = %d, want 1", got)
	}
}

func TestSetupStatusCacheCachesErrorsOnlyUntilTheBoundedTTL(t *testing.T) {
	now := time.Unix(10, 0)
	cache := newSetupStatusCache()
	cache.now = func() time.Time { return now }
	sentinel := errors.New("database unavailable")
	calls := 0
	probe := func(context.Context) (bool, error) {
		calls++
		if calls == 1 {
			return true, sentinel
		}
		return false, nil
	}

	for range 2 {
		required, err := cache.get(context.Background(), probe)
		if !required || !errors.Is(err, sentinel) {
			t.Fatalf("cached result = (%t, %v), want (true, sentinel)", required, err)
		}
	}
	if calls != 1 {
		t.Fatalf("calls inside TTL = %d, want 1", calls)
	}

	now = now.Add(publicProbeCacheTTL)
	required, err := cache.get(context.Background(), probe)
	if required || err != nil {
		t.Fatalf("refreshed result = (%t, %v), want (false, nil)", required, err)
	}
	if calls != 2 {
		t.Fatalf("calls after TTL = %d, want 2", calls)
	}
}

func TestSetupStatusCacheDoesNotPublishRequestCancellation(t *testing.T) {
	cache := newSetupStatusCache()
	var calls atomic.Int32
	entered := make(chan struct{})
	release := make(chan struct{})
	probe := func(context.Context) (bool, error) {
		calls.Add(1)
		close(entered)
		<-release
		return false, nil
	}

	requestContext, cancelRequest := context.WithCancel(context.Background())
	firstDone := make(chan error, 1)
	go func() {
		_, err := cache.get(requestContext, probe)
		firstDone <- err
	}()
	<-entered
	cancelRequest()

	secondDone := make(chan error, 1)
	go func() {
		required, err := cache.get(context.Background(), probe)
		if required && err == nil {
			err = errors.New("setup unexpectedly required")
		}
		secondDone <- err
	}()
	close(release)
	if err := <-firstDone; !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled caller error = %v, want context.Canceled", err)
	}
	if err := <-secondDone; err != nil {
		t.Fatal(err)
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("probe calls = %d, want the surviving shared probe only", got)
	}
}

func TestSetupStatusCacheInvalidationDiscardsAnOlderInFlightProbe(t *testing.T) {
	cache := newSetupStatusCache()
	var calls atomic.Int32
	entered := make(chan struct{})
	release := make(chan struct{})
	probe := func(context.Context) (bool, error) {
		if calls.Add(1) == 1 {
			close(entered)
			<-release
			return true, nil
		}
		return false, nil
	}

	done := make(chan bool, 1)
	go func() {
		required, err := cache.get(context.Background(), probe)
		done <- err == nil && required
	}()
	<-entered
	cache.invalidate()
	close(release)
	if stale := <-done; stale {
		t.Fatal("an in-flight pre-invalidation setup result was published")
	}
	if got := calls.Load(); got != 2 {
		t.Fatalf("probe calls = %d, want a fresh probe after invalidation", got)
	}
}

type panicOnceStatusAuth struct {
	*fakeAdminAuth
	calls atomic.Int32
}

func (a *panicOnceStatusAuth) SetupRequired(context.Context) (bool, error) {
	if a.calls.Add(1) == 1 {
		panic("setup status probe panic")
	}
	return false, nil
}

func TestSetupStatusProbePanicUsesRouterRecoveryAndDoesNotWedgeCache(t *testing.T) {
	auth := &panicOnceStatusAuth{fakeAdminAuth: &fakeAdminAuth{}}
	handler := mustRouter(t, testDependencies(auth, mustURL("https://pgbase.example")))

	first := httptest.NewRecorder()
	handler.ServeHTTP(first, httptest.NewRequest(http.MethodGet, "/api/admin/status", nil))
	if first.Code != http.StatusInternalServerError {
		t.Fatalf("panicking status probe = %d, want %d; body=%s", first.Code, http.StatusInternalServerError, first.Body.String())
	}
	if strings.Contains(first.Body.String(), "setup status probe panic") {
		t.Fatalf("panic response leaked panic value: %s", first.Body.String())
	}

	second := httptest.NewRecorder()
	handler.ServeHTTP(second, httptest.NewRequest(http.MethodGet, "/api/admin/status", nil))
	if second.Code != http.StatusOK {
		t.Fatalf("status after panic = %d, want %d; body=%s", second.Code, http.StatusOK, second.Body.String())
	}
	if got := auth.calls.Load(); got != 2 {
		t.Fatalf("SetupRequired calls = %d, want 2", got)
	}
}

func TestSuccessfulSetupInvalidatesThePublicStatusCache(t *testing.T) {
	auth := &fakeAdminAuth{setupRequired: true, setupSession: validSession()}
	handler := mustRouter(t, testDependencies(auth, mustURL("https://pgbase.example")))

	status := func() bool {
		request := httptest.NewRequest(http.MethodGet, "/api/admin/status", nil)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusOK {
			t.Fatalf("status endpoint = %d body=%s", response.Code, response.Body.String())
		}
		var body struct {
			SetupRequired bool `json:"setupRequired"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		return body.SetupRequired
	}

	if !status() {
		t.Fatal("initial status did not report setup required")
	}
	auth.setupRequired = false
	request := httptest.NewRequest(http.MethodPost, "/api/admin/setup",
		strings.NewReader(`{"setupToken":"secret","email":"admin@example.com","password":"correct horse battery staple"}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Origin", "https://pgbase.example")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusCreated {
		t.Fatalf("setup = %d body=%s", response.Code, response.Body.String())
	}
	if status() {
		t.Fatal("post-setup status returned the cached first-run result")
	}
	if auth.setupRequiredCalls != 2 {
		t.Fatalf("SetupRequired calls = %d, want one before and one after setup", auth.setupRequiredCalls)
	}
}
