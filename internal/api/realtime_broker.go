package api

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"sync"
	"unicode/utf8"
)

const maximumApplicationRealtimePayload = 64 << 10
const applicationRealtimeTopicPrefix = "@app/"
const maximumApplicationRealtimeTopicBytes = 188

type ApplicationRealtimeMessage struct {
	Topic string
	Data  json.RawMessage
}

type applicationRealtimeClient struct {
	topics map[string]struct{}
	queue  chan ApplicationRealtimeMessage
}

// ApplicationRealtimeBroker is shared by the HTTP realtime hub and the
// capability-gated JavaScript app facade. It exposes only connected client IDs
// and exact subscription topics; authentication remains owned by the hub.
type ApplicationRealtimeBroker struct {
	mu        sync.RWMutex
	clients   map[string]*applicationRealtimeClient
	authorize func(context.Context, string, ApplicationRealtimeMessage) bool
}

func (b *ApplicationRealtimeBroker) setAuthorizer(authorize func(context.Context, string, ApplicationRealtimeMessage) bool) {
	b.mu.Lock()
	b.authorize = authorize
	b.mu.Unlock()
}

func NewApplicationRealtimeBroker() *ApplicationRealtimeBroker {
	return &ApplicationRealtimeBroker{clients: map[string]*applicationRealtimeClient{}}
}

func (b *ApplicationRealtimeBroker) connect(id string) <-chan ApplicationRealtimeMessage {
	b.mu.Lock()
	defer b.mu.Unlock()
	client := &applicationRealtimeClient{topics: map[string]struct{}{}, queue: make(chan ApplicationRealtimeMessage, 256)}
	b.clients[id] = client
	return client.queue
}

func (b *ApplicationRealtimeBroker) disconnect(id string) {
	b.mu.Lock()
	delete(b.clients, id)
	b.mu.Unlock()
}

func (b *ApplicationRealtimeBroker) subscribe(id string, topics []string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	client := b.clients[id]
	if client == nil {
		return
	}
	client.topics = make(map[string]struct{}, len(topics))
	for _, topic := range topics {
		client.topics[topic] = struct{}{}
	}
}

func (b *ApplicationRealtimeBroker) Clients() []string {
	b.mu.RLock()
	ids := make([]string, 0, len(b.clients))
	for id := range b.clients {
		ids = append(ids, id)
	}
	b.mu.RUnlock()
	sort.Strings(ids)
	return ids
}

func (b *ApplicationRealtimeBroker) Publish(ctx context.Context, topic string, data json.RawMessage) int {
	// The broker is intentionally an internal/operator capability.  Its public
	// HTTP counterpart only calls it from an extension granted app-control.
	// Keep this lower-level boundary bounded too because Go extensions can call
	// it directly.
	if !validBrokerTopic(topic) || len(data) == 0 || len(data) > maximumApplicationRealtimePayload || !json.Valid(data) {
		return 0
	}
	b.mu.RLock()
	authorize := b.authorize
	targets := make(map[string]*applicationRealtimeClient, len(b.clients))
	for id, client := range b.clients {
		if _, ok := client.topics[topic]; ok {
			targets[id] = client
		}
	}
	b.mu.RUnlock()
	delivered := 0
	message := ApplicationRealtimeMessage{Topic: topic, Data: append(json.RawMessage(nil), data...)}
	for id, client := range targets {
		if authorize == nil || !authorize(ctx, id, message) {
			continue
		}
		select {
		case client.queue <- message:
			delivered++
		default:
		}
	}
	return delivered
}

// PublishApplication maps an arbitrary UTF-8 application topic into the
// reserved wire namespace before publishing.  The mapping is reversible for
// SDKs but is never inferred from a collection-looking topic.
func (b *ApplicationRealtimeBroker) PublishApplication(ctx context.Context, topic string, data json.RawMessage) (int, error) {
	encoded, err := ApplicationRealtimeTopic(topic)
	if err != nil {
		return 0, err
	}
	return b.Publish(ctx, encoded, data), nil
}

// ApplicationRealtimeTopic returns the one canonical wire topic for raw.
// RawURLEncoding prevents separators, whitespace, and a raw @app/ prefix from
// being interpreted as a second namespace layer.
func ApplicationRealtimeTopic(raw string) (string, error) {
	if raw == "" || len(raw) > maximumApplicationRealtimeTopicBytes || !utf8.ValidString(raw) {
		return "", errors.New("invalid application realtime topic")
	}
	return applicationRealtimeTopicPrefix + base64.RawURLEncoding.EncodeToString([]byte(raw)), nil
}

func validBrokerTopic(topic string) bool {
	if topic == "" || len(topic) > 256 {
		return false
	}
	for _, value := range topic {
		if value <= 0x20 || value == 0x7f {
			return false
		}
	}
	return true
}

// @ is a reserved realtime application namespace.  Collection subscriptions
// never begin with it, so an application message cannot turn a removed,
// renamed, or access-denied collection name into an unguarded custom topic.
// The rest of the topic is exact: no wildcard or path normalization is used.
func validApplicationRealtimeTopic(topic string) bool {
	if len(topic) < 2 || len(topic) > 256 || topic[0] != '@' {
		return false
	}
	for _, value := range topic {
		if value <= 0x20 || value == 0x7f || strings.ContainsRune("?#*", value) {
			return false
		}
	}
	return true
}
