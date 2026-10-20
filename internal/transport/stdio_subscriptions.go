package transport

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

const stdioListenAckTimeout = 5 * time.Second

type stdioSubscription struct {
	requestID      int64
	response       chan *Response
	acknowledged   chan struct{}
	ended          chan struct{}
	subscriptionID json.RawMessage
	acked          bool
	completed      bool
}

func (c *StdioConnection) startSubscriptionListener(parent context.Context) {
	ctx, cancel := context.WithCancel(parent)
	c.listenCancel = cancel
	c.listenDone = make(chan struct{})
	go func() {
		defer close(c.listenDone)
		c.listenForToolChanges(ctx)
	}()
}

func (c *StdioConnection) listenForToolChanges(ctx context.Context) {
	subscription, err := c.beginSubscription(ctx)
	if err != nil {
		if ctx.Err() == nil && !c.isClosed() {
			c.logger.Warn("upstream tool-change subscription stopped", "err", err)
		}
		return
	}
	if c.awaitSubscription(ctx, subscription) {
		// Servers end subscriptions when shutting down, so this one isn't renewed; one refresh
		// covers any change the ended subscription would have reported.
		c.toolsChanged.NotifyToolsChanged()
		c.logger.Info("upstream ended its tool-change subscription")
	}
}

func (c *StdioConnection) beginSubscription(ctx context.Context) (*stdioSubscription, error) {
	params, err := c.listenParams()
	if err != nil {
		return nil, err
	}
	id := c.nextID.Add(1)
	subscription := &stdioSubscription{
		requestID:    id,
		response:     c.pending.register(id),
		acknowledged: make(chan struct{}),
		ended:        make(chan struct{}),
	}
	c.listenMu.Lock()
	c.listen = subscription
	c.listenMu.Unlock()
	request := Request{JsonRPC: JsonRPCVersion, ID: id, Method: MethodSubscriptionsListen, Params: params}
	if err := c.writeJSONContext(ctx, request); err != nil {
		c.pending.remove(id)
		c.clearSubscription(subscription)
		c.abandonAfterFailedWrite(ctx, err)
		return nil, err
	}
	return subscription, nil
}

func (c *StdioConnection) listenParams() (json.RawMessage, error) {
	params, err := json.Marshal(ListenParams{Notifications: ListenNotifications{ToolsListChanged: true}})
	if err != nil {
		return nil, fmt.Errorf("encode subscriptions/listen params: %w", err)
	}
	return prepareModernParams(params, c.negotiatedModernVersion())
}

func (c *StdioConnection) awaitSubscription(ctx context.Context, subscription *stdioSubscription) bool {
	timer := c.clock.NewTimer(stdioListenAckTimeout)
	defer timer.Stop()
	acknowledged, ackTimeout := subscription.acknowledged, timer.Chan()
	for {
		select {
		case <-acknowledged:
			acknowledged, ackTimeout = nil, nil
		case response := <-subscription.response:
			return c.finishSubscription(subscription, response)
		case <-subscription.ended:
			return c.finishSubscription(subscription, nil)
		case <-ackTimeout:
			c.withdrawUnacknowledged(ctx, subscription)
			return false
		case <-ctx.Done():
			c.retireSubscription(subscription)
			return false
		case <-c.done:
			c.retireSubscription(subscription)
			return false
		}
	}
}

func (c *StdioConnection) withdrawUnacknowledged(ctx context.Context, subscription *stdioSubscription) {
	c.retireSubscription(subscription)
	c.logger.Warn("upstream did not acknowledge tool-change subscription")
	c.cancelUpstreamRequest(ctx, subscription.requestID)
}

func (c *StdioConnection) finishSubscription(subscription *stdioSubscription, response *Response) bool {
	acked := c.subscriptionAcked(subscription)
	c.clearSubscription(subscription)
	if response != nil && response.Error != nil {
		c.logger.Warn("upstream rejected tool-change subscription", "err", response.Error)
		return false
	}
	return acked && !c.isClosed()
}

