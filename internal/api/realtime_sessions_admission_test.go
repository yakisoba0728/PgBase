package api

import (
	"bufio"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/yakisoba0728/PgBase/internal/identity"
	"github.com/yakisoba0728/PgBase/internal/userauth"
)

// admitRealtimeForTest reserves a slot exactly as connect does, using the
// request's resolved principal and client network bucket.
func admitRealtimeForTest(hub *realtimeHub, request *http.Request) (*realtimeClient, bool) {
	address, _ := clientIP(request, hub.dependencies.trustedProxies())
	bucket, _ := clientAddress(request, hub.dependencies.trustedProxies())
	client := &realtimeClient{id: uuid.NewString(), address: address, principal: realtimeSubject(request)}
	hub.mu.Lock()
	defer hub.mu.Unlock()
	return client, hub.admitLocked(client, bucket)
}

func anonymousRealtimeRequest(remoteAddr string) *http.Request {
	request := httptest.NewRequest(http.MethodGet, "/api/realtime", nil)
	request.RemoteAddr = remoteAddr
	return request
}

func userRealtimeRequest(remoteAddr string, id uuid.UUID) *http.Request {
	request := anonymousRealtimeRequest(remoteAddr)
	return request.WithContext(context.WithValue(request.Context(), userPrincipalKey{}, authenticatedUser{principal: userauth.Principal{User: userauth.User{ID: id}}, token: "test"}))
}

func serviceRealtimeRequest(remoteAddr string, id uuid.UUID) *http.Request {
	request := anonymousRealtimeRequest(remoteAddr)
	return request.WithContext(context.WithValue(request.Context(), publicRecordPrincipalKey{}, identity.Subject{Kind: identity.Service, ID: id}))
}

func requireRealtimeLimit(t *testing.T, hub *realtimeHub, request *http.Request) {
	t.Helper()
	before := len(hub.clients)
	response := httptest.NewRecorder()
	hub.connect(response, request)
	if response.Code != http.StatusTooManyRequests || !strings.Contains(response.Body.String(), `"realtime_limit"`) {
		t.Fatalf("connect status=%d body=%s, want 429 realtime_limit", response.Code, response.Body.String())
	}
	if len(hub.clients) != before {
		t.Fatalf("refused connection changed admitted clients from %d to %d", before, len(hub.clients))
	}
}

func requireRealtimeCountersEmpty(t *testing.T, hub *realtimeHub) {
	t.Helper()
	hub.mu.RLock()
	defer hub.mu.RUnlock()
	if len(hub.clients) != 0 || len(hub.userClients) != 0 || len(hub.serviceClients) != 0 || hub.anonymousClients != 0 || len(hub.addressClients) != 0 {
		t.Fatalf("admission leaked: clients=%d users=%v services=%v anonymous=%d addresses=%v", len(hub.clients), hub.userClients, hub.serviceClients, hub.anonymousClients, hub.addressClients)
	}
}

func TestRealtimeAnonymousConnectionsCannotUseAuthenticatedReserve(t *testing.T) {
	hub := newRealtimeHub(Dependencies{Realtime: &sessionRealtimeStub{}})
	for network := range maximumAnonymousRealtimeConnections / maximumAnonymousRealtimeConnectionsPerClient {
		for range maximumAnonymousRealtimeConnectionsPerClient {
			if _, ok := admitRealtimeForTest(hub, anonymousRealtimeRequest(fmt.Sprintf("198.51.100.%d:1", network+1))); !ok {
				t.Fatalf("anonymous connection refused below the anonymous share (network %d)", network)
			}
		}
	}
	if hub.anonymousClients != 96 || len(hub.clients) != 96 {
		t.Fatalf("anonymous=%d clients=%d, want 96", hub.anonymousClients, len(hub.clients))
	}
	// A fresh network still cannot take one of the 32 reserved slots.
	requireRealtimeLimit(t, hub, anonymousRealtimeRequest("203.0.113.200:1"))

	if _, ok := admitRealtimeForTest(hub, userRealtimeRequest("203.0.113.1:1", uuid.New())); !ok {
		t.Fatal("user refused while authenticated slots were reserved")
	}
	if _, ok := admitRealtimeForTest(hub, serviceRealtimeRequest("203.0.113.2:1", uuid.New())); !ok {
		t.Fatal("service token refused while authenticated slots were reserved")
	}
	admin := &realtimeClient{id: uuid.NewString(), principal: identity.Subject{Kind: identity.Administrator, ID: uuid.New()}}
	hub.mu.Lock()
	adminAdmitted := hub.admitLocked(admin, "203.0.113.3")
	hub.mu.Unlock()
	if !adminAdmitted {
		t.Fatal("administrator refused while authenticated slots were reserved")
	}
	// Authenticated principals still share the whole process limit.
	for len(hub.clients) < maximumRealtimeConnections {
		if _, ok := admitRealtimeForTest(hub, userRealtimeRequest("203.0.113.4:1", uuid.New())); !ok {
			t.Fatalf("authenticated connection refused at %d clients", len(hub.clients))
		}
	}
	requireRealtimeLimit(t, hub, userRealtimeRequest("203.0.113.5:1", uuid.New()))
}

