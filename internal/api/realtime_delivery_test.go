package api

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/yakisoba0728/PgBase/internal/realtime"
	"github.com/yakisoba0728/PgBase/internal/recordaccess"
)

// replayRealtime follows the production reader's head-only, per-topic page and
// global sequence contract without requiring PostgreSQL for transport failures.
type replayRealtime struct {
	mu     sync.Mutex
	head   int64
	floor  int64
	events map[string][]realtime.Event
	reads  int
}

func (s *replayRealtime) Read(context.Context, uuid.UUID, string, *int64) ([]realtime.Event, int64, error) {
	panic("multiplex must use ReadFor")
}

func (s *replayRealtime) ReadFor(_ context.Context, request realtime.ReadRequest) ([]realtime.Event, int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reads++
	if request.Collection == "" {
		return nil, 0, recordaccess.ErrDenied
	}
	if request.After == nil {
		return nil, s.head, nil
	}
	if *request.After < s.floor {
		return nil, 0, realtime.ErrCursorExpired
	}
	if *request.After > s.head {
		return nil, 0, realtime.ErrCursorInvalid
	}
	result := []realtime.Event{}
	for _, event := range s.events[request.Collection] {
		if event.Sequence > *request.After {
			result = append(result, event)
		}
		if len(result) == 100 {
			return result, event.Sequence, nil
		}
	}
	return result, s.head, nil
}

func (s *replayRealtime) seed(collection string, sequences ...int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.events == nil {
		s.events = map[string][]realtime.Event{}
	}
	for _, sequence := range sequences {
		s.events[collection] = append(s.events[collection], realtime.Event{Sequence: sequence, Collection: collection, Action: "create", Operation: "create", PublicID: fmt.Sprint(sequence), Record: map[string]any{"id": fmt.Sprint(sequence)}})
		if sequence > s.head {
			s.head = sequence
		}
	}
}

type replayFrame struct{ id, event, data string }
type replayConnection struct {
	response         *http.Response
	reader           *bufio.Reader
	clientID, cursor string
}

func openReplayConnection(t *testing.T, server *httptest.Server, cursor string) *replayConnection {
	return openReplayConnectionFor(t, server, cursor, 5*time.Second)
}

func openReplayConnectionFor(t *testing.T, server *httptest.Server, cursor string, lifetime time.Duration) *replayConnection {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), lifetime)
	t.Cleanup(cancel)
	request, _ := http.NewRequestWithContext(ctx, http.MethodGet, server.URL+"/api/realtime", nil)
	if cursor != "" {
		request.Header.Set("Last-Event-ID", cursor)
	}
	response, err := server.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { response.Body.Close() })
	if response.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(response.Body)
		t.Fatalf("reconnect cursor=%q status=%d body=%s", cursor, response.StatusCode, body)
	}
	connection := &replayConnection{response: response, reader: bufio.NewReader(response.Body), cursor: cursor}
	frame := connection.next(t)
	if frame.event != "PB_CONNECT" {
		t.Fatalf("connect=%+v", frame)
	}
	var data struct {
		ClientID string `json:"clientId"`
	}
	if json.Unmarshal([]byte(frame.data), &data) != nil || data.ClientID == "" {
		t.Fatalf("connect data=%s", frame.data)
	}
	connection.clientID = data.ClientID
	return connection
}

func (c *replayConnection) next(t *testing.T) replayFrame {
	t.Helper()
	frame := replayFrame{}
	for {
		line, err := c.reader.ReadString('\n')
		if err != nil {
			t.Fatalf("SSE read after cursor=%q: %v", c.cursor, err)
		}
		line = strings.TrimSuffix(line, "\n")
		if line == "" {
			return frame
		}
		if value, ok := strings.CutPrefix(line, "id: "); ok {
			frame.id = value
			c.cursor = value
		}
		if value, ok := strings.CutPrefix(line, "event: "); ok {
			frame.event = value
		}
		if value, ok := strings.CutPrefix(line, "data: "); ok {
			frame.data = value
		}
	}
}

func (c *replayConnection) record(t *testing.T) replayFrame {
	t.Helper()
	for {
		frame := c.next(t)
		if frame.event == "cursor" || frame.event == "" {
			continue
		}
		return frame
	}
}

