package api

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/yakisoba0728/PgBase/internal/realtime"
)

type sessionRealtimeStub struct{ head int64 }

func (s *sessionRealtimeStub) Read(context.Context, uuid.UUID, string, *int64) ([]realtime.Event, int64, error) {
	return nil, s.head, nil
}

func (s *sessionRealtimeStub) ReadFor(context.Context, realtime.ReadRequest) ([]realtime.Event, int64, error) {
	return nil, s.head, nil
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