func TestRealtimeAnonymousConnectionsAreLimitedPerClientNetwork(t *testing.T) {
	hub := newRealtimeHub(Dependencies{Realtime: &sessionRealtimeStub{}})
	for index := range maximumAnonymousRealtimeConnectionsPerClient {
		if _, ok := admitRealtimeForTest(hub, anonymousRealtimeRequest(fmt.Sprintf("192.0.2.1:%d", 1000+index))); !ok {
			t.Fatalf("anonymous connection %d refused", index)
		}
	}
	requireRealtimeLimit(t, hub, anonymousRealtimeRequest("192.0.2.1:2000"))
	if _, ok := admitRealtimeForTest(hub, anonymousRealtimeRequest("192.0.2.2:1")); !ok {
		t.Fatal("another IPv4 address shared the exhausted bucket")
	}
	// The anonymous network bucket does not limit authenticated principals.
	if _, ok := admitRealtimeForTest(hub, userRealtimeRequest("192.0.2.1:3000", uuid.New())); !ok {
		t.Fatal("user on an exhausted anonymous network was refused")
	}

	// Distinct IPv6 addresses in one /64 share a bucket; another /64 does not.
	for index := range maximumAnonymousRealtimeConnectionsPerClient {
		if _, ok := admitRealtimeForTest(hub, anonymousRealtimeRequest(fmt.Sprintf("[2001:db8:1:2::%x]:443", index+1))); !ok {
			t.Fatalf("IPv6 anonymous connection %d refused", index)
		}
	}
	requireRealtimeLimit(t, hub, anonymousRealtimeRequest("[2001:db8:1:2:ffff:ffff:ffff:ffff]:443"))
	if _, ok := admitRealtimeForTest(hub, anonymousRealtimeRequest("[2001:db8:1:3::1]:443")); !ok {
		t.Fatal("a different IPv6 /64 shared the exhausted bucket")
	}
	if got := hub.addressClients["2001:db8:1:2::/64"]; got != maximumAnonymousRealtimeConnectionsPerClient {
		t.Fatalf("IPv6 /64 bucket=%d, buckets=%v", got, hub.addressClients)
	}
}

