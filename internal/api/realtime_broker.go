package api

import (
	"context"
	"encoding/json"
	"sort"
	"sync"
)

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
