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

	"github.com/yakisoba0728/PgBase/internal/realtime"
	"github.com/yakisoba0728/PgBase/internal/recordops"
)

type realtimeV2 interface {
	ReadFor(context.Context, realtime.ReadRequest) ([]realtime.Event, int64, error)
}

type realtimeSubscription struct {
	raw, collection, filter, expand, fields string
	recordID                                string
	cursor                                  *int64
	application                             bool
}
type realtimeClient struct {
	mu                 sync.RWMutex
	deliveryMu         sync.Mutex
	id, address, token string
	admittedUser       uuid.UUID // guarded by realtimeHub.mu
	principal          identity.Subject
	subscriptions      []realtimeSubscription
	resume             *int64
}
type realtimeHub struct {
	mu           sync.RWMutex
	clients      map[string]*realtimeClient
	userClients  map[uuid.UUID]int
	dependencies Dependencies
}

func newRealtimeHub(dependencies Dependencies) *realtimeHub {
	hub := &realtimeHub{clients: map[string]*realtimeClient{}, userClients: map[uuid.UUID]int{}, dependencies: dependencies}
	if dependencies.ApplicationRealtimeBroker != nil {
		dependencies.ApplicationRealtimeBroker.setAuthorizer(hub.authorizeApplicationMessage)
	}
	return hub
}

type realtimeApplicationBroadcastAuthorizer interface {
	AllowsApplicationBroadcast(context.Context, realtime.ReadRequest) (bool, error)
}

