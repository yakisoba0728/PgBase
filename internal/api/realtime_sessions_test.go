package api

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/yakisoba0728/PgBase/internal/identity"
	"github.com/yakisoba0728/PgBase/internal/realtime"
	"github.com/yakisoba0728/PgBase/internal/recordaccess"
	"github.com/yakisoba0728/PgBase/internal/userauth"
)

type sessionRealtimeStub struct{ head int64 }

func (s *sessionRealtimeStub) Read(context.Context, uuid.UUID, string, *int64) ([]realtime.Event, int64, error) {
	return nil, s.head, nil
}

type applicationTopicRealtimeStub struct {
	mu    sync.Mutex
	reads int
	err   error
}

func (s *applicationTopicRealtimeStub) Read(context.Context, uuid.UUID, string, *int64) ([]realtime.Event, int64, error) {
	return nil, 0, s.err
}

func (s *applicationTopicRealtimeStub) ReadFor(context.Context, realtime.ReadRequest) ([]realtime.Event, int64, error) {
	s.mu.Lock()
	s.reads++
	s.mu.Unlock()
	return nil, 0, s.err
}

func (s *applicationTopicRealtimeStub) readCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.reads
}

func TestRealtimeApplicationTopicDeliversOverHTTPExactly(t *testing.T) {
	stream := &applicationTopicRealtimeStub{}
	broker := NewApplicationRealtimeBroker()
	hub := newRealtimeHub(Dependencies{Realtime: stream, ApplicationRealtimeBroker: broker})
	server := httptest.NewServer(http.HandlerFunc(hub.serve))
	defer server.Close()

	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, server.URL+"/api/realtime", nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("connect status=%d", response.StatusCode)
	}
	reader := bufio.NewReader(response.Body)
	connect := readRealtimeSSEFrame(t, reader)
	if connect.event != "PB_CONNECT" {
		t.Fatalf("connect event=%+v", connect)
	}
	var connected struct {
		ClientID string `json:"clientId"`
	}
	if err := json.Unmarshal([]byte(connect.data), &connected); err != nil || connected.ClientID == "" {
		t.Fatalf("connect data=%q err=%v", connect.data, err)
	}

	body := bytes.NewBufferString(`{"clientId":"` + connected.ClientID + `","subscriptions":["@app/notice"]}`)
	subscribe, err := http.NewRequest(http.MethodPost, server.URL+"/api/realtime", body)
	if err != nil {
		t.Fatal(err)
	}
	subscribe.Header.Set("Content-Type", "application/json")
	subscribed, err := http.DefaultClient.Do(subscribe)
	if err != nil {
		t.Fatal(err)
	}
	subscribed.Body.Close()
	if subscribed.StatusCode != http.StatusNoContent {
		t.Fatalf("subscribe status=%d", subscribed.StatusCode)
	}
	other := newRealtimeHub(Dependencies{Realtime: stream})
	wrongBackend := httptest.NewRequest(http.MethodPost, "/api/realtime", strings.NewReader(`{"clientId":"`+connected.ClientID+`","subscriptions":["@app/notice"]}`))
	wrongBackend.Header.Set("Content-Type", "application/json")
	notFound := httptest.NewRecorder()
	other.serve(notFound, wrongBackend)
	if notFound.Code != http.StatusNotFound || !strings.Contains(notFound.Body.String(), "realtime_client_not_found") {
		t.Fatalf("subscription routed to another backend: status=%d body=%q", notFound.Code, notFound.Body.String())
	}
	if calls := stream.readCount(); calls != 0 {
		t.Fatalf("custom subscription queried collection reader %d times", calls)
	}

	if delivered := broker.Publish(t.Context(), "@app/notice", json.RawMessage("{\n  \"value\": [1, true]\n}")); delivered != 1 {
		t.Fatalf("delivered=%d", delivered)
	}
	message := readRealtimeSSEFrame(t, reader)
	if message.event != "@app/notice" || message.data != `{"value":[1,true]}` {
		t.Fatalf("application message=%+v", message)
	}
	if delivered := broker.Publish(t.Context(), "@app/notice/", json.RawMessage(`true`)); delivered != 0 {
		t.Fatalf("different exact topic delivered=%d", delivered)
	}
}

