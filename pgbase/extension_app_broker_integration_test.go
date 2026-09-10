//go:build integration

package pgbase

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/yakisoba0728/PgBase/internal/api"
	"github.com/yakisoba0728/PgBase/internal/identity"
	"github.com/yakisoba0728/PgBase/internal/jsvm"
)

func TestJavaScriptApplicationBrokerDeliversThroughRealtimeHTTP(t *testing.T) {
	fixture := newPublicUsersApp(t, true)
	cookie, csrf := setupPublicUsersAdministrator(t, fixture)
	createPublicUsersCollection(t, fixture, cookie, csrf, "broker_notes")
	enable := publicUsersRequest(t, fixture, http.MethodPut, "/api/admin/collections/broker_notes/user-access", map[string]any{"enabled": true}, "", cookie, map[string]string{"X-PgBase-CSRF": csrf})
	assertPublicUsersStatus(t, enable, http.StatusOK)
	owner := registerPublicUser(t, fixture, "broker-owner@example.test", publicUserOnePass)
	nonOwner := registerPublicUser(t, fixture, "broker-other@example.test", publicUserTwoPass)
	created := publicUsersRequest(t, fixture, http.MethodPost, "/api/v1/user/collections/broker_notes/records", map[string]any{"title": "owned"}, owner.Token, nil, map[string]string{"Idempotency-Key": "broker-owned-record-0001"})
	assertPublicUsersStatus(t, created, http.StatusCreated)
	var record map[string]any
	decodePublicUsersJSON(t, created, &record)
	recordID, _ := record["id"].(string)
	if uuid.Validate(recordID) != nil {
		t.Fatalf("record id=%q", recordID)
	}

	type connection struct {
		client   *http.Client
		response *http.Response
		reader   *bufio.Reader
		id       string
	}
	connect := func(token string) connection {
		t.Helper()
		client := &http.Client{Timeout: 8 * time.Second}
		request, _ := http.NewRequest(http.MethodGet, fixture.server.URL+"/api/realtime", nil)
		request.Host = "pgbase.test"
		if token != "" {
			request.Header.Set("Authorization", "Bearer "+token)
		}
		response, err := client.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		reader := bufio.NewReader(response.Body)
		var clientID string
		for clientID == "" {
			line, readErr := reader.ReadString('\n')
			if readErr != nil {
				t.Fatal(readErr)
			}
			if strings.HasPrefix(line, "data: ") {
				var event struct {
					ClientID string `json:"clientId"`
				}
				_ = json.Unmarshal([]byte(strings.TrimSpace(strings.TrimPrefix(line, "data: "))), &event)
				clientID = event.ClientID
			}
		}
		body, _ := json.Marshal(map[string]any{"clientId": clientID, "subscriptions": []string{"broker_notes"}})
		subscribe, _ := http.NewRequest(http.MethodPost, fixture.server.URL+"/api/realtime", bytes.NewReader(body))
		subscribe.Host = "pgbase.test"
		subscribe.Header.Set("Content-Type", "application/json")
		if token != "" {
			subscribe.Header.Set("Authorization", "Bearer "+token)
		}
		subscriptionResponse, subscribeErr := client.Do(subscribe)
		if subscribeErr != nil || subscriptionResponse.StatusCode != http.StatusNoContent {
			t.Fatalf("subscribe status=%v err=%v", subscriptionResponse.StatusCode, subscribeErr)
		}
		subscriptionResponse.Body.Close()
		return connection{client: client, response: response, reader: reader, id: clientID}
	}
	ownerConnection := connect(owner.Token)
	otherConnection := connect(nonOwner.Token)
	guestConnection := connect("")
	defer ownerConnection.response.Body.Close()
	defer otherConnection.response.Body.Close()
	defer guestConnection.response.Body.Close()

	directory := t.TempDir()
	if err := os.WriteFile(filepath.Join(directory, "pgbase.scripts.json"), []byte(`{"format":"pgbase.scripts/v1","routes":[{"method":"POST","pattern":"/publish","access":"admin","handler":{"module":"route.js","export":"run"},"capabilities":["app-control"]}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "route.js"), []byte(fmt.Sprintf(`exports.run=()=> $app.subscriptionsBroker().publish("broker_notes", {record:{id:%q},ok:true})`, recordID)), 0o600); err != nil {
		t.Fatal(err)
	}
	bundle, err := jsvm.LoadBundle(directory)
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := jsvm.NewRuntime(bundle, jsvm.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close(context.Background())
	host := &extensionHost{bridge: fixture.app.extensions, principal: identity.Subject{Kind: identity.Administrator, ID: uuid.New()}, requestID: "broker-publish", valid: true}
	input, _ := json.Marshal(map[string]string{"recordId": recordID})
	output, err := runtime.Invoke(t.Context(), jsvm.Invocation{Handler: bundle.Manifest().Routes[0].Handler, Capabilities: []string{"app-control"}, Input: input, Host: host})
	if err != nil || string(output) != `{"delivered":1}` {
		t.Fatalf("publish=%s err=%v", output, err)
	}
	for {
		line, err := ownerConnection.reader.ReadString('\n')
		if err != nil {
			t.Fatal(err)
		}
		if strings.TrimSpace(line) == "event: broker_notes" {
			data, err := ownerConnection.reader.ReadString('\n')
			if err != nil || !strings.Contains(data, `"ok":true`) {
				t.Fatalf("data=%q err=%v", data, err)
			}
			break
		}
	}
	assertNoBrokerEvent := func(name string, candidate connection) {
		t.Helper()
		result := make(chan string, 1)
		go func() {
			for {
				line, _ := candidate.reader.ReadString('\n')
				if strings.TrimSpace(line) != "" && strings.TrimSpace(line) != ": keepalive" {
					result <- line
					return
				}
			}
		}()
		select {
		case line := <-result:
			t.Fatalf("%s received broker output %q", name, line)
		case <-time.After(150 * time.Millisecond):
		}
	}
	assertNoBrokerEvent("non-owner", otherConnection)
	assertNoBrokerEvent("guest", guestConnection)
	assertPublicUsersStatus(t, publicUsersRequest(t, fixture, http.MethodPost, "/api/v1/auth/logout", nil, owner.Token, nil, nil), http.StatusNoContent)
	output, err = runtime.Invoke(t.Context(), jsvm.Invocation{Handler: bundle.Manifest().Routes[0].Handler, Capabilities: []string{"app-control"}, Input: input, Host: host})
	if err != nil || string(output) != `{"delivered":0}` {
		t.Fatalf("publish after logout=%s err=%v", output, err)
	}
	ownerConnection.response.Body.Close()
	otherConnection.response.Body.Close()
	guestConnection.response.Body.Close()
	deadline := time.Now().Add(time.Second)
	for len(fixture.app.realtimeBroker.Clients()) != 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if got := fixture.app.realtimeBroker.Clients(); len(got) != 0 {
		t.Fatalf("connected clients after close=%v", got)
	}
}

func TestJavaScriptApplicationBrokerDeliversArbitraryTopicThroughRealtimeHTTP(t *testing.T) {
	fixture := newPublicUsersApp(t, true)
	events, cancel := openMultiplexStream(t, fixture, "", nil)
	defer cancel()
	connected := nextStreamEvent(t, events, "PB_CONNECT")
	var connection struct {
		ClientID string `json:"clientId"`
	}
	if err := json.Unmarshal([]byte(connected.data), &connection); err != nil || connection.ClientID == "" {
		t.Fatalf("connect=%s err=%v", connected.data, err)
	}
	rawTopic := "@topic/한 글? #"
	topic, err := api.ApplicationRealtimeTopic(rawTopic)
	if err != nil {
		t.Fatal(err)
	}
	replaceMultiplexSubscriptions(t, fixture, connection.ClientID, "", nil, "", []string{topic})

	directory := t.TempDir()
	if err := os.WriteFile(filepath.Join(directory, "pgbase.scripts.json"), []byte(`{"format":"pgbase.scripts/v1","routes":[{"method":"POST","pattern":"/publish","access":"admin","handler":{"module":"route.js","export":"run"},"capabilities":["app-control"]}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "route.js"), []byte(fmt.Sprintf(`exports.run=()=> $app.subscriptionsBroker().publishApplication(%q, ["ready",true,3])`, rawTopic)), 0o600); err != nil {
		t.Fatal(err)
	}
	bundle, err := jsvm.LoadBundle(directory)
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := jsvm.NewRuntime(bundle, jsvm.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close(context.Background())
	host := &extensionHost{bridge: fixture.app.extensions, principal: identity.Subject{Kind: identity.Administrator, ID: uuid.New()}, requestID: "broker-custom-publish", valid: true}
	output, err := runtime.Invoke(t.Context(), jsvm.Invocation{Handler: bundle.Manifest().Routes[0].Handler, Capabilities: []string{"app-control"}, Input: json.RawMessage(`{}`), Host: host})
	if err != nil || string(output) != `{"delivered":1}` {
		t.Fatalf("publish=%s err=%v", output, err)
	}
	message := nextStreamEvent(t, events, topic)
	if message.data != `["ready",true,3]` {
		t.Fatalf("arbitrary custom payload=%s", message.data)
	}
}