func (c *StdioConnection) dispatchModernSubscriptionNotification(notification Notification) bool {
	switch notification.Method {
	case NotificationSubscriptionsAcknowledged:
		return c.acknowledgeSubscription(notification.Params)
	case NotificationToolsChanged:
		return c.forwardSubscriptionChange(notification.Params)
	case NotificationCancelled:
		return c.cancelSubscription(notification.Params)
	default:
		return false
	}
}

func (c *StdioConnection) acknowledgeSubscription(raw json.RawMessage) bool {
	var ack SubscriptionAckParams
	if json.Unmarshal(raw, &ack) != nil || !ack.Notifications.ToolsListChanged ||
		!validJSONID(ack.Meta.SubscriptionID) {
		return true
	}
	c.listenMu.Lock()
	subscription := c.listen
	if subscription == nil || subscription.completed || subscription.acked ||
		!sameJSONID(ack.Meta.SubscriptionID, subscription.requestID) {
		c.listenMu.Unlock()
		return true
	}
	subscription.subscriptionID = append(json.RawMessage(nil), ack.Meta.SubscriptionID...)
	subscription.acked = true
	close(subscription.acknowledged)
	c.listenMu.Unlock()
	c.logger.Debug("upstream acknowledged tool-change subscription")
	c.toolsChanged.NotifyToolsChanged()
	return true
}

func (c *StdioConnection) forwardSubscriptionChange(raw json.RawMessage) bool {
	var changed SubscriptionNotificationParams
	if json.Unmarshal(raw, &changed) != nil || !validJSONID(changed.Meta.SubscriptionID) {
		return true
	}
	c.listenMu.Lock()
	subscription := c.listen
	accepted := subscription != nil && subscription.acked && !subscription.completed &&
		subscriptionIDsEqual(subscription.subscriptionID, changed.Meta.SubscriptionID)
	c.listenMu.Unlock()
	if accepted {
		c.toolsChanged.NotifyToolsChanged()
	}
	return true
}

func (c *StdioConnection) cancelSubscription(raw json.RawMessage) bool {
	var params CancelledParams
	if json.Unmarshal(raw, &params) != nil || !validJSONID(params.RequestID) {
		return true
	}
	c.listenMu.Lock()
	subscription := c.listen
	if subscription == nil || subscription.completed || !sameJSONID(params.RequestID, subscription.requestID) {
		c.listenMu.Unlock()
		return true
	}
	subscription.completed = true
	close(subscription.ended)
	c.listenMu.Unlock()
	c.pending.remove(subscription.requestID)
	return true
}

func (c *StdioConnection) noteSubscriptionResponse(response *Response) {
	c.listenMu.Lock()
	if subscription := c.listen; subscription != nil && !subscription.completed &&
		normalizeID(response.ID) == subscription.requestID {
		subscription.completed = true
	}
	c.listenMu.Unlock()
}

func (c *StdioConnection) retireSubscription(subscription *stdioSubscription) {
	c.pending.remove(subscription.requestID)
	c.clearSubscription(subscription)
}

func (c *StdioConnection) clearSubscription(subscription *stdioSubscription) {
	c.listenMu.Lock()
	if c.listen == subscription {
		c.listen = nil
	}
	c.listenMu.Unlock()
}

func (c *StdioConnection) subscriptionAcked(subscription *stdioSubscription) bool {
	c.listenMu.Lock()
	defer c.listenMu.Unlock()
	return subscription.acked
}

func subscriptionIDsEqual(a, b json.RawMessage) bool {
	var expected any
	if !validJSONID(a) || !validJSONID(b) || json.Unmarshal(b, &expected) != nil {
		return false
	}
	return sameJSONID(a, normalizeID(expected))
}

func validJSONID(raw json.RawMessage) bool {
	var value any
	if json.Unmarshal(raw, &value) != nil {
		return false
	}
	switch value.(type) {
	case string, float64:
		return true
	default:
		return false
	}
}
