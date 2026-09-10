package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/yakisoba0728/PgBase/internal/identity"
	"github.com/yakisoba0728/PgBase/internal/query"
	"github.com/yakisoba0728/PgBase/internal/realtime"
	"github.com/yakisoba0728/PgBase/internal/recordops"
)

type realtimeV2 interface {
	ReadFor(context.Context, realtime.ReadRequest) ([]realtime.Event, int64, error)
}

type realtimeSubscription struct {
	raw, collection, filter, expand, fields string
	recordID                                uuid.UUID
	cursor                                  *int64
}
type realtimeClient struct {
	mu                 sync.RWMutex
	deliveryMu         sync.Mutex
	id, address, token string
	principal          identity.Subject
	subscriptions      []realtimeSubscription
	resume             *int64
}
type realtimeHub struct {
	mu           sync.RWMutex
	clients      map[string]*realtimeClient
	dependencies Dependencies
}

func newRealtimeHub(dependencies Dependencies) *realtimeHub {
	return &realtimeHub{clients: map[string]*realtimeClient{}, dependencies: dependencies}
}

func realtimeSessionRoutes(dependencies Dependencies) []route {
	if dependencies.Realtime == nil {
		return nil
	}
	hub := newRealtimeHub(dependencies)
	handler := optionalRealtimeAdmission(dependencies, http.HandlerFunc(hub.serve))
	return []route{
		{method: http.MethodGet, pattern: "/api/realtime", access: accessPublic, response: passthrough(), deadline: deadlineDefault, handler: handler},
		{method: http.MethodPost, pattern: "/api/realtime", access: accessPublic, response: atomicJSON(maximumAdminResponseBytes), deadline: deadlineDefault, maximumRequestBytes: maximumCredentialRequestBytes, handler: handler},
	}
}

func optionalRealtimeAdmission(d Dependencies, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cookieName := adminCookieNameForRequest(r, d.PublicURL, d.trustedProxies())
		if _, err := r.Cookie(cookieName); err == nil {
			middleware := adminSameOriginReadMiddleware(d.AdminAuth, d.PublicURL, d.trustedProxies, next)
			if r.Method == http.MethodPost {
				middleware = adminSessionMiddleware(d.AdminAuth, d.PublicURL, d.trustedProxies, next)
			}
			middleware.ServeHTTP(w, r)
			return
		}
		values := r.Header.Values("Authorization")
		if len(values) == 0 {
			anonymousSessionMiddleware(next).ServeHTTP(w, r)
			return
		}
		if len(values) != 1 {
			writeAPIError(w, r, APIError{Status: 401, Code: "authentication_required", Message: "Authentication failed"})
			return
		}
		parts := strings.Fields(values[0])
		if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
			writeAPIError(w, r, APIError{Status: 401, Code: "authentication_required", Message: "Authentication failed"})
			return
		}
		if d.UserAuth != nil {
			if p, err := d.UserAuth.Authenticate(r.Context(), parts[1]); err == nil {
				ctx := context.WithValue(r.Context(), userPrincipalKey{}, authenticatedUser{principal: p, token: parts[1]})
				next.ServeHTTP(w, r.WithContext(ctx))
				return
			}
		}
		serviceSessionMiddleware(d.ServiceTokens, next).ServeHTTP(w, r)
	})
}

func realtimeSubject(r *http.Request) identity.Subject {
	if user, ok := r.Context().Value(userPrincipalKey{}).(authenticatedUser); ok {
		return identity.Subject{Kind: identity.User, ID: user.principal.User.ID}
	}
	if admin, ok := AdminPrincipalFromContext(r.Context()); ok {
		return identity.Subject{Kind: identity.Administrator, ID: admin.Administrator.ID}
	}
	if subject, ok := r.Context().Value(publicRecordPrincipalKey{}).(identity.Subject); ok {
		return subject
	}
	return identity.Subject{Kind: identity.Anonymous}
}

func (h *realtimeHub) serve(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		h.replace(w, r)
		return
	}
	h.connect(w, r)
}

