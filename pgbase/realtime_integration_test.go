//go:build integration

package pgbase

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"github.com/google/uuid"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestPocketBaseRealtimeAvailableToGuestAdministratorAndService(t *testing.T) {
	fixture := newPublicUsersApp(t, true)
	admin, csrf := setupPublicUsersAdministrator(t, fixture)
	createPublicUsersCollection(t, fixture, admin, csrf, "audience_events")
	allow := map[string]any{"op": "allow"}
	assertPublicUsersStatus(t, publicUsersRequest(t, fixture, http.MethodPut, "/api/admin/collections/audience_events/rules", map[string]any{"rules": map[string]any{"list": allow, "view": allow, "create": allow, "update": allow, "delete": allow}}, "", admin, map[string]string{"X-PgBase-CSRF": csrf}), 200)
	var collectionID uuid.UUID
	if err := fixture.app.pool.QueryRow(t.Context(), `select id from pgbase_meta.collections where name='audience_events'`).Scan(&collectionID); err != nil {
		t.Fatal(err)
	}
	issued := publicUsersRequest(t, fixture, http.MethodPost, "/api/admin/service-tokens", map[string]any{"name": "realtime", "ttlSeconds": 3600, "scopes": []map[string]any{{"collectionId": collectionID.String(), "action": "read"}}}, "", admin, map[string]string{"X-PgBase-CSRF": csrf})
	assertPublicUsersStatus(t, issued, 201)
	var service struct {
		Token string `json:"token"`
	}
	decodePublicUsersJSON(t, issued, &service)
	for _, test := range []struct {
		name, bearer, csrf string
		cookie             *http.Cookie
	}{{name: "guest"}, {name: "administrator", cookie: admin, csrf: csrf}, {name: "service", bearer: service.Token}} {
		t.Run(test.name, func(t *testing.T) {
			events, cancel := openMultiplexStream(t, fixture, test.bearer, test.cookie)
			defer cancel()
			connected := nextStreamEvent(t, events, "PB_CONNECT")
			var data struct {
				ClientID string `json:"clientId"`
			}
			if err := json.Unmarshal([]byte(connected.data), &data); err != nil {
				t.Fatal(err)
			}
			replaceMultiplexSubscriptions(t, fixture, data.ClientID, test.bearer, test.cookie, test.csrf, []string{"audience_events"})
			created := publicUsersRequest(t, fixture, http.MethodPost, "/api/v1/collections/audience_events/records", map[string]any{"title": test.name}, "", admin, map[string]string{"X-PgBase-CSRF": csrf})
			assertPublicUsersStatus(t, created, 201)
			event := nextStreamEvent(t, events, "audience_events")
			if !strings.Contains(event.data, test.name) {
				t.Fatalf("event=%s", event.data)
			}
		})
	}
}

func openMultiplexStream(t *testing.T, fixture *publicUsersFixture, bearer string, cookie *http.Cookie) (<-chan streamEvent, context.CancelFunc) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	request, _ := http.NewRequestWithContext(ctx, http.MethodGet, fixture.server.URL+"/api/realtime", nil)
	if bearer != "" {
		request.Header.Set("Authorization", "Bearer "+bearer)
	}
	if cookie != nil {
		request.AddCookie(cookie)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	if response.StatusCode != 200 {
		cancel()
		t.Fatalf("connect status=%d", response.StatusCode)
	}
	events := make(chan streamEvent, 100)
	go func() {
		defer close(events)
		defer response.Body.Close()
		scanner := bufio.NewScanner(response.Body)
		var event streamEvent
		for scanner.Scan() {
			line := scanner.Text()
			if line == "" {
				select {
				case events <- event:
				case <-ctx.Done():
					return
				}
				event = streamEvent{}
				continue
			}
			if strings.HasPrefix(line, "id: ") {
				event.id = strings.TrimPrefix(line, "id: ")
			}
			if strings.HasPrefix(line, "event: ") {
				event.event = strings.TrimPrefix(line, "event: ")
			}
			if strings.HasPrefix(line, "data: ") {
				event.data = strings.TrimPrefix(line, "data: ")
			}
		}
	}()
	return events, cancel
}

