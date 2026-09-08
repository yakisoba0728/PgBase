package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/webisnoteasy/PgBase/internal/realtime"
	"github.com/webisnoteasy/PgBase/internal/userauth"
)

type realtimeAuthStub struct {
	principal userauth.Principal
	mu        sync.Mutex
	calls     int
}

func (*realtimeAuthStub) Register(context.Context, string, string, string) (userauth.Session, error) {
	return userauth.Session{}, nil
}
func (*realtimeAuthStub) Login(context.Context, string, string, string) (userauth.Session, error) {
	return userauth.Session{}, nil
}
func (s *realtimeAuthStub) Authenticate(context.Context, string) (userauth.Principal, error) {
	s.mu.Lock()
	s.calls++
	s.mu.Unlock()
	return s.principal, nil
}
func (*realtimeAuthStub) Logout(context.Context, string) error { return nil }
func (*realtimeAuthStub) ListUsers(context.Context, uuid.UUID, int) (userauth.UserPage, error) {
	return userauth.UserPage{}, nil
}
func (*realtimeAuthStub) SetUserActive(context.Context, uuid.UUID, bool) (userauth.ManagedUser, error) {
	return userauth.ManagedUser{}, nil
}
func (*realtimeAuthStub) RevokeUserSessions(context.Context, uuid.UUID) (int64, error)    { return 0, nil }
func (*realtimeAuthStub) ChangePassword(context.Context, uuid.UUID, string, string) error { return nil }

type blockingRealtime struct{ started chan struct{} }

func (s *blockingRealtime) Read(ctx context.Context, _ uuid.UUID, _ string, _ *int64) ([]realtime.Event, int64, error) {
	s.started <- struct{}{}
	<-ctx.Done()
	return nil, 0, ctx.Err()
}

type immediateRealtime struct{}

func (*immediateRealtime) Read(context.Context, uuid.UUID, string, *int64) ([]realtime.Event, int64, error) {
	return nil, 7, nil
}

type disconnectWriter struct {
	*httptest.ResponseRecorder
	cancel context.CancelFunc
}

func (*disconnectWriter) SetWriteDeadline(time.Time) error { return nil }
func (w *disconnectWriter) FlushError() error              { w.cancel(); return nil }

func realtimeRequest(ctx context.Context, id uuid.UUID) *http.Request {
	request := httptest.NewRequest(http.MethodGet, "http://pgbase.test/api/v1/user/collections/notes/realtime", nil).WithContext(ctx)
	request.SetPathValue("collection", "notes")
	return request.WithContext(context.WithValue(request.Context(), userPrincipalKey{}, authenticatedUser{principal: userauth.Principal{User: userauth.User{ID: id}}, token: "private-token"}))
}

func TestRealtimeAdmissionIsBoundedAndReleasedOnDisconnect(t *testing.T) {
	id := uuid.New()
	auth := &realtimeAuthStub{principal: userauth.Principal{User: userauth.User{ID: id}}}
	stream := &blockingRealtime{started: make(chan struct{}, 4)}
	handler := realtimeHandler(Dependencies{Realtime: stream, UserAuth: auth})
	cancels := make([]context.CancelFunc, 0, 4)
	done := make(chan struct{}, 4)
	for range 4 {
		ctx, cancel := context.WithCancel(context.Background())
		cancels = append(cancels, cancel)
		go func() { handler.ServeHTTP(httptest.NewRecorder(), realtimeRequest(ctx, id)); done <- struct{}{} }()
		<-stream.started
	}
	rejected := httptest.NewRecorder()
	handler.ServeHTTP(rejected, realtimeRequest(context.Background(), id))
	if rejected.Code != http.StatusTooManyRequests {
		t.Fatalf("fifth connection status=%d", rejected.Code)
	}
	for _, cancel := range cancels {
		cancel()
	}
	for range 4 {
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Fatal("disconnected realtime handler did not release")
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	reconnected := httptest.NewRecorder()
	handler.ServeHTTP(reconnected, realtimeRequest(ctx, id))
	if reconnected.Code == http.StatusTooManyRequests {
		t.Fatal("disconnected slots were not released")
	}
}

func TestRealtimeFlushDisconnectStopsBeforePollingAgain(t *testing.T) {
	id := uuid.New()
	auth := &realtimeAuthStub{principal: userauth.Principal{User: userauth.User{ID: id}}}
	ctx, cancel := context.WithCancel(context.Background())
	writer := &disconnectWriter{ResponseRecorder: httptest.NewRecorder(), cancel: cancel}
	realtimeHandler(Dependencies{Realtime: &immediateRealtime{}, UserAuth: auth}).ServeHTTP(writer, realtimeRequest(ctx, id))
	if got := writer.Body.String(); got != "id: 7\nevent: cursor\ndata: {}\n\n" {
		t.Fatalf("stream=%q", got)
	}
	auth.mu.Lock()
	calls := auth.calls
	auth.mu.Unlock()
	if calls != 1 {
		t.Fatalf("authentication calls=%d", calls)
	}
}