func (h *realtimeHub) connect(w http.ResponseWriter, r *http.Request) {
	cursor, err := realtimeCursor(r)
	if err != nil {
		writeRealtimeError(w, r, err)
		return
	}
	address, _ := clientIP(r, h.dependencies.trustedProxies())
	client := &realtimeClient{id: uuid.NewString(), address: address, principal: realtimeSubject(r), token: realtimeCredential(r), resume: cursor}
	lifecycle := &RealtimeLifecycleEvent{UserID: client.principal.ID, Principal: client.principal, ClientID: client.id, Cursor: cursor}
	finish, err := beginRealtime(h.dependencies.RealtimeLifecycle, r.Context(), "connect", lifecycle)
	if err != nil {
		finish(err)
		writeRealtimeError(w, r, err)
		return
	}
	defer func() { finish(nil) }()
	h.mu.Lock()
	if len(h.clients) >= 128 {
		h.mu.Unlock()
		writeAPIError(w, r, APIError{Status: 429, Code: "realtime_limit", Message: "Too many realtime connections"})
		return
	}
	h.clients[client.id] = client
	h.mu.Unlock()
	defer func() { h.mu.Lock(); delete(h.clients, client.id); h.mu.Unlock() }()
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Accel-Buffering", "no")
	control := http.NewResponseController(w)
	if _, err := fmt.Fprintf(w, "id: %s\nevent: PB_CONNECT\ndata: {\"clientId\":%q}\n\n", client.id, client.id); err != nil {
		return
	}
	if err := control.Flush(); err != nil {
		return
	}
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
		}
		client.deliveryMu.Lock()
		if !h.revalidate(r.Context(), client) {
			client.deliveryMu.Unlock()
			return
		}
		client.mu.RLock()
		subscriptions := append([]realtimeSubscription(nil), client.subscriptions...)
		principal := client.principal
		client.mu.RUnlock()
		for index := range subscriptions {
			sub := &subscriptions[index]
			if sub.cursor == nil && cursor != nil {
				value := *cursor
				sub.cursor = &value
			}
			reader, ok := h.dependencies.Realtime.(realtimeV2)
			if !ok {
				client.deliveryMu.Unlock()
				return
			}
			events, next, err := reader.ReadFor(r.Context(), realtime.ReadRequest{Principal: principal, Collection: sub.collection, RecordID: sub.recordID, Filter: sub.filter, Fields: sub.fields, After: sub.cursor, RuleContext: ruleRequestContext(r)})
			relationFilter := errors.Is(err, realtime.ErrRelationFilterRequiresCurrentRecord)
			if relationFilter {
				events, next, err = reader.ReadFor(r.Context(), realtime.ReadRequest{Principal: principal, Collection: sub.collection, RecordID: sub.recordID, After: sub.cursor, RuleContext: ruleRequestContext(r)})
			}
			if err != nil {
				payload, _ := json.Marshal(map[string]string{"code": "subscription_failed"})
				_, _ = fmt.Fprintf(w, "event: PB_ERROR\ndata: %s\n\n", payload)
				_ = control.Flush()
				client.deliveryMu.Unlock()
				return
			}
			for _, event := range events {
				if relationFilter {
					if event.Operation == "delete" {
						continue
					}
					page, listErr := h.dependencies.Records.List(r.Context(), recordops.ListRequest{Principal: principal, RequestID: requestID(r.Context()), Collection: event.Collection, Limit: 1, Filter: "(" + sub.filter + ") && id = '" + event.RecordID.String() + "'", RuleContext: ruleRequestContext(r)})
					if listErr != nil || len(page.Items) == 0 {
						continue
					}
				}
				record := event.Record
				if sub.expand != "" && event.Operation != "delete" && h.dependencies.Records != nil {
					current, getErr := h.dependencies.Records.Get(r.Context(), recordops.GetRequest{Principal: principal, RequestID: requestID(r.Context()), Collection: event.Collection, ID: event.RecordID, Expand: sub.expand, Fields: sub.fields})
					if getErr == nil {
						if expanded, ok := current.Data["expand"]; ok {
							record["expand"] = expanded
						}
					}
				}
				if sub.fields != "" {
					projection, compileErr := query.CompileProjection(sub.fields)
					if compileErr != nil {
						client.deliveryMu.Unlock()
						return
					}
					projected, ok := projection.Apply(record).(map[string]any)
					if !ok {
						client.deliveryMu.Unlock()
						return
					}
					record = projected
				}
				messageLifecycle := &RealtimeLifecycleEvent{UserID: principal.ID, Principal: principal, ClientID: client.id, Collection: event.Collection, Event: &event}
				messageFinish, messageErr := beginRealtime(h.dependencies.RealtimeLifecycle, r.Context(), "message", messageLifecycle)
				if messageErr != nil {
					messageFinish(messageErr)
					client.deliveryMu.Unlock()
					return
				}
				payload, _ := json.Marshal(map[string]any{"action": event.Action, "record": record})
				if _, err := fmt.Fprintf(w, "id: %d\nevent: %s\ndata: %s\n\n", event.Sequence, sub.raw, payload); err != nil {
					messageFinish(err)
					client.deliveryMu.Unlock()
					return
				}
				messageFinish(nil)
			}
			value := next
			sub.cursor = &value
		}
		client.mu.Lock()
		for i := range client.subscriptions {
			for _, updated := range subscriptions {
				if client.subscriptions[i].raw == updated.raw {
					client.subscriptions[i].cursor = updated.cursor
				}
			}
		}
		client.mu.Unlock()
		if err := control.Flush(); err != nil {
			client.deliveryMu.Unlock()
			return
		}
		client.deliveryMu.Unlock()
	}
}

