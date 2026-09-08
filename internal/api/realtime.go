package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/webisnoteasy/PgBase/internal/realtime"
	"github.com/webisnoteasy/PgBase/internal/recordaccess"
	"github.com/webisnoteasy/PgBase/internal/userauth"
)

type Realtime interface {
	Read(context.Context, uuid.UUID, string, *int64) ([]realtime.Event, int64, error)
}

// Admission is bounded globally and per user; all memory remains bounded even
// when clients hold idle connections or stop reading from their sockets.
func realtimeHandler(dependencies Dependencies) http.Handler {
	var mu sync.Mutex
	users := make(map[uuid.UUID]int)
	total := 0
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		current, ok := r.Context().Value(userPrincipalKey{}).(authenticatedUser)
		if !ok {
			writeUserAuthError(w, r, userauth.ErrInvalidCredentials)
			return
		}
		id := current.principal.User.ID
		mu.Lock()
		admitted := total < 128 && users[id] < 4
		if admitted {
			total++
			users[id]++
		}
		mu.Unlock()
		if !admitted {
			writeAPIError(w, r, APIError{Status: 429, Code: "realtime_limit", Message: "Too many realtime connections"})
			return
		}
		defer func() {
			mu.Lock()
			total--
			users[id]--
			if users[id] == 0 {
				delete(users, id)
			}
			mu.Unlock()
		}()
		cursor, err := realtimeCursor(r)
		if err != nil {
			writeRealtimeError(w, r, err)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 30*time.Minute)
		defer cancel()
		events, next, err := dependencies.Realtime.Read(ctx, id, r.PathValue("collection"), cursor)
		if err != nil {
			writeRealtimeError(w, r, err)
			return
		}
		control := http.NewResponseController(w)
		if err := control.SetWriteDeadline(time.Now().Add(10 * time.Second)); err != nil {
			writeRealtimeError(w, r, err)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Accel-Buffering", "no")
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			// Revalidate immediately before delivery, including a logout that happened
			// while the database batch was being read.
			principal, err := dependencies.UserAuth.Authenticate(ctx, current.token)
			if err != nil || principal.User.ID != id {
				return
			}
			if err := control.SetWriteDeadline(time.Now().Add(10 * time.Second)); err != nil {
				return
			}
			for _, event := range events {
				payload, err := json.Marshal(event)
				if err != nil {
					return
				}
				if _, err := fmt.Fprintf(w, "id: %d\nevent: record\ndata: %s\n\n", event.Sequence, payload); err != nil {
					return
				}
			}
			if _, err := fmt.Fprintf(w, "id: %d\nevent: cursor\ndata: {}\n\n", next); err != nil {
				return
			}
			if err := control.Flush(); err != nil {
				return
			}
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
			cursor = &next
			events, next, err = dependencies.Realtime.Read(ctx, id, r.PathValue("collection"), cursor)
			if err != nil {
				return
			}
		}
	})
}

func realtimeCursor(r *http.Request) (*int64, error) {
	if r.URL.RawQuery != "" {
		return nil, realtime.ErrCursorInvalid
	}
	values := r.Header.Values("Last-Event-ID")
	if len(values) == 0 {
		return nil, nil
	}
	if len(values) != 1 || values[0] == "" || len(values[0]) > 19 {
		return nil, realtime.ErrCursorInvalid
	}
	for _, digit := range values[0] {
		if digit < '0' || digit > '9' {
			return nil, realtime.ErrCursorInvalid
		}
	}
	parsed, err := strconv.ParseInt(values[0], 10, 64)
	if err != nil {
		return nil, realtime.ErrCursorInvalid
	}
	return &parsed, nil
}

func writeRealtimeError(w http.ResponseWriter, r *http.Request, err error) {
	response := APIError{Status: 503, Code: "realtime_unavailable", Message: "Realtime is unavailable"}
	switch {
	case errors.Is(err, realtime.ErrCursorExpired):
		response = APIError{Status: 410, Code: "cursor_expired", Message: "Reload records before reconnecting"}
	case errors.Is(err, realtime.ErrCursorInvalid):
		response = APIError{Status: 400, Code: "invalid_cursor", Message: "Invalid realtime cursor"}
	case errors.Is(err, recordaccess.ErrDenied):
		response = APIError{Status: 403, Code: "access_denied", Message: "Collection access denied"}
	}
	writeAPIError(w, r, response)
}
