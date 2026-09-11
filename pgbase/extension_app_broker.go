package pgbase

import (
	"context"
	"encoding/json"
	"strings"
)

const maximumBrokerPayload = 64 << 10

func (control *extensionAppControl) callBroker(ctx context.Context, method string, raw json.RawMessage) (json.RawMessage, error) {
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
	if err := decodeExtensionInput(raw, &request); err != nil || len(request.Data) == 0 || len(request.Data) > maximumBrokerPayload || !json.Valid(request.Data) {
		return nil, extensionError("invalid_broker_message", err)
	}
	if method == "app-control.brokerPublishApplication" {
		delivered, publishErr := control.app.realtimeBroker.PublishApplication(ctx, request.Topic, request.Data)
		if publishErr != nil {
			return nil, extensionError("invalid_broker_message", publishErr)
		}
		return json.Marshal(map[string]int{"delivered": delivered})
	}
	if strings.TrimSpace(request.Topic) == "" || len(request.Topic) > 256 {
		return nil, extensionError("invalid_broker_message", nil)
	}
	return json.Marshal(map[string]int{"delivered": control.app.realtimeBroker.Publish(ctx, request.Topic, request.Data)})
}
