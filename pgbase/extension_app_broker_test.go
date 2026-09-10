package pgbase

import (
	"encoding/json"
	"sync"
	"testing"
)

func TestExtensionBrokerPublishesOnlyToSubscribedClientsAcrossCalls(t *testing.T) {
	control := &extensionAppControl{broker: map[string]*extensionBrokerClient{}}
	for _, raw := range []string{
		`{"clientId":"alpha","topics":["orders"]}`,
		`{"clientId":"beta","topics":["users"]}`,
	} {
		if _, err := control.callBroker("app-control.brokerRegister", json.RawMessage(raw)); err != nil {
			t.Fatal(err)
		}
	}
	if output, err := control.callBroker("app-control.brokerPublish", json.RawMessage(`{"topic":"orders","data":{"id":7}}`)); err != nil || string(output) != `{"delivered":1}` {
		t.Fatalf("publish output=%s err=%v", output, err)
	}
	output, err := control.callBroker("app-control.brokerDrain", json.RawMessage(`{"clientId":"alpha"}`))
	if err != nil || string(output) != `[{"topic":"orders","data":{"id":7}}]` {
		t.Fatalf("alpha drain=%s err=%v", output, err)
	}
	output, err = control.callBroker("app-control.brokerDrain", json.RawMessage(`{"clientId":"beta"}`))
	if err != nil || string(output) != `[]` {
		t.Fatalf("beta drain=%s err=%v", output, err)
	}
}

func TestExtensionBrokerConcurrentPublishIsBounded(t *testing.T) {
	control := &extensionAppControl{broker: map[string]*extensionBrokerClient{}}
	if _, err := control.callBroker("app-control.brokerRegister", json.RawMessage(`{"clientId":"client","topics":["topic"]}`)); err != nil {
		t.Fatal(err)
	}
	var group sync.WaitGroup
	for range maximumBrokerQueue + 40 {
		group.Go(func() {
			if _, err := control.callBroker("app-control.brokerPublish", json.RawMessage(`{"topic":"topic","data":1}`)); err != nil {
				t.Error(err)
			}
		})
	}
	group.Wait()
	output, err := control.callBroker("app-control.brokerDrain", json.RawMessage(`{"clientId":"client"}`))
	if err != nil {
		t.Fatal(err)
	}
	var messages []extensionBrokerMessage
	if err := json.Unmarshal(output, &messages); err != nil || len(messages) != maximumBrokerQueue {
		t.Fatalf("queue length=%d err=%v", len(messages), err)
	}
}