func (h *realtimeHub) replace(w http.ResponseWriter, r *http.Request) {
	var form struct {
		ClientID      string   `json:"clientId"`
		Subscriptions []string `json:"subscriptions"`
	}
	if decodeStrictJSON(r, &form) != nil || form.ClientID == "" || len(form.Subscriptions) > 1000 {
		writeAPIError(w, r, APIError{Status: 400, Code: "invalid_request", Message: "Invalid realtime subscription"})
		return
	}
	h.mu.RLock()
	client := h.clients[form.ClientID]
	h.mu.RUnlock()
	if client == nil {
		writeAPIError(w, r, APIError{Status: 404, Code: "realtime_client_not_found", Message: "Missing or invalid client id"})
		return
	}
	client.deliveryMu.Lock()
	defer client.deliveryMu.Unlock()
	principal := realtimeSubject(r)
	address, _ := clientIP(r, h.dependencies.trustedProxies())
	client.mu.RLock()
	clientAddress, currentPrincipal := client.address, client.principal
	client.mu.RUnlock()
	if clientAddress != address {
		writeAPIError(w, r, APIError{Status: 400, Code: "invalid_realtime_client", Message: "Invalid realtime client"})
		return
	}
	if currentPrincipal.Kind != identity.Anonymous && currentPrincipal != principal {
		writeAPIError(w, r, APIError{Status: 403, Code: "realtime_authorization_mismatch", Message: "Realtime authorization does not match"})
		return
	}
	subscriptions := make([]realtimeSubscription, 0, len(form.Subscriptions))
	seen := map[string]bool{}
	for _, raw := range form.Subscriptions {
		sub, err := parseRealtimeSubscription(raw)
		if err != nil || seen[raw] {
			writeAPIError(w, r, APIError{Status: 400, Code: "invalid_subscription", Message: "Invalid realtime subscription"})
			return
		}
		seen[raw] = true
		subscriptions = append(subscriptions, sub)
	}
	lifecycle := &RealtimeLifecycleEvent{UserID: principal.ID, Principal: principal, ClientID: client.id, Subscriptions: append([]string(nil), form.Subscriptions...)}
	finish, hookErr := beginRealtime(h.dependencies.RealtimeLifecycle, r.Context(), "subscribe", lifecycle)
	if hookErr != nil {
		finish(hookErr)
		writeRealtimeError(w, r, hookErr)
		return
	}
	defer func() { finish(nil) }()
	reader, ok := h.dependencies.Realtime.(realtimeV2)
	if !ok {
		writeAPIError(w, r, APIError{Status: 503, Code: "realtime_unavailable", Message: "Realtime is unavailable"})
		return
	}
	client.mu.RLock()
	resume := client.resume
	previous := make(map[string]*int64, len(client.subscriptions))
	if currentPrincipal == principal {
		for _, subscription := range client.subscriptions {
			previous[subscription.raw] = cloneRealtimeCursor(subscription.cursor)
		}
	}
	client.mu.RUnlock()
	for index := range subscriptions {
		// Always validate against the current schema and policy before replacing
		// client state, even when a reconnect cursor is already available.
		_, head, err := reader.ReadFor(r.Context(), realtime.ReadRequest{Principal: principal, Collection: subscriptions[index].collection, RecordID: subscriptions[index].recordID, Filter: subscriptions[index].filter, Fields: subscriptions[index].fields, RuleContext: ruleRequestContext(r)})
		if errors.Is(err, realtime.ErrRelationFilterRequiresCurrentRecord) {
			_, head, err = reader.ReadFor(r.Context(), realtime.ReadRequest{Principal: principal, Collection: subscriptions[index].collection, RecordID: subscriptions[index].recordID, RuleContext: ruleRequestContext(r)})
		}
		if err != nil {
			writeRealtimeError(w, r, err)
			return
		}
		if subscriptions[index].expand != "" && h.dependencies.Records != nil {
			_, err = h.dependencies.Records.List(r.Context(), recordops.ListRequest{Principal: principal, RequestID: requestID(r.Context()), Collection: subscriptions[index].collection, Limit: 1, Filter: subscriptions[index].filter, Expand: subscriptions[index].expand, Fields: subscriptions[index].fields, RuleContext: ruleRequestContext(r)})
			if err != nil {
				writeRealtimeError(w, r, err)
				return
			}
		}
		if cursor, exists := previous[subscriptions[index].raw]; exists {
			subscriptions[index].cursor = cloneRealtimeCursor(cursor)
		} else if resume != nil {
			value := *resume
			subscriptions[index].cursor = &value
		} else {
			subscriptions[index].cursor = &head
		}
	}
	client.mu.Lock()
	client.principal = principal
	client.token = realtimeCredential(r)
	client.subscriptions = subscriptions
	client.resume = nil
	client.mu.Unlock()
	w.WriteHeader(http.StatusNoContent)
}