func TestRealtimeApplicationNamespaceCannotBypassCollectionAuthorization(t *testing.T) {
	stream := &applicationTopicRealtimeStub{err: recordaccess.ErrDenied}
	hub := newRealtimeHub(Dependencies{Realtime: stream, ApplicationRealtimeBroker: NewApplicationRealtimeBroker()})
	client := &realtimeClient{id: "client", address: "192.0.2.1", principal: identity.Subject{Kind: identity.Anonymous}}
	hub.clients[client.id] = client

	denied := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/realtime", bytes.NewBufferString(`{"clientId":"client","subscriptions":["removed_notes"]}`))
	req.RemoteAddr = "192.0.2.1:9999"
	req.Header.Set("Content-Type", "application/json")
	hub.replace(denied, req)
	if denied.Code != http.StatusForbidden {
		t.Fatalf("removed collection subscription status=%d", denied.Code)
	}

	custom := httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/realtime", bytes.NewBufferString(`{"clientId":"client","subscriptions":["@app/removed_notes"]}`))
	req.RemoteAddr = "192.0.2.1:9999"
	req.Header.Set("Content-Type", "application/json")
	hub.replace(custom, req)
	if custom.Code != http.StatusNoContent || stream.readCount() != 1 {
		t.Fatalf("namespaced custom status=%d collection reads=%d", custom.Code, stream.readCount())
	}
}

func TestApplicationRealtimeTopicPreservesRawUTF8AndPrefix(t *testing.T) {
	for _, raw := range []string{"custom-topic", "@topic/한 글? #", "@app/Y3VzdG9tLXRvcGlj"} {
		wire, err := ApplicationRealtimeTopic(raw)
		if err != nil || !strings.HasPrefix(wire, applicationRealtimeTopicPrefix) {
			t.Fatalf("raw=%q wire=%q err=%v", raw, wire, err)
		}
		decoded, decodeErr := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(wire, applicationRealtimeTopicPrefix))
		if decodeErr != nil || string(decoded) != raw {
			t.Fatalf("raw=%q decoded=%q err=%v", raw, decoded, decodeErr)
		}
	}
	if _, err := ApplicationRealtimeTopic(""); err == nil {
		t.Fatal("empty application topic accepted")
	}
	if _, err := ApplicationRealtimeTopic(strings.Repeat("a", maximumApplicationRealtimeTopicBytes+1)); err == nil {
		t.Fatal("oversized application topic accepted")
	}
}

func TestApplicationTopicRechecksRevokedUserBeforeBrokerDelivery(t *testing.T) {
	id := uuid.New()
	auth := &realtimeAuthStub{principal: userauth.Principal{User: userauth.User{ID: id}}, err: userauth.ErrInvalidCredentials}
	broker := NewApplicationRealtimeBroker()
	hub := newRealtimeHub(Dependencies{Realtime: &applicationTopicRealtimeStub{}, UserAuth: auth, ApplicationRealtimeBroker: broker})
	hub.clients["client"] = &realtimeClient{id: "client", token: "revoked", principal: identity.Subject{Kind: identity.User, ID: id}}
	broker.connect("client")
	broker.subscribe("client", []string{"@app/Y3VzdG9tLXRvcGlj"})
	if delivered := broker.Publish(t.Context(), "@app/Y3VzdG9tLXRvcGlj", json.RawMessage(`true`)); delivered != 0 {
		t.Fatalf("revoked user received custom topic: delivered=%d", delivered)
	}
}

type realtimeSSEFrame struct{ event, data string }

func readRealtimeSSEFrame(t *testing.T, reader *bufio.Reader) realtimeSSEFrame {
	t.Helper()
	frame := realtimeSSEFrame{}
	for {
		line, err := reader.ReadString('\n')
		if err != nil && err != io.EOF {
			t.Fatal(err)
		}
		line = strings.TrimSuffix(line, "\n")
		if line == "" {
			return frame
		}
		if value, ok := strings.CutPrefix(line, "event: "); ok {
			frame.event = value
		}
		if value, ok := strings.CutPrefix(line, "data: "); ok {
			frame.data = value
		}
		if err == io.EOF {
			t.Fatal("truncated realtime SSE frame")
		}
	}
}

func (s *sessionRealtimeStub) ReadFor(context.Context, realtime.ReadRequest) ([]realtime.Event, int64, error) {
	return nil, s.head, nil
}

type disconnectingRealtimeStub struct {
	sessionRealtimeStub
	disconnect func()
}

func (s *disconnectingRealtimeStub) ReadFor(ctx context.Context, request realtime.ReadRequest) ([]realtime.Event, int64, error) {
	s.disconnect()
	return s.sessionRealtimeStub.ReadFor(ctx, request)
}

