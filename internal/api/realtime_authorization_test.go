package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/yakisoba0728/PgBase/internal/identity"
	"github.com/yakisoba0728/PgBase/internal/recordaccess"
	"github.com/yakisoba0728/PgBase/internal/userauth"
)

func TestRealtimeQueuedApplicationMessagesRespectUnsubscribeAndRevocation(t *testing.T) {
	for _, change := range []string{"unsubscribe", "revoke"} {
		t.Run(change, func(t *testing.T) {
			id := uuid.New()
			auth := &realtimeAuthStub{principal: userauth.Principal{User: userauth.User{ID: id}}}
			broker := NewApplicationRealtimeBroker()
			hub := newRealtimeHub(Dependencies{Realtime: &replayRealtime{}, UserAuth: auth, ApplicationRealtimeBroker: broker})
			client := &realtimeClient{id: "queued", principal: identity.Subject{Kind: identity.User, ID: id}, token: "before", subscriptions: []realtimeSubscription{{raw: "@app/private", application: true}}}
			hub.clients[client.id] = client
			queue := broker.connect(client.id)
			broker.subscribe(client.id, []string{"@app/private"})
			if broker.Publish(t.Context(), "@app/private", json.RawMessage(`{"private":true}`)) != 1 {
				t.Fatal("fixture did not queue authorized message")
			}
			if change == "unsubscribe" {
				client.subscriptions = nil
				broker.subscribe(client.id, nil)
			} else {
				auth.err = userauth.ErrInvalidCredentials
			}
			writer := httptest.NewRecorder()
			err := hub.deliverApplicationMessage(writer, httptest.NewRequest("GET", "/api/realtime", nil), http.NewResponseController(writer), client, <-queue)
			if change == "revoke" && !errors.Is(err, recordaccess.ErrDenied) {
				t.Fatalf("revoked delivery=%v", err)
			}
			if change == "unsubscribe" && err != nil {
				t.Fatal(err)
			}
			if writer.Body.Len() != 0 {
				t.Fatalf("stale queued message written after %s", change)
			}
		})
	}
}

type rotatedRealtimeAuth struct {
	realtimeAuthStub
	accepted string
}

func (a *rotatedRealtimeAuth) Authenticate(ctx context.Context, token string) (userauth.Principal, error) {
	if token != a.accepted {
		return userauth.Principal{}, userauth.ErrInvalidCredentials
	}
	return a.realtimeAuthStub.Authenticate(ctx, token)
}

func TestRealtimeMultiplexReauthenticationPreservesReplayAndUsesNewCredential(t *testing.T) {
	id := uuid.New()
	auth := &rotatedRealtimeAuth{realtimeAuthStub: realtimeAuthStub{principal: userauth.Principal{User: userauth.User{ID: id}}}, accepted: "rotated"}
	hub := newRealtimeHub(Dependencies{Realtime: &sessionRealtimeStub{head: 91}, UserAuth: auth})
	cursor := int64(37)
	client := &realtimeClient{id: "reauth", address: "192.0.2.1", admittedUser: id, principal: identity.Subject{Kind: identity.User, ID: id}, token: "expired", subscriptions: []realtimeSubscription{{raw: "notes", collection: "notes", cursor: &cursor}}}
	hub.clients[client.id] = client
	hub.userClients[id] = 1
	request := httptest.NewRequest(http.MethodPost, "/api/realtime", bytes.NewBufferString(`{"clientId":"reauth","subscriptions":["notes"]}`))
	request.RemoteAddr = "192.0.2.1:9999"
	request.Header.Set("Content-Type", "application/json")
	request = request.WithContext(context.WithValue(request.Context(), userPrincipalKey{}, authenticatedUser{principal: auth.principal, token: "rotated"}))
	response := httptest.NewRecorder()
	hub.replace(response, request)
	if response.Code != http.StatusNoContent {
		t.Fatalf("reauth status=%d body=%s", response.Code, response.Body.String())
	}
	if client.token != "rotated" || *client.subscriptions[0].cursor != 37 || hub.userClients[id] != 1 || !hub.revalidate(t.Context(), client) {
		t.Fatal("reauth lost replay position, admission, or current credential")
	}
}
