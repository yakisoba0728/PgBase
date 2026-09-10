package pgbase

import (
	"encoding/json"
	"errors"
	"sort"
	"strings"
)

const (
	maximumBrokerClients = 256
	maximumBrokerTopics  = 64
	maximumBrokerQueue   = 256
	maximumBrokerPayload = 64 << 10
)

type extensionBrokerClient struct {
	Topics map[string]struct{}
	Queue  []extensionBrokerMessage
}

type extensionBrokerMessage struct {
	Topic string          `json:"topic"`
	Data  json.RawMessage `json:"data"`
}

func (control *extensionAppControl) callBroker(method string, raw json.RawMessage) (json.RawMessage, error) {
	var request struct {
		ClientID string          `json:"clientId"`
		Topics   []string        `json:"topics"`
		Topic    string          `json:"topic"`
		Data     json.RawMessage `json:"data"`
	}
	if err := decodeExtensionInput(raw, &request); err != nil {
		return nil, extensionError("invalid_broker_request", err)
	}
	switch method {
	case "app-control.brokerRegister":
		if request.ClientID == "" || len(request.ClientID) > 256 || len(request.Topics) > maximumBrokerTopics {
			return nil, extensionError("invalid_broker_client", nil)
		}
		topics := make(map[string]struct{}, len(request.Topics))
		for _, topic := range request.Topics {
			if strings.TrimSpace(topic) == "" || len(topic) > 256 {
				return nil, extensionError("invalid_broker_topic", nil)
			}
			topics[topic] = struct{}{}
		}
		control.mu.Lock()
		if _, exists := control.broker[request.ClientID]; !exists && len(control.broker) >= maximumBrokerClients {
			control.mu.Unlock()
			return nil, extensionError("broker_capacity_exceeded", nil)
		}
		client := control.broker[request.ClientID]
		if client == nil {
			client = &extensionBrokerClient{}
			control.broker[request.ClientID] = client
		}
		client.Topics = topics
		control.mu.Unlock()
		return json.RawMessage(`{"registered":true}`), nil
	case "app-control.brokerUnregister":
		control.mu.Lock()
		_, existed := control.broker[request.ClientID]
		delete(control.broker, request.ClientID)
		control.mu.Unlock()
		return json.Marshal(existed)
	case "app-control.brokerClients":
		control.mu.RLock()
		ids := make([]string, 0, len(control.broker))
		for id := range control.broker {
			ids = append(ids, id)
		}
		control.mu.RUnlock()
		sort.Strings(ids)
		return json.Marshal(ids)
	case "app-control.brokerPublish":
		if strings.TrimSpace(request.Topic) == "" || len(request.Topic) > 256 || len(request.Data) == 0 || len(request.Data) > maximumBrokerPayload || !json.Valid(request.Data) {
			return nil, extensionError("invalid_broker_message", nil)
		}
		delivered := 0
		control.mu.Lock()
		for _, client := range control.broker {
			if _, subscribed := client.Topics[request.Topic]; !subscribed {
				continue
			}
			if len(client.Queue) >= maximumBrokerQueue {
				client.Queue = client.Queue[1:]
			}
			client.Queue = append(client.Queue, extensionBrokerMessage{Topic: request.Topic, Data: append(json.RawMessage(nil), request.Data...)})
			delivered++
		}
		control.mu.Unlock()
		return json.Marshal(map[string]int{"delivered": delivered})
	case "app-control.brokerDrain":
		control.mu.Lock()
		client := control.broker[request.ClientID]
		if client == nil {
			control.mu.Unlock()
			return nil, extensionError("broker_client_not_found", errors.New("broker client is not registered"))
		}
		messages := append([]extensionBrokerMessage{}, client.Queue...)
		client.Queue = nil
		control.mu.Unlock()
		return json.Marshal(messages)
	default:
		return nil, extensionError("unsupported_app_control", nil)
	}
}
