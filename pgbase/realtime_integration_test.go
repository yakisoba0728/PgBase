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

type streamEvent struct{ id, event, data string }

func openUserStream(t *testing.T, app *publicUsersFixture, token, cursor string) (<-chan streamEvent, context.CancelFunc) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	request, err := http.NewRequestWithContext(ctx, "GET", app.server.URL+"/api/v1/user/collections/notes/events", nil)
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