func cloneRealtimeCursor(cursor *int64) *int64 {
	if cursor == nil {
		return nil
	}
	value := *cursor
	return &value
}

func realtimeCredential(r *http.Request) string {
	if user, ok := r.Context().Value(userPrincipalKey{}).(authenticatedUser); ok {
		return user.token
	}
	if admin, ok := AdminPrincipalFromContext(r.Context()); ok {
		return admin.RawSession
	}
	parts := strings.Fields(r.Header.Get("Authorization"))
	if len(parts) == 2 {
		return parts[1]
	}
	return ""
}

func (h *realtimeHub) revalidate(ctx context.Context, client *realtimeClient) bool {
	client.mu.RLock()
	principal, token := client.principal, client.token
	client.mu.RUnlock()
	switch principal.Kind {
	case identity.Anonymous:
		return true
	case identity.User:
		p, err := h.dependencies.UserAuth.Authenticate(ctx, token)
		return err == nil && p.User.ID == principal.ID
	case identity.Service:
		p, err := h.dependencies.ServiceTokens.Authenticate(ctx, token)
		return err == nil && p.Subject == principal
	case identity.Administrator:
		p, err := h.dependencies.AdminAuth.Authenticate(ctx, token)
		return err == nil && p.Administrator.ID == principal.ID
	default:
		return false
	}
}

func parseRealtimeSubscription(raw string) (realtimeSubscription, error) {
	if raw == "" || len(raw) > 2500 {
		return realtimeSubscription{}, errors.New("invalid topic")
	}
	parts := strings.SplitN(raw, "?", 2)
	topic := strings.TrimSuffix(parts[0], "/*")
	segments := strings.Split(topic, "/")
	if len(segments) > 2 || segments[0] == "" {
		return realtimeSubscription{}, errors.New("invalid topic")
	}
	sub := realtimeSubscription{raw: raw, collection: segments[0]}
	if len(segments) == 2 {
		id, err := uuid.Parse(segments[1])
		if err != nil {
			return realtimeSubscription{}, err
		}
		sub.recordID = id
	}
	if len(parts) == 2 {
		values, err := url.ParseQuery(parts[1])
		if err != nil {
			return realtimeSubscription{}, err
		}
		for key := range values {
			if key != "filter" && key != "expand" && key != "fields" {
				return realtimeSubscription{}, errors.New("unknown option")
			}
		}
		sub.filter = values.Get("filter")
		sub.expand = values.Get("expand")
		sub.fields = values.Get("fields")
	}
	return sub, nil
}
