//go:build integration

package pgbase

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"
)

type multiplexReplayConnection struct {
	body     *http.Response
	reader   *bufio.Reader
	clientID string
}

func openMultiplexReplay(t *testing.T, fixture *publicUsersFixture, token, cursor string) *multiplexReplayConnection {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 8*time.Second)
	t.Cleanup(cancel)
	request, _ := http.NewRequestWithContext(ctx, http.MethodGet, fixture.server.URL+"/api/realtime", nil)
	request.Header.Set("Authorization", "Bearer "+token)
	if cursor != "" {
		request.Header.Set("Last-Event-ID", cursor)
	}
	response, err := fixture.server.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { response.Body.Close() })
	if response.StatusCode != http.StatusOK {
		t.Fatalf("replay status=%d", response.StatusCode)
	}
	connection := &multiplexReplayConnection{body: response, reader: bufio.NewReader(response.Body)}
	frame := connection.next(t)
	if frame.event != "PB_CONNECT" || frame.id != "" {
		t.Fatalf("connect frame=%+v", frame)
	}
	var connect struct {
		ClientID string `json:"clientId"`
	}
	if json.Unmarshal([]byte(frame.data), &connect) != nil || connect.ClientID == "" {
		t.Fatalf("connect data=%s", frame.data)
	}
	connection.clientID = connect.ClientID
	return connection
}

func (c *multiplexReplayConnection) next(t *testing.T) streamEvent {
	t.Helper()
	frame := streamEvent{}
	for {
		line, err := c.reader.ReadString('\n')
		if err != nil {
			t.Fatalf("read replay: %v", err)
		}
		line = strings.TrimSuffix(line, "\n")
		if line == "" {
			return frame
		}
		if value, ok := strings.CutPrefix(line, "id: "); ok {
			frame.id = value
		}
		if value, ok := strings.CutPrefix(line, "event: "); ok {
			frame.event = value
		}
		if value, ok := strings.CutPrefix(line, "data: "); ok {
			frame.data = value
		}
	}
}

func (c *multiplexReplayConnection) record(t *testing.T) streamEvent {
	t.Helper()
	for {
		frame := c.next(t)
		if frame.event != "cursor" && frame.event != "" {
			return frame
		}
	}
}

func TestRealtimeMultiplexPostgresInterruptedOverlapReplaysEveryTopic(t *testing.T) {
	fixture := newPublicUsersApp(t, true)
	admin, csrf := setupPublicUsersAdministrator(t, fixture)
	for _, collection := range []string{"replay_early", "replay_later"} {
		createPublicUsersCollection(t, fixture, admin, csrf, collection)
		assertPublicUsersStatus(t, publicUsersRequest(t, fixture, "PUT", "/api/admin/collections/"+collection+"/user-access", map[string]any{"enabled": true}, "", admin, map[string]string{"X-PgBase-CSRF": csrf}), 200)
	}
	owner := registerPublicUser(t, fixture, "multiplex-replay@example.test", publicUserOnePass)
	for _, collection := range []string{"replay_early", "replay_later"} {
		assertPublicUsersStatus(t, publicUsersRequest(t, fixture, "POST", "/api/v1/user/collections/"+collection+"/records", map[string]any{"title": collection}, owner.Token, nil, nil), 201)
		if err := fixture.app.realtimeWorker.(interface{ Deliver(context.Context) error }).Deliver(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	first := openMultiplexReplay(t, fixture, owner.Token, "0")
	topics := []string{"replay_later", "replay_early", "replay_early/*"}
	replaceMultiplexSubscriptions(t, fixture, first.clientID, owner.Token, nil, "", topics)
	interrupted := first.record(t)
	if interrupted.event != "replay_early" || interrupted.id != "0" {
		t.Fatalf("unsafe first checkpoint=%+v", interrupted)
	}
	first.body.Body.Close()
	second := openMultiplexReplay(t, fixture, owner.Token, interrupted.id)
	replaceMultiplexSubscriptions(t, fixture, second.clientID, owner.Token, nil, "", topics)
	previous := int64(0)
	for _, topic := range []string{"replay_early", "replay_early/*", "replay_later"} {
		frame := second.record(t)
		cursor, err := strconv.ParseInt(frame.id, 10, 64)
		if err != nil || cursor < previous || frame.event != topic {
			t.Fatalf("replay expected %s after %d: %+v", topic, previous, frame)
		}
		previous = cursor
	}
}

func TestRealtimeMultiplexPostgresApplicationTopicSurvivesPollAndRevocation(t *testing.T) {
	fixture := newPublicUsersApp(t, true)
	setupPublicUsersAdministrator(t, fixture)
	owner := registerPublicUser(t, fixture, "multiplex-app@example.test", publicUserOnePass)
	connection := openMultiplexReplay(t, fixture, owner.Token, "")
	replaceMultiplexSubscriptions(t, fixture, connection.clientID, owner.Token, nil, "", []string{"@app/notice"})
	time.Sleep(1200 * time.Millisecond)
	if delivered := fixture.app.realtimeBroker.Publish(t.Context(), "@app/notice", json.RawMessage(`{"alive":true}`)); delivered != 1 {
		t.Fatalf("after real collection reader polling: delivered=%d", delivered)
	}
	if frame := connection.record(t); frame.event != "@app/notice" || frame.id != "" {
		t.Fatalf("application event=%+v", frame)
	}
	assertPublicUsersStatus(t, publicUsersRequest(t, fixture, "POST", "/api/v1/auth/logout", nil, owner.Token, nil, nil), 204)
	if delivered := fixture.app.realtimeBroker.Publish(t.Context(), "@app/notice", json.RawMessage(`{"revoked":true}`)); delivered != 0 {
		t.Fatal("revoked user received application message")
	}
	deadline := time.Now().Add(3 * time.Second)
	for len(fixture.app.realtimeBroker.Clients()) != 0 {
		if time.Now().After(deadline) {
			t.Fatal("revoked connection retained broker admission")
		}
		time.Sleep(10 * time.Millisecond)
	}
}