func replaceMultiplexSubscriptions(t *testing.T, fixture *publicUsersFixture, clientID, bearer string, cookie *http.Cookie, csrf string, subscriptions []string) {
	t.Helper()
	body, _ := json.Marshal(map[string]any{"clientId": clientID, "subscriptions": subscriptions})
	request, _ := http.NewRequest(http.MethodPost, fixture.server.URL+"/api/realtime", bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	if bearer != "" {
		request.Header.Set("Authorization", "Bearer "+bearer)
	}
	if cookie != nil {
		request.AddCookie(cookie)
		request.Header.Set("Origin", publicUsersOrigin)
		request.Header.Set("X-PgBase-CSRF", csrf)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != 204 {
		t.Fatalf("subscribe status=%d", response.StatusCode)
	}
}

func TestPocketBaseRealtimeConnectReplaceFilterFieldsDeleteAndRevocation(t *testing.T) {
	fixture := newPublicUsersApp(t, true)
	admin, csrf := setupPublicUsersAdministrator(t, fixture)
	createPublicUsersCollection(t, fixture, admin, csrf, "multiplex_notes")
	assertPublicUsersStatus(t, publicUsersRequest(t, fixture, "PUT", "/api/admin/collections/multiplex_notes/user-access", map[string]any{"enabled": true}, "", admin, map[string]string{"X-PgBase-CSRF": csrf}), 200)
	owner := registerPublicUser(t, fixture, "multiplex@example.com", publicUserOnePass)
	events, cancel := openMultiplexStream(t, fixture, owner.Token, nil)
	defer cancel()
	connected := nextStreamEvent(t, events, "PB_CONNECT")
	var data struct {
		ClientID string `json:"clientId"`
	}
	if json.Unmarshal([]byte(connected.data), &data) != nil || data.ClientID == "" {
		t.Fatalf("connect=%+v", connected)
	}
	topic := "multiplex_notes?filter=title%3D%27wanted%27&fields=id%2Ctitle"
	replaceMultiplexSubscriptions(t, fixture, data.ClientID, owner.Token, nil, "", []string{topic})
	assertPublicUsersStatus(t, publicUsersRequest(t, fixture, "POST", "/api/v1/user/collections/multiplex_notes/records", map[string]any{"title": "ignored"}, owner.Token, nil, nil), 201)
	created := publicUsersRequest(t, fixture, "POST", "/api/v1/user/collections/multiplex_notes/records", map[string]any{"title": "wanted"}, owner.Token, nil, nil)
	assertPublicUsersStatus(t, created, 201)
	var record struct {
		ID string `json:"id"`
	}
	decodePublicUsersJSON(t, created, &record)
	event := nextStreamEvent(t, events, topic)
	if !strings.Contains(event.data, `"action":"create"`) || !strings.Contains(event.data, `"title":"wanted"`) || strings.Contains(event.data, "collectionName") {
		t.Fatalf("projected event=%s", event.data)
	}
	assertPublicUsersStatus(t, publicUsersRequest(t, fixture, "DELETE", "/api/v1/user/collections/multiplex_notes/records/"+record.ID, nil, owner.Token, nil, nil), 204)
	event = nextStreamEvent(t, events, topic)
	if !strings.Contains(event.data, `"action":"delete"`) || !strings.Contains(event.data, `"title":"wanted"`) {
		t.Fatalf("delete event=%s", event.data)
	}
	assertPublicUsersStatus(t, publicUsersRequest(t, fixture, "POST", "/api/v1/auth/logout", nil, owner.Token, nil, nil), 204)
	timer := time.NewTimer(3 * time.Second)
	defer timer.Stop()
	for {
		select {
		case _, ok := <-events:
			if !ok {
				return
			}
		case <-timer.C:
			t.Fatal("revoked multiplex stream remained open")
		}
	}
}

type streamEvent struct{ id, event, data string }

func openUserStream(t *testing.T, app *publicUsersFixture, token, cursor string) (<-chan streamEvent, context.CancelFunc) {
	return openUserStreamForCollection(t, app, token, cursor, "notes")
}

func openUserStreamForCollection(t *testing.T, app *publicUsersFixture, token, cursor, collection string) (<-chan streamEvent, context.CancelFunc) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	request, err := http.NewRequestWithContext(ctx, "GET", app.server.URL+"/api/v1/user/collections/"+collection+"/events", nil)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+token)
	if cursor != "" {
		request.Header.Set("Last-Event-ID", cursor)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	if response.StatusCode != 200 {
		cancel()
		response.Body.Close()
		t.Fatalf("SSE status=%d", response.StatusCode)
	}
	events := make(chan streamEvent, 100)
	go func() {
		defer close(events)
		defer response.Body.Close()
		scanner := bufio.NewScanner(response.Body)
		var event streamEvent
		for scanner.Scan() {
			line := scanner.Text()
			if line == "" {
				select {
				case events <- event:
				case <-ctx.Done():
					return
				}
				event = streamEvent{}
				continue
			}
			switch {
			case strings.HasPrefix(line, "id: "):
				event.id = strings.TrimPrefix(line, "id: ")
			case strings.HasPrefix(line, "event: "):
				event.event = strings.TrimPrefix(line, "event: ")
			case strings.HasPrefix(line, "data: "):
				event.data = strings.TrimPrefix(line, "data: ")
			}
		}
	}()
	return events, cancel
}

func TestRealtimeLifecycleHooksThroughPGHTTPDelivery(t *testing.T) {
	registry := &HookRegistry{}
	var connects, subscribes, messages atomic.Int32
	for kind, counter := range map[string]*atomic.Int32{HookRealtimeConnect: &connects, HookRealtimeSubscribe: &subscribes, HookRealtimeMessage: &messages} {
		counter := counter
		if _, err := registry.On(kind, Hook{Before: func(context.Context, *HookEvent) error { counter.Add(1); return nil }}); err != nil {
			t.Fatal(err)
		}
	}
	app := newPublicUsersAppOptions(t, true, func(options *Options) { options.Hooks = registry })
	admin, csrf := setupPublicUsersAdministrator(t, app)
	createPublicUsersCollection(t, app, admin, csrf, "hook_events")
	assertPublicUsersStatus(t, publicUsersRequest(t, app, "PUT", "/api/admin/collections/hook_events/user-access", map[string]any{"enabled": true}, "", admin, map[string]string{"X-PgBase-CSRF": csrf}), 200)
	owner := registerPublicUser(t, app, "hook-stream@example.com", publicUserOnePass)
	events, cancel := openUserStreamForCollection(t, app, owner.Token, "", "hook_events")
	nextStreamEvent(t, events, "cursor")
	assertPublicUsersStatus(t, publicUsersRequest(t, app, "POST", "/api/v1/user/collections/hook_events/records", map[string]any{"title": "deliver"}, owner.Token, nil, nil), 201)
	nextStreamEvent(t, events, "record")
	cancel()
	if connects.Load() != 1 || subscribes.Load() < 1 || messages.Load() != 1 {
		t.Fatalf("connect=%d subscribe=%d message=%d", connects.Load(), subscribes.Load(), messages.Load())
	}
}

func nextStreamEvent(t *testing.T, events <-chan streamEvent, kind string) streamEvent {
	t.Helper()
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	for {
		select {
		case event, ok := <-events:
			if !ok {
				t.Fatal("SSE ended unexpectedly")
			}
			if event.event == kind {
				return event
			}
		case <-timer.C:
			t.Fatalf("no %s SSE event", kind)
		}
	}
}

func TestRealtimeHTTPReplayDeleteAndSessionRevocation(t *testing.T) {
	app := newPublicUsersApp(t, true)
	admin, csrf := setupPublicUsersAdministrator(t, app)
	createPublicUsersCollection(t, app, admin, csrf, "notes")
	policy := publicUsersRequest(t, app, "PUT", "/api/admin/collections/notes/user-access", map[string]any{"enabled": true}, "", admin, map[string]string{"X-PgBase-CSRF": csrf})
	assertPublicUsersStatus(t, policy, 200)
	owner := registerPublicUser(t, app, "stream-owner@example.com", publicUserOnePass)
	other := registerPublicUser(t, app, "stream-other@example.com", publicUserTwoPass)
	events, cancel := openUserStream(t, app, owner.Token, "")
	defer cancel()
	start := nextStreamEvent(t, events, "cursor")
	assertPublicUsersStatus(t, publicUsersRequest(t, app, "POST", "/api/v1/user/collections/notes/records", map[string]any{"title": "other-private"}, other.Token, nil, nil), 201)
	created := publicUsersRequest(t, app, "POST", "/api/v1/user/collections/notes/records", map[string]any{"title": "owner-private"}, owner.Token, nil, nil)
	assertPublicUsersStatus(t, created, 201)
	var record struct {
		ID string `json:"id"`
	}
	decodePublicUsersJSON(t, created, &record)
	first := nextStreamEvent(t, events, "record")
	var message struct {
		RecordID  string `json:"recordId"`
		Operation string `json:"operation"`
	}
	if json.Unmarshal([]byte(first.data), &message) != nil || message.RecordID != record.ID || message.Operation != "create" || strings.Contains(first.data, "private") {
		t.Fatalf("unexpected record metadata %s", first.data)
	}
	before, _ := strconv.ParseInt(start.id, 10, 64)
	after, _ := strconv.ParseInt(first.id, 10, 64)
	if after <= before {
		t.Fatal("delivery cursor did not advance")
	}
	cancel()
	assertPublicUsersStatus(t, publicUsersRequest(t, app, "DELETE", "/api/v1/user/collections/notes/records/"+record.ID, nil, owner.Token, nil, map[string]string{"If-Match": created.Header().Get("ETag")}), 204)
	replay, replayCancel := openUserStream(t, app, owner.Token, first.id)
	defer replayCancel()
	deleted := nextStreamEvent(t, replay, "record")
	if json.Unmarshal([]byte(deleted.data), &message) != nil || message.RecordID != record.ID || message.Operation != "delete" {
		t.Fatalf("deleted owner metadata lost: %s", deleted.data)
	}
	assertPublicUsersStatus(t, publicUsersRequest(t, app, "POST", "/api/v1/auth/logout", nil, owner.Token, nil, nil), 204)
	timer := time.NewTimer(3 * time.Second)
	defer timer.Stop()
	for {
		select {
		case _, ok := <-replay:
			if !ok {
				return
			}
		case <-timer.C:
			t.Fatal("revoked session retained its stream")
		}
	}
}

func TestRealtimeAppliesCurrentRulesAndDeletedRecordSnapshot(t *testing.T) {
	app := newPublicUsersApp(t, true)
	admin, csrf := setupPublicUsersAdministrator(t, app)
	createPublicUsersCollection(t, app, admin, csrf, "notes")
	assertPublicUsersStatus(t, publicUsersRequest(t, app, "PUT", "/api/admin/collections/notes/user-access", map[string]any{"enabled": true}, "", admin, map[string]string{"X-PgBase-CSRF": csrf}), 200)
	viewer := registerPublicUser(t, app, "event-viewer@example.com", publicUserOnePass)
	writer := registerPublicUser(t, app, "event-writer@example.com", publicUserTwoPass)
	rules := map[string]any{"list": map[string]any{"op": "eq", "field": "title", "value": "published"}, "view": map[string]any{"op": "eq", "field": "title", "value": "published"}, "create": map[string]any{"op": "owner"}, "update": map[string]any{"op": "owner"}, "delete": map[string]any{"op": "owner"}}
	set := func() {
		t.Helper()
		assertPublicUsersStatus(t, publicUsersRequest(t, app, "PUT", "/api/admin/collections/notes/rules", map[string]any{"rules": rules}, "", admin, map[string]string{"X-PgBase-CSRF": csrf}), 200)
	}
	set()
	events, cancel := openUserStream(t, app, viewer.Token, "")
	defer cancel()
	nextStreamEvent(t, events, "cursor")
	created := publicUsersRequest(t, app, "POST", "/api/v1/user/collections/notes/records", map[string]any{"title": "published"}, writer.Token, nil, nil)
	assertPublicUsersStatus(t, created, 201)
	var record struct {
		ID string `json:"id"`
	}
	decodePublicUsersJSON(t, created, &record)
	event := nextStreamEvent(t, events, "record")
	if !strings.Contains(event.data, record.ID) {
		t.Fatalf("cross-owner public event missing: %s", event.data)
	}
	assertPublicUsersStatus(t, publicUsersRequest(t, app, "DELETE", "/api/v1/user/collections/notes/records/"+record.ID, nil, writer.Token, nil, nil), 204)
	event = nextStreamEvent(t, events, "record")
	if !strings.Contains(event.data, `"operation":"delete"`) {
		t.Fatalf("delete snapshot rule=%s", event.data)
	}
	rules["view"] = map[string]any{"op": "deny"}
	set()
	assertPublicUsersStatus(t, publicUsersRequest(t, app, "POST", "/api/v1/user/collections/notes/records", map[string]any{"title": "published"}, writer.Token, nil, nil), 201)
	timer := time.NewTimer(1500 * time.Millisecond)
	defer timer.Stop()
	for {
		select {
		case event, ok := <-events:
			if !ok {
				return
			}
			if event.event == "record" {
				t.Fatal("event delivered after view rule revoked")
			}
		case <-timer.C:
			return
		}
	}
}
