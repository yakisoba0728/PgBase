//go:build integration

package pgbase

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/yakisoba0728/PgBase/internal/identity"
	"github.com/yakisoba0728/PgBase/internal/jsvm"
)

func TestJavaScriptApplicationBrokerDeliversThroughRealtimeHTTP(t *testing.T) {
	fixture := newPublicUsersApp(t, false)
	cookie, csrf := setupPublicUsersAdministrator(t, fixture)
	createPublicUsersCollection(t, fixture, cookie, csrf, "broker_notes")
	client := &http.Client{Timeout: 8 * time.Second}
	request, _ := http.NewRequest(http.MethodGet, fixture.server.URL+"/api/realtime", nil)
	request.Host = "pgbase.test"
	request.AddCookie(cookie)
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	reader := bufio.NewReader(response.Body)
	var clientID string
	for clientID == "" {
		line, err := reader.ReadString('\n')
		if err != nil {
			t.Fatal(err)
		}
		if strings.HasPrefix(line, "data: ") {
			var connected struct {
				ClientID string `json:"clientId"`
			}
			_ = json.Unmarshal([]byte(strings.TrimSpace(strings.TrimPrefix(line, "data: "))), &connected)
			clientID = connected.ClientID
		}
	}
	body, _ := json.Marshal(map[string]any{"clientId": clientID, "subscriptions": []string{"broker_notes"}})
	subscribe, _ := http.NewRequest(http.MethodPost, fixture.server.URL+"/api/realtime", bytes.NewReader(body))
	subscribe.Host = "pgbase.test"
	subscribe.Header.Set("Origin", publicUsersOrigin)
	subscribe.Header.Set("Content-Type", "application/json")
	subscribe.Header.Set("X-PgBase-CSRF", csrf)
	subscribe.AddCookie(cookie)
	subscriptionResponse, err := client.Do(subscribe)
	if err != nil || subscriptionResponse.StatusCode != http.StatusNoContent {
		t.Fatalf("subscribe status=%v err=%v", subscriptionResponse.StatusCode, err)
	}
	subscriptionResponse.Body.Close()

	directory := t.TempDir()
	if err := os.WriteFile(filepath.Join(directory, "pgbase.scripts.json"), []byte(`{"format":"pgbase.scripts/v1","routes":[{"method":"POST","pattern":"/publish","access":"admin","handler":{"module":"route.js","export":"run"},"capabilities":["app-control"]}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "route.js"), []byte(`exports.run=()=> $app.subscriptionsBroker().publish("broker_notes", {ok:true})`), 0o600); err != nil {
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
	output, err := runtime.Invoke(t.Context(), jsvm.Invocation{Handler: bundle.Manifest().Routes[0].Handler, Capabilities: []string{"app-control"}, Host: host})
	if err != nil || string(output) != `{"delivered":1}` {
		t.Fatalf("publish=%s err=%v", output, err)
	}
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			t.Fatal(err)
		}
		if strings.TrimSpace(line) == "event: broker_notes" {
			data, err := reader.ReadString('\n')
			if err != nil || strings.TrimSpace(data) != `data: {"ok":true}` {
				t.Fatalf("data=%q err=%v", data, err)
			}
			break
		}
	}
	response.Body.Close()
	deadline := time.Now().Add(time.Second)
	for len(fixture.app.realtimeBroker.Clients()) != 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if got := fixture.app.realtimeBroker.Clients(); len(got) != 0 {
		t.Fatalf("connected clients after close=%v", got)
	}
}