func TestRealtimeAnonymousNetworkUsesTrustedProxyClient(t *testing.T) {
	hub := newRealtimeHub(Dependencies{Realtime: &sessionRealtimeStub{}, TrustedProxies: []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8")}})
	forwarded := func(client string) *http.Request {
		request := anonymousRealtimeRequest("10.0.0.5:1")
		request.Header.Set("X-Forwarded-For", client)
		return request
	}
	for range maximumAnonymousRealtimeConnectionsPerClient {
		if _, ok := admitRealtimeForTest(hub, forwarded("198.51.100.7")); !ok {
			t.Fatal("forwarded anonymous connection refused")
		}
	}
	requireRealtimeLimit(t, hub, forwarded("198.51.100.7"))
	if _, ok := admitRealtimeForTest(hub, forwarded("198.51.100.8")); !ok {
		t.Fatal("a different client behind the trusted proxy shared the bucket")
	}
}

func TestRealtimeUserAndServiceTokenConnectionLimits(t *testing.T) {
	hub := newRealtimeHub(Dependencies{Realtime: &sessionRealtimeStub{}})
	user := uuid.New()
	for index := range maximumRealtimeConnectionsPerUser {
		if _, ok := admitRealtimeForTest(hub, userRealtimeRequest(fmt.Sprintf("192.0.2.%d:1", index+1), user)); !ok {
			t.Fatalf("user connection %d refused", index)
		}
	}
	requireRealtimeLimit(t, hub, userRealtimeRequest("192.0.2.50:1", user))
	if _, ok := admitRealtimeForTest(hub, userRealtimeRequest("192.0.2.50:1", uuid.New())); !ok {
		t.Fatal("another user was refused")
	}

	token := uuid.New()
	for index := range maximumRealtimeConnectionsPerServiceToken {
		if _, ok := admitRealtimeForTest(hub, serviceRealtimeRequest(fmt.Sprintf("192.0.2.%d:1", index+100), token)); !ok {
			t.Fatalf("service token connection %d refused", index)
		}
	}
	requireRealtimeLimit(t, hub, serviceRealtimeRequest("192.0.2.200:1", token))
	if _, ok := admitRealtimeForTest(hub, serviceRealtimeRequest("192.0.2.200:1", uuid.New())); !ok {
		t.Fatal("another service token was refused")
	}
}

func TestRealtimeGuestUpgradeMovesAdmissionToUser(t *testing.T) {
	hub := newRealtimeHub(Dependencies{Realtime: &sessionRealtimeStub{}})
	client, ok := admitRealtimeForTest(hub, anonymousRealtimeRequest("192.0.2.1:1"))
	if !ok {
		t.Fatal("anonymous connection refused")
	}
	user := uuid.New()
	request := httptest.NewRequest(http.MethodPost, "/api/realtime", strings.NewReader(`{"clientId":"`+client.id+`","subscriptions":["notes"]}`))
	request.RemoteAddr = "192.0.2.1:9999"
	request.Header.Set("Content-Type", "application/json")
	request = request.WithContext(context.WithValue(request.Context(), userPrincipalKey{}, authenticatedUser{principal: userauth.Principal{User: userauth.User{ID: user}}, token: "test"}))
	response := httptest.NewRecorder()
	hub.replace(response, request)
	if response.Code != http.StatusNoContent {
		t.Fatalf("upgrade status=%d body=%s", response.Code, response.Body.String())
	}
	if hub.anonymousClients != 0 || len(hub.addressClients) != 0 || hub.userClients[user] != 1 || len(hub.clients) != 1 {
		t.Fatalf("after upgrade anonymous=%d addresses=%v users=%v clients=%d", hub.anonymousClients, hub.addressClients, hub.userClients, len(hub.clients))
	}
	hub.mu.Lock()
	hub.releaseLocked(client)
	hub.releaseLocked(client)
	hub.mu.Unlock()
	requireRealtimeCountersEmpty(t, hub)

	// A refused upgrade keeps the guest accounting intact.
	for range maximumRealtimeConnectionsPerUser {
		if _, ok := admitRealtimeForTest(hub, userRealtimeRequest("192.0.2.9:1", user)); !ok {
			t.Fatal("user connection refused")
		}
	}
	guest, ok := admitRealtimeForTest(hub, anonymousRealtimeRequest("192.0.2.1:1"))
	if !ok {
		t.Fatal("anonymous connection refused")
	}
	request = httptest.NewRequest(http.MethodPost, "/api/realtime", strings.NewReader(`{"clientId":"`+guest.id+`","subscriptions":["notes"]}`))
	request.RemoteAddr = "192.0.2.1:9999"
	request.Header.Set("Content-Type", "application/json")
	request = request.WithContext(context.WithValue(request.Context(), userPrincipalKey{}, authenticatedUser{principal: userauth.Principal{User: userauth.User{ID: user}}, token: "test"}))
	response = httptest.NewRecorder()
	hub.replace(response, request)
	if response.Code != http.StatusTooManyRequests || !strings.Contains(response.Body.String(), `"realtime_limit"`) {
		t.Fatalf("over-limit upgrade status=%d body=%s", response.Code, response.Body.String())
	}
	if hub.anonymousClients != 1 || hub.addressClients["192.0.2.1"] != 1 || !guest.admittedAnonymous || hub.userClients[user] != maximumRealtimeConnectionsPerUser {
		t.Fatalf("refused upgrade changed accounting: anonymous=%d addresses=%v users=%v", hub.anonymousClients, hub.addressClients, hub.userClients)
	}
}

type panickingRealtimeWriter struct{ header http.Header }

func (w *panickingRealtimeWriter) Header() http.Header              { return w.header }
func (w *panickingRealtimeWriter) WriteHeader(int)                  {}
func (w *panickingRealtimeWriter) Write([]byte) (int, error)        { panic("realtime write panic") }
func (w *panickingRealtimeWriter) SetWriteDeadline(time.Time) error { return nil }

func TestRealtimeAdmissionReleasedOnErrorAndPanic(t *testing.T) {
	hub := newRealtimeHub(Dependencies{Realtime: &sessionRealtimeStub{}})
	// httptest.ResponseRecorder cannot set write deadlines, so the admitted
	// stream exits on the error path before streaming.
	for _, request := range []*http.Request{anonymousRealtimeRequest("192.0.2.1:1"), userRealtimeRequest("192.0.2.1:1", uuid.New()), serviceRealtimeRequest("192.0.2.1:1", uuid.New())} {
		hub.connect(httptest.NewRecorder(), request)
		requireRealtimeCountersEmpty(t, hub)
	}
	for _, request := range []*http.Request{anonymousRealtimeRequest("192.0.2.1:1"), userRealtimeRequest("192.0.2.1:1", uuid.New()), serviceRealtimeRequest("192.0.2.1:1", uuid.New())} {
		func() {
			defer func() {
				if recover() == nil {
					t.Fatal("writer panic was not propagated")
				}
			}()
			hub.connect(&panickingRealtimeWriter{header: http.Header{}}, request)
		}()
		requireRealtimeCountersEmpty(t, hub)
	}
}

func TestRealtimeAdmissionStressReleasesEverySlot(t *testing.T) {
	hub := newRealtimeHub(Dependencies{Realtime: &sessionRealtimeStub{}})
	server := httptest.NewServer(http.HandlerFunc(hub.serve))
	defer server.Close()
	client := &http.Client{Transport: &http.Transport{MaxConnsPerHost: 0, MaxIdleConnsPerHost: -1, DisableKeepAlives: true}}

	var mu sync.Mutex
	var accepted, limited int
	var wg sync.WaitGroup
	for round := range 3 {
		for worker := range 48 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
				defer cancel()
				request, err := http.NewRequestWithContext(ctx, http.MethodGet, server.URL+"/api/realtime", nil)
				if err != nil {
					t.Error(err)
					return
				}
				response, err := client.Do(request)
				if err != nil {
					t.Errorf("round %d worker %d: %v", round, worker, err)
					return
				}
				defer response.Body.Close()
				switch response.StatusCode {
				case http.StatusOK:
					line, err := bufio.NewReader(response.Body).ReadString('\n')
					if err != nil || !strings.HasPrefix(line, "event: PB_CONNECT") {
						t.Errorf("connect frame=%q err=%v", line, err)
					}
					mu.Lock()
					accepted++
					mu.Unlock()
					// Hold briefly so streams overlap, then disconnect.
					time.Sleep(time.Duration(worker%5) * 10 * time.Millisecond)
				case http.StatusTooManyRequests:
					mu.Lock()
					limited++
					mu.Unlock()
				default:
					t.Errorf("unexpected status %d", response.StatusCode)
				}
			}()
		}
		wg.Wait()
	}
	if accepted == 0 || limited == 0 {
		t.Fatalf("stress did not exercise both admission outcomes: accepted=%d limited=%d", accepted, limited)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		hub.mu.RLock()
		idle := len(hub.clients) == 0 && hub.anonymousClients == 0 && len(hub.addressClients) == 0
		hub.mu.RUnlock()
		if idle || time.Now().After(deadline) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	requireRealtimeCountersEmpty(t, hub)
}
