package userauth

import (
	"context"
	"github.com/google/uuid"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestOAuthAvatarClosesItsIdleTransport(t *testing.T) {
	closed := make(chan struct{}, 1)
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write([]byte("invalid image"))
	}))
	server.Config.ConnState = func(_ net.Conn, state http.ConnState) {
		if state == http.StateClosed {
			select {
			case closed <- struct{}{}:
			default:
			}
		}
	}
	server.Start()
	defer server.Close()
	if err := (&Service{}).importOAuthAvatar(context.Background(), uuid.New(), server.URL, true); err == nil {
		t.Fatal("invalid image accepted")
	}
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Error("avatar transport retained idle connection")
	}
}
