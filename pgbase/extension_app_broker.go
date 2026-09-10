package pgbase

import (
	"encoding/json"
	"strings"
)

const maximumBrokerPayload = 64 << 10

func (control *extensionAppControl) callBroker(method string, raw json.RawMessage) (json.RawMessage, error) {
	if control.app.realtimeBroker == nil {
		return nil, extensionError("broker_unavailable", nil)
	}
	if method == "app-control.brokerClients" {
		return json.Marshal(control.app.realtimeBroker.Clients())
	}
	var request struct {
		Topic string          `json:"topic"`
		Data  json.RawMessage `json:"data"`
	}
	if err := decodeExtensionInput(raw, &request); err != nil || strings.TrimSpace(request.Topic) == "" || len(request.Topic) > 256 || len(request.Data) == 0 || len(request.Data) > maximumBrokerPayload || !json.Valid(request.Data) {
		return nil, extensionError("invalid_broker_message", err)
	}
	return json.Marshal(map[string]int{"delivered": control.app.realtimeBroker.Publish(request.Topic, request.Data)})
}