func TestRealtimeReplaceAfterDisconnectDoesNotReserveUserSlot(t *testing.T) {
	stream := &disconnectingRealtimeStub{}
	hub := newRealtimeHub(Dependencies{Realtime: stream})
	client := &realtimeClient{id: "client", address: "192.0.2.1", principal: identity.Subject{Kind: identity.Anonymous}}
	hub.clients[client.id] = client
	stream.disconnect = func() {
		hub.mu.Lock()
		delete(hub.clients, client.id)
		hub.mu.Unlock()
	}

	user := userauth.User{ID: uuid.New()}
	request := httptest.NewRequest(http.MethodPost, "/api/realtime", strings.NewReader(`{"clientId":"client","subscriptions":["notes"]}`))
	request.RemoteAddr = "192.0.2.1:9999"
	request.Header.Set("Content-Type", "application/json")
	request = request.WithContext(context.WithValue(request.Context(), userPrincipalKey{}, authenticatedUser{principal: userauth.Principal{User: user}, token: "test"}))
	response := httptest.NewRecorder()
	hub.replace(response, request)

	if response.Code != http.StatusNotFound || !strings.Contains(response.Body.String(), "realtime_client_not_found") {
		t.Fatalf("replace status = %d, body = %s", response.Code, response.Body.String())
	}
	if len(hub.userClients) != 0 || client.admittedUser != uuid.Nil {
		t.Fatalf("disconnected client consumed admission: %v", hub.userClients)
	}
}

func TestRealtimeReplaceWaitsForDeliveryAndPreservesUnchangedCursor(t *testing.T) {
	stream := &sessionRealtimeStub{head: 91}
	hub := newRealtimeHub(Dependencies{Realtime: stream})
	cursor := int64(37)
	client := &realtimeClient{
		id:            "client-1",
		principal:     realtimeSubject(httptest.NewRequest(http.MethodGet, "/", nil)),
		subscriptions: []realtimeSubscription{{raw: "notes", collection: "notes", cursor: &cursor}},
	}
	addressRequest := httptest.NewRequest(http.MethodPost, "/api/realtime", nil)
	addressRequest.RemoteAddr = "192.0.2.10:4321"
	client.address, _ = clientIP(addressRequest, hub.dependencies.trustedProxies())
	hub.clients[client.id] = client

	client.deliveryMu.Lock()
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		request := httptest.NewRequest(http.MethodPost, "/api/realtime", bytes.NewBufferString(`{"clientId":"client-1","subscriptions":["notes","tasks"]}`))
		request.Header.Set("Content-Type", "application/json")
		request.RemoteAddr = "192.0.2.10:4321"
		response := httptest.NewRecorder()
		hub.replace(response, request)
		done <- response
	}()
	select {
	case <-done:
		t.Fatal("replacement completed while an old-principal delivery was active")
	case <-time.After(50 * time.Millisecond):
	}
	client.deliveryMu.Unlock()

	select {
	case response := <-done:
		if response.Code != http.StatusNoContent {
			t.Fatalf("replace status = %d, body = %s", response.Code, response.Body.String())
		}
	case <-time.After(time.Second):
		t.Fatal("replacement remained blocked after delivery completed")
	}
	client.mu.RLock()
	defer client.mu.RUnlock()
	if got := *client.subscriptions[0].cursor; got != cursor {
		t.Fatalf("unchanged subscription cursor = %d, want %d", got, cursor)
	}
	if got := *client.subscriptions[1].cursor; got != stream.head {
		t.Fatalf("new subscription cursor = %d, want current head %d", got, stream.head)
	}
}

func TestRealtimeLifecycleReportsAdmissionAndSubscriptionFailures(t *testing.T) {
	hooks := &realtimeLifecycleStub{}
	hub := newRealtimeHub(Dependencies{Realtime: &applicationTopicRealtimeStub{err: realtime.ErrCursorExpired}, RealtimeLifecycle: hooks})
	for range 128 {
		hub.clients[uuid.NewString()] = &realtimeClient{}
	}
	response := httptest.NewRecorder()
	hub.connect(response, httptest.NewRequest(http.MethodGet, "/api/realtime", nil))
	if response.Code != http.StatusTooManyRequests || strings.Join(hooks.calls, " ") != "connect.before connect.after.error" {
		t.Fatalf("connection limit status=%d hooks=%v", response.Code, hooks.calls)
	}

	hooks.calls = nil
	hub.clients["client"] = &realtimeClient{id: "client", address: "192.0.2.1", principal: identity.Subject{Kind: identity.Anonymous}}
	request := httptest.NewRequest(http.MethodPost, "/api/realtime", strings.NewReader(`{"clientId":"client","subscriptions":["notes"]}`))
	request.RemoteAddr = "192.0.2.1:9999"
	request.Header.Set("Content-Type", "application/json")
	response = httptest.NewRecorder()
	hub.replace(response, request)
	if response.Code != http.StatusGone || strings.Join(hooks.calls, " ") != "subscribe.before subscribe.after.error" {
		t.Fatalf("expired cursor status=%d hooks=%v", response.Code, hooks.calls)
	}
}
