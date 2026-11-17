package userauth

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
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

func TestOAuthAvatarBoundsRedirects(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := requests.Add(1)
		// A finite chain keeps the regression safe before the fix. The default
		// http.Client limit does not apply once CheckRedirect is overridden.
		if n <= 20 {
			http.Redirect(w, r, "/avatar?hop="+strconv.Itoa(int(n)), http.StatusFound)
			return
		}
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write([]byte("invalid image"))
	}))
	defer server.Close()
	if err := (&Service{}).importOAuthAvatar(context.Background(), uuid.New(), server.URL, true); err == nil {
		t.Fatal("invalid redirected avatar accepted")
	}
	if got := requests.Load(); got != 10 {
		t.Fatalf("avatar followed %d requests, want initial request plus nine redirects", got)
	}
}

func TestOAuthAvatarAllowsShortRedirectChain(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := requests.Add(1)
		if n <= 2 {
			http.Redirect(w, r, "/avatar?hop="+strconv.Itoa(int(n)), http.StatusFound)
			return
		}
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write([]byte("invalid image"))
	}))
	defer server.Close()
	err := (&Service{}).importOAuthAvatar(context.Background(), uuid.New(), server.URL, true)
	if err == nil || err.Error() != "avatar image rejected" {
		t.Fatalf("short redirect chain did not reach image validation: %v", err)
	}
	if got := requests.Load(); got != 3 {
		t.Fatalf("short chain made %d requests, want three", got)
	}
}
