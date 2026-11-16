package api

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/yakisoba0728/PgBase/internal/identity"
	"github.com/yakisoba0728/PgBase/internal/realtime"
	"github.com/yakisoba0728/PgBase/internal/recordops"
)

type relationFilterReplay struct{ replayRealtime }

func (s *relationFilterReplay) ReadFor(ctx context.Context, request realtime.ReadRequest) ([]realtime.Event, int64, error) {
	if request.Filter != "" {
		return nil, 0, realtime.ErrRelationFilterRequiresCurrentRecord
	}
	return s.replayRealtime.ReadFor(ctx, request)
}

type deadlineReplayRecorder struct{ *httptest.ResponseRecorder }

func (*deadlineReplayRecorder) SetWriteDeadline(time.Time) error { return nil }

// A successful fallback read followed by a failed current-record lookup must
// not acknowledge the event. Otherwise reconnecting at the cursor frame skips
// a matching event permanently, even after the database recovers.
func TestRealtimeRelationFilterFailurePreservesCheckpoint(t *testing.T) {
	for _, failure := range []struct {
		name string
		err  error
	}{
		{"pool_deadline", context.DeadlineExceeded},
		{"connection_loss", &pgconn.PgError{Code: "08006", Message: "synthetic connection failure"}},
	} {
		t.Run(failure.name, func(t *testing.T) {
			stream := &relationFilterReplay{}
			stream.seed("notes", 10)
			records := &fakeRecordService{listErr: failure.err}
			hub := newRealtimeHub(Dependencies{Realtime: stream, Records: records})
			cursor := int64(0)
			client := &realtimeClient{principal: identity.Subject{Kind: identity.Anonymous}, subscriptions: []realtimeSubscription{{raw: "notes?filter=owner.active=true", collection: "notes", filter: "owner.active=true", cursor: &cursor}}}
			request := httptest.NewRequest(http.MethodGet, "/api/realtime", nil)
			failed := &deadlineReplayRecorder{httptest.NewRecorder()}
			err := hub.deliverRecordMessages(failed, request, http.NewResponseController(failed), client)
			if !errors.Is(err, failure.err) {
				t.Fatalf("current-record failure was lost: error=%v body=%q cursor=%d", err, failed.Body.String(), *client.subscriptions[0].cursor)
			}
			if *client.subscriptions[0].cursor != 0 || strings.Contains(failed.Body.String(), "id:") {
				t.Fatalf("failed lookup acknowledged event: body=%q cursor=%d", failed.Body.String(), *client.subscriptions[0].cursor)
			}
			if !strings.Contains(failed.Body.String(), `"code":"subscription_failed"`) || strings.Contains(failed.Body.String(), failure.err.Error()) {
				t.Fatalf("failed lookup did not emit a sanitized stream error: %q", failed.Body.String())
			}
			records.listErr = nil
			records.page = recordops.RecordPage{Items: []map[string]any{{"id": "10"}}}
			recovered := &deadlineReplayRecorder{httptest.NewRecorder()}
			if err := hub.deliverRecordMessages(recovered, request, http.NewResponseController(recovered), client); err != nil {
				t.Fatal(err)
			}
			if *client.subscriptions[0].cursor != 10 || !strings.Contains(recovered.Body.String(), `"id":"10"`) {
				t.Fatalf("recovery lost matching record: body=%q cursor=%d", recovered.Body.String(), *client.subscriptions[0].cursor)
			}
		})
	}
}

func TestRealtimeRelationFilterNonmatchStillAdvancesCheckpoint(t *testing.T) {
	stream := &relationFilterReplay{}
	stream.seed("notes", 10)
	hub := newRealtimeHub(Dependencies{Realtime: stream, Records: &fakeRecordService{}})
	cursor := int64(0)
	client := &realtimeClient{principal: identity.Subject{Kind: identity.Anonymous}, subscriptions: []realtimeSubscription{{raw: "notes?filter=owner.active=true", collection: "notes", filter: "owner.active=true", cursor: &cursor}}}
	request := httptest.NewRequest(http.MethodGet, "/api/realtime", nil)
	response := &deadlineReplayRecorder{httptest.NewRecorder()}
	if err := hub.deliverRecordMessages(response, request, http.NewResponseController(response), client); err != nil {
		t.Fatal(err)
	}
	if *client.subscriptions[0].cursor != 10 || strings.Contains(response.Body.String(), "event: notes") {
		t.Fatalf("genuine nonmatch did not acknowledge safely: body=%q cursor=%d", response.Body.String(), *client.subscriptions[0].cursor)
	}
}
