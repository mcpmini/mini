package transport

import "encoding/json"

const (
	MethodSubscriptionsListen             = "subscriptions/listen"
	NotificationSubscriptionsAcknowledged = "notifications/subscriptions/acknowledged"
)

type ListenNotifications struct {
	ToolsListChanged bool `json:"toolsListChanged,omitempty"`
}

type ListenParams struct {
	Notifications ListenNotifications `json:"notifications"`
}

type ListenRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Params  ListenParams    `json:"params"`
}

type SubscriptionMeta struct {
	SubscriptionID json.RawMessage `json:"io.modelcontextprotocol/subscriptionId"`
}

type SubscriptionAck struct {
	JSONRPC string                `json:"jsonrpc"`
	Method  string                `json:"method"`
	Params  SubscriptionAckParams `json:"params"`
}

type SubscriptionAckParams struct {
	Meta          SubscriptionMeta    `json:"_meta"`
	Notifications ListenNotifications `json:"notifications"`
}

func NewSubscriptionAck(id json.RawMessage, notifications ListenNotifications) SubscriptionAck {
	return SubscriptionAck{
		JSONRPC: "2.0",
		Method:  NotificationSubscriptionsAcknowledged,
		Params:  SubscriptionAckParams{Meta: SubscriptionMeta{SubscriptionID: id}, Notifications: notifications},
	}
}

type ToolsListChangedNotification struct {
	JSONRPC string                         `json:"jsonrpc"`
	Method  string                         `json:"method"`
	Params  SubscriptionNotificationParams `json:"params"`
}

type SubscriptionNotificationParams struct {
	Meta SubscriptionMeta `json:"_meta"`
}

func NewToolsListChangedNotification(subscriptionID json.RawMessage) ToolsListChangedNotification {
	return ToolsListChangedNotification{
		JSONRPC: "2.0",
		Method:  NotificationToolsChanged,
		Params:  SubscriptionNotificationParams{Meta: SubscriptionMeta{SubscriptionID: subscriptionID}},
	}
}