func (h *realtimeHub) authorizeApplicationMessage(ctx context.Context, clientID string, message ApplicationRealtimeMessage) bool {
	h.mu.RLock()
	client := h.clients[clientID]
	h.mu.RUnlock()
	if client == nil || !h.revalidate(ctx, client) {
		return false
	}
	client.mu.RLock()
	principal := client.principal
	client.mu.RUnlock()
	// Application topics have no collection counterpart.  Authentication is
	// still checked immediately before each write, while topic authorization is
	// the explicit app-control capability needed to call Publish.
	if validApplicationRealtimeTopic(message.Topic) {
		return true
	}
	var envelope struct {
		ID     string `json:"id"`
		Record *struct {
			ID string `json:"id"`
		} `json:"record"`
	}
	if json.Unmarshal(message.Data, &envelope) == nil {
		id := envelope.ID
		if envelope.Record != nil {
			id = envelope.Record.ID
		}
		if id != "" && len(id) <= 1024 && h.dependencies.Records != nil {
			_, getErr := h.dependencies.Records.Get(ctx, recordops.GetRequest{Principal: principal, RequestID: "realtime-broker-" + clientID, Collection: message.Topic, PublicID: id})
			return getErr == nil
		}
	}
	if principal.Kind == identity.Administrator {
		return true
	}
	authorizer, ok := h.dependencies.Realtime.(realtimeApplicationBroadcastAuthorizer)
	if !ok {
		return false
	}
	allowed, err := authorizer.AllowsApplicationBroadcast(ctx, realtime.ReadRequest{Principal: principal, Collection: message.Topic})
	return err == nil && allowed
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
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Minute)
	defer cancel()
	r = r.WithContext(ctx)
	address, _ := clientIP(r, h.dependencies.trustedProxies())
	client := &realtimeClient{id: uuid.NewString(), address: address, principal: realtimeSubject(r), token: realtimeCredential(r), resume: cursor}
	lifecycle := &RealtimeLifecycleEvent{UserID: client.principal.ID, Principal: client.principal, ClientID: client.id, Cursor: cursor}
	finish, err := beginRealtime(h.dependencies.RealtimeLifecycle, ctx, "connect", lifecycle)
	if err != nil {
		finish(err)
		writeRealtimeError(w, r, err)
		return
	}
	var connectionErr error
	defer func() { finish(connectionErr) }()
	h.mu.Lock()
	userAtLimit := client.principal.Kind == identity.User && h.userClients[client.principal.ID] >= 4
	if len(h.clients) >= 128 || userAtLimit {
		h.mu.Unlock()
		writeAPIError(w, r, APIError{Status: 429, Code: "realtime_limit", Message: "Too many realtime connections"})
		return
	}
	h.clients[client.id] = client
	if client.principal.Kind == identity.User {
		h.userClients[client.principal.ID]++
		client.admittedUser = client.principal.ID
	}
	h.mu.Unlock()
	defer func() {
		h.mu.Lock()
		delete(h.clients, client.id)
		if client.admittedUser != uuid.Nil {
			h.userClients[client.admittedUser]--
			if h.userClients[client.admittedUser] == 0 {
				delete(h.userClients, client.admittedUser)
			}
		}
		h.mu.Unlock()
	}()
	var applicationMessages <-chan ApplicationRealtimeMessage
	if h.dependencies.ApplicationRealtimeBroker != nil {
		applicationMessages = h.dependencies.ApplicationRealtimeBroker.connect(client.id)
		defer h.dependencies.ApplicationRealtimeBroker.disconnect(client.id)
	}
	control := http.NewResponseController(w)
	if err := control.SetWriteDeadline(time.Now().Add(10 * time.Second)); err != nil {
		connectionErr = err
		writeRealtimeError(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Accel-Buffering", "no")
	// The client identity is not a replay cursor. Omitting id also preserves an
	// existing numeric Last-Event-ID until a durable checkpoint is delivered.
	if _, err := fmt.Fprintf(w, "event: PB_CONNECT\ndata: {\"clientId\":%q}\n\n", client.id); err != nil {
		connectionErr = err
		return
	}
	if err := control.Flush(); err != nil {
		connectionErr = err
		return
	}
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			connectionErr = ctx.Err()
			return
		case message := <-applicationMessages:
			client.deliveryMu.Lock()
			err = h.deliverApplicationMessage(w, r, control, client, message)
			client.deliveryMu.Unlock()
		case <-ticker.C:
			client.deliveryMu.Lock()
			err = h.deliverRecordMessages(w, r, control, client)
			client.deliveryMu.Unlock()
		}
		if err != nil {
			connectionErr = err
			return
		}
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
		if subscriptions[index].application {
			// Custom application topics are exact broker names.  They have no
			// durable cursor, collection lookup, or collection-policy fallback.
			continue
		}
		// Always validate against the current schema and policy before replacing
		// client state. Validate retained/reconnect cursors here too so expired
		// history returns 410 without installing a subscription that cannot read.
		candidate := resume
		if cursor, exists := previous[subscriptions[index].raw]; exists {
			candidate = cursor
		}
		readRequest := realtime.ReadRequest{Principal: principal, Collection: subscriptions[index].collection, PublicID: subscriptions[index].recordID, Filter: subscriptions[index].filter, Fields: subscriptions[index].fields, After: candidate, RuleContext: ruleRequestContext(r)}
		_, head, err := reader.ReadFor(r.Context(), readRequest)
		if errors.Is(err, realtime.ErrRelationFilterRequiresCurrentRecord) {
			readRequest.Filter, readRequest.Fields = "", ""
			_, head, err = reader.ReadFor(r.Context(), readRequest)
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
		if candidate != nil {
			subscriptions[index].cursor = cloneRealtimeCursor(candidate)
		} else {
			subscriptions[index].cursor = &head
		}
	}
	if principal.Kind == identity.User {
		h.mu.Lock()
		atLimit := client.admittedUser == uuid.Nil && h.userClients[principal.ID] >= 4
		if client.admittedUser == uuid.Nil && !atLimit {
			h.userClients[principal.ID]++
			client.admittedUser = principal.ID
		}
		h.mu.Unlock()
		if atLimit {
			writeAPIError(w, r, APIError{Status: 429, Code: "realtime_limit", Message: "Too many realtime connections"})
			return
		}
	}
	client.mu.Lock()
	client.principal = principal
	client.token = realtimeCredential(r)
	client.subscriptions = subscriptions
	client.resume = nil
	client.mu.Unlock()
	if h.dependencies.ApplicationRealtimeBroker != nil {
		h.dependencies.ApplicationRealtimeBroker.subscribe(client.id, form.Subscriptions)
	}
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
	if validApplicationRealtimeTopic(raw) {
		return realtimeSubscription{raw: raw, application: true}, nil
	}
	parts := strings.SplitN(raw, "?", 2)
	topic := strings.TrimSuffix(parts[0], "/*")
	segments := strings.Split(topic, "/")
	if len(segments) > 2 || segments[0] == "" {
		return realtimeSubscription{}, errors.New("invalid topic")
	}
	sub := realtimeSubscription{raw: raw, collection: segments[0]}
	if len(segments) == 2 {
		if segments[1] == "" || len(segments[1]) > 1024 {
			return realtimeSubscription{}, errors.New("invalid record id")
		}
		sub.recordID = segments[1]
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