func replaceReplayTopics(t *testing.T, server *httptest.Server, connection *replayConnection, topics ...string) {
	t.Helper()
	body, _ := json.Marshal(map[string]any{"clientId": connection.clientID, "subscriptions": topics})
	response, err := server.Client().Post(server.URL+"/api/realtime", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusNoContent {
		data, _ := io.ReadAll(response.Body)
		t.Fatalf("replace status=%d body=%s", response.StatusCode, data)
	}
}

func TestRealtimeApplicationTopicSurvivesCollectionPolling(t *testing.T) {
	stream, broker := &replayRealtime{}, NewApplicationRealtimeBroker()
	hub := newRealtimeHub(Dependencies{Realtime: stream, ApplicationRealtimeBroker: broker})
	server := httptest.NewServer(http.HandlerFunc(hub.serve))
	t.Cleanup(server.Close)
	connection := openReplayConnection(t, server, "")
	replaceReplayTopics(t, server, connection, "@app/notice")
	time.Sleep(1200 * time.Millisecond)
	if delivered := broker.Publish(t.Context(), "@app/notice", json.RawMessage(`{"alive":true}`)); delivered != 1 {
		t.Fatalf("application topic disconnected after poll: delivered=%d", delivered)
	}
	frame := connection.record(t)
	if frame.event != "@app/notice" {
		t.Fatalf("application frame=%+v", frame)
	}
	stream.mu.Lock()
	reads := stream.reads
	stream.mu.Unlock()
	if reads != 0 {
		t.Fatalf("custom topic queried collection reader %d times", reads)
	}
}

func TestRealtimeMultiplexOutlivesHTTPServerWriteTimeout(t *testing.T) {
	stream, broker := &replayRealtime{}, NewApplicationRealtimeBroker()
	hub := newRealtimeHub(Dependencies{Realtime: stream, ApplicationRealtimeBroker: broker})
	server := httptest.NewUnstartedServer(http.HandlerFunc(hub.serve))
	server.Config.WriteTimeout = 40 * time.Millisecond
	server.Start()
	t.Cleanup(server.Close)
	connection := openReplayConnection(t, server, "")
	replaceReplayTopics(t, server, connection, "@app/notice")
	time.Sleep(100 * time.Millisecond)
	broker.Publish(t.Context(), "@app/notice", json.RawMessage(`true`))
	if frame := connection.record(t); frame.event != "@app/notice" {
		t.Fatalf("frame=%+v", frame)
	}
}

func TestRealtimeMultiplexReconnectBeforeFirstRecord(t *testing.T) {
	hub := newRealtimeHub(Dependencies{Realtime: &replayRealtime{}})
	server := httptest.NewServer(http.HandlerFunc(hub.serve))
	t.Cleanup(server.Close)
	first := openReplayConnection(t, server, "")
	first.response.Body.Close()
	second := openReplayConnection(t, server, first.cursor)
	second.response.Body.Close()
}

func TestRealtimeMultiplexOrdersTopicsBeforeAdvancingResumeCursor(t *testing.T) {
	stream := &replayRealtime{}
	hub := newRealtimeHub(Dependencies{Realtime: stream})
	server := httptest.NewServer(http.HandlerFunc(hub.serve))
	t.Cleanup(server.Close)
	connection := openReplayConnection(t, server, "")
	replaceReplayTopics(t, server, connection, "later", "earlier")
	stream.seed("earlier", 10)
	stream.seed("later", 20)
	first := connection.record(t)
	if first.event != "earlier" {
		t.Fatalf("out-of-order first topic=%s data=%s cursor=%s", first.event, first.data, connection.cursor)
	}
	connection.response.Body.Close()
	reconnected := openReplayConnection(t, server, connection.cursor)
	replaceReplayTopics(t, server, reconnected, "later", "earlier")
	if next := reconnected.record(t); next.event != "later" {
		t.Fatalf("resume event=%+v", next)
	}
}

func TestRealtimeMultiplexInterruptedOverlappingTopicsReplayUndeliveredMatch(t *testing.T) {
	stream := &replayRealtime{}
	hub := newRealtimeHub(Dependencies{Realtime: stream})
	server := httptest.NewServer(http.HandlerFunc(hub.serve))
	t.Cleanup(server.Close)
	connection := openReplayConnection(t, server, "")
	replaceReplayTopics(t, server, connection, "notes", "notes/*")
	stream.seed("notes", 10)
	first := connection.record(t)
	if first.event != "notes" {
		t.Fatalf("first=%+v", first)
	}
	if cursor, err := strconv.ParseInt(connection.cursor, 10, 64); err != nil || cursor >= 10 {
		t.Fatalf("cursor acknowledged undelivered overlapping match: %q", connection.cursor)
	}
	connection.response.Body.Close()
	reconnected := openReplayConnection(t, server, connection.cursor)
	replaceReplayTopics(t, server, reconnected, "notes", "notes/*")
	for range 2 {
		if frame := reconnected.record(t); frame.event == "notes/*" {
			return
		}
	}
	t.Fatal("undelivered overlapping subscription match was lost on reconnect")
}

func TestRealtimeMultiplexDoesNotAdvancePastAnotherTopicsFullPage(t *testing.T) {
	stream := &replayRealtime{}
	hub := newRealtimeHub(Dependencies{Realtime: stream})
	server := httptest.NewServer(http.HandlerFunc(hub.serve))
	t.Cleanup(server.Close)
	connection := openReplayConnection(t, server, "")
	replaceReplayTopics(t, server, connection, "later", "busy")
	for sequence := int64(1); sequence <= 101; sequence++ {
		stream.seed("busy", sequence)
	}
	stream.seed("later", 200)
	for sequence := int64(1); sequence <= 101; sequence++ {
		frame := connection.record(t)
		if frame.event != "busy" || !strings.Contains(frame.data, fmt.Sprintf(`"id":"%d"`, sequence)) {
			t.Fatalf("event %d=%+v", sequence, frame)
		}
	}
	if frame := connection.record(t); frame.event != "later" {
		t.Fatalf("last=%+v", frame)
	}
}

func TestRealtimeMultiplexRejectsExpiredResumeBeforeReplacingSubscriptions(t *testing.T) {
	stream := &replayRealtime{head: 20, floor: 10}
	hub := newRealtimeHub(Dependencies{Realtime: stream})
	server := httptest.NewServer(http.HandlerFunc(hub.serve))
	t.Cleanup(server.Close)
	connection := openReplayConnection(t, server, "5")
	body, _ := json.Marshal(map[string]any{"clientId": connection.clientID, "subscriptions": []string{"notes"}})
	response, err := server.Client().Post(server.URL+"/api/realtime", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusGone {
		t.Fatalf("expired resume status=%d, want 410", response.StatusCode)
	}
	hub.mu.RLock()
	client := hub.clients[connection.clientID]
	hub.mu.RUnlock()
	client.mu.RLock()
	defer client.mu.RUnlock()
	if len(client.subscriptions) != 0 || client.resume == nil || *client.resume != 5 {
		t.Fatal("failed replacement changed subscription/resume state")
	}
}

func TestRealtimeMultiplexSlowConsumerReleasesAdmission(t *testing.T) {
	broker := NewApplicationRealtimeBroker()
	hub := newRealtimeHub(Dependencies{Realtime: &replayRealtime{}, ApplicationRealtimeBroker: broker})
	server := httptest.NewServer(http.HandlerFunc(hub.serve))
	t.Cleanup(server.Close)
	transport := &http.Transport{DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
		connection, err := (&net.Dialer{}).DialContext(ctx, network, address)
		if err == nil {
			err = connection.(*net.TCPConn).SetReadBuffer(1024)
			if err != nil {
				connection.Close()
			}
		}
		return connection, err
	}}
	t.Cleanup(transport.CloseIdleConnections)
	server.Client().Transport = transport
	connection := openReplayConnectionFor(t, server, "", 15*time.Second)
	replaceReplayTopics(t, server, connection, "@app/large")
	payload := json.RawMessage(`"` + strings.Repeat("x", maximumApplicationRealtimePayload-2) + `"`)
	for range 512 {
		broker.Publish(t.Context(), "@app/large", payload)
	}
	// Keep the socket open without consuming payloads. The server, rather than
	// client cancellation, must stop the blocked write and release its slot.
	deadline := time.Now().Add(12 * time.Second)
	for {
		hub.mu.RLock()
		_, exists := hub.clients[connection.clientID]
		hub.mu.RUnlock()
		if !exists && len(broker.Clients()) == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("slow consumer retained connection/broker admission past write deadline")
		}
		time.Sleep(20 * time.Millisecond)
	}
	reconnected := openReplayConnection(t, server, "")
	reconnected.response.Body.Close()
}
