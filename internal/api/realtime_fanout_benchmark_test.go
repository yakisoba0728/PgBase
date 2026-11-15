package api

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
)

// This isolates the in-process application broadcast queue. It does not model
// PostgreSQL record replay, authorization queries, HTTP sockets or TLS. One in
// eight clients stops consuming; its queue is already full before measurement.
func BenchmarkApplicationRealtimeFanout(b *testing.B) {
	for _, clients := range []int{1, 32, 256} {
		b.Run(fmt.Sprintf("clients=%d", clients), func(b *testing.B) {
			broker := NewApplicationRealtimeBroker()
			broker.setAuthorizer(func(context.Context, string, ApplicationRealtimeMessage) bool { return true })
			payload := json.RawMessage(`{"event":"qualification","sequence":1}`)
			readers := make([]<-chan ApplicationRealtimeMessage, 0, clients)
			slow := 0
			for index := range clients {
				id := fmt.Sprint(index)
				queue := broker.connect(id)
				broker.subscribe(id, []string{"@app/qualification"})
				if clients > 1 && index%8 == 0 {
					for range cap(queue) {
						broker.clients[id].queue <- ApplicationRealtimeMessage{Data: payload}
					}
					slow++
				} else {
					readers = append(readers, queue)
				}
			}
			b.ReportAllocs()
			b.SetBytes(int64(len(payload) * len(readers)))
			for b.Loop() {
				if got := broker.Publish(context.Background(), "@app/qualification", payload); got != len(readers) {
					b.Fatalf("delivered=%d want=%d", got, len(readers))
				}
				for _, queue := range readers {
					if message := <-queue; string(message.Data) != string(payload) {
						b.Fatal("broadcast payload changed")
					}
				}
			}
			b.ReportMetric(float64(len(readers)), "delivered/op")
			b.ReportMetric(float64(slow), "full-queues/op")
		})
	}
}
