package api

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/yakisoba0728/PgBase/internal/identity"
	"github.com/yakisoba0728/PgBase/internal/recordaccess"
	"github.com/yakisoba0728/PgBase/internal/userauth"
)

// An idle poll authenticates once; any poll that writes a record or moves a
// checkpoint still rechecks the credential after its read.
func TestRealtimeIdlePollAuthenticatesOnceAndBatchesStillRecheck(t *testing.T) {
	id := uuid.New()
	auth := &realtimeAuthStub{principal: userauth.Principal{User: userauth.User{ID: id}}}
	stream := &replayRealtime{}
	stream.seed("notes", 1, 2)
	hub := newRealtimeHub(Dependencies{Realtime: stream, UserAuth: auth})
	two := int64(2)
	client := &realtimeClient{id: "idle", principal: identity.Subject{Kind: identity.User, ID: id}, token: "private-token",
		subscriptions: []realtimeSubscription{{raw: "notes", collection: "notes", cursor: &two}}}
	poll := func() (string, int, error) {
		t.Helper()
		writer := &disconnectWriter{ResponseRecorder: httptest.NewRecorder(), cancel: func() {}}
		request := httptest.NewRequest(http.MethodGet, "http://pgbase.test/api/realtime", nil)
		auth.mu.Lock()
		before := auth.calls
		auth.mu.Unlock()
		err := hub.deliverRecordMessages(writer, request, http.NewResponseController(writer), client)
		auth.mu.Lock()
		defer auth.mu.Unlock()
		return writer.Body.String(), auth.calls - before, err
	}

	body, calls, err := poll()
	if err != nil || body != "id: 2\nevent: cursor\ndata: {}\n\n" || calls != 1 {
		t.Fatalf("idle poll body=%q authentications=%d err=%v", body, calls, err)
	}

	// Another collection's event moves the shared head past this topic's
	// checkpoint without a visible message; that advance is still rechecked.
	stream.seed("other", 3)
	body, calls, err = poll()
	if err != nil || body != "id: 3\nevent: cursor\ndata: {}\n\n" || calls != 2 {
		t.Fatalf("advancing poll body=%q authentications=%d err=%v", body, calls, err)
	}

	stream.seed("notes", 4)
	body, calls, err = poll()
	if err != nil || !strings.Contains(body, "event: notes\n") || calls < 3 {
		t.Fatalf("message poll body=%q authentications=%d err=%v", body, calls, err)
	}

	// A revocation is still observed by the next poll, before any read.
	reads := stream.reads
	auth.mu.Lock()
	auth.err = userauth.ErrInvalidCredentials
	auth.mu.Unlock()
	body, calls, err = poll()
	if !errors.Is(err, recordaccess.ErrDenied) || body != "" || calls != 1 || stream.reads != reads {
		t.Fatalf("revoked poll body=%q authentications=%d reads=%d->%d err=%v", body, calls, reads, stream.reads, err)
	}
}
