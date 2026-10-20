package transport

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

const stdioListenAckTimeout = 5 * time.Second

type listenState int

const (
	listenAwaitingAck listenState = iota
	listenActive
	listenEnded
)

type stdioSubscription struct {
	requestID      int64
	response       chan *Response
	acknowledged   chan struct{}
	ended          chan struct{}
	subscriptionID json.RawMessage
	state          listenState // guarded by listenMu
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
	c.awaitSubscription(ctx, subscription)
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

func (c *StdioConnection) awaitSubscription(ctx context.Context, subscription *stdioSubscription) {
	timer := c.clock.NewTimer(stdioListenAckTimeout)
	defer timer.Stop()
	acknowledged, ackTimeout := subscription.acknowledged, timer.Chan()
	for {
		select {
		case <-acknowledged:
			acknowledged, ackTimeout = nil, nil
		case response := <-subscription.response:
			c.finishSubscription(subscription, response)
			return
		case <-subscription.ended:
			c.clearSubscription(subscription)
			return
		case <-ackTimeout:
			c.withdrawUnacknowledged(ctx, subscription)
			return
		case <-ctx.Done():
			c.retireSubscription(subscription)
			return
		case <-c.done:
			c.retireSubscription(subscription)
			return
		}
	}
}

func (c *StdioConnection) withdrawUnacknowledged(ctx context.Context, subscription *stdioSubscription) {
	c.retireSubscription(subscription)
	c.logger.Warn("upstream did not acknowledge tool-change subscription")
	c.cancelUpstreamRequest(ctx, subscription.requestID)
}

func (c *StdioConnection) finishSubscription(subscription *stdioSubscription, response *Response) {
	c.clearSubscription(subscription)
	if response.Error != nil {
		c.logger.Warn("upstream rejected tool-change subscription", "err", response.Error)
	}
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
	if subscription == nil || subscription.state != listenAwaitingAck ||
		!sameJSONID(ack.Meta.SubscriptionID, subscription.requestID) {
		c.listenMu.Unlock()
		return true
	}
	subscription.subscriptionID = append(json.RawMessage(nil), ack.Meta.SubscriptionID...)
	subscription.state = listenActive
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
	accepted := subscription != nil && subscription.state == listenActive &&
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
	if subscription == nil || subscription.state == listenEnded ||
		!sameJSONID(params.RequestID, subscription.requestID) {
		c.listenMu.Unlock()
		return true
	}
	wasActive := subscription.end()
	close(subscription.ended)
	c.listenMu.Unlock()
	c.pending.remove(subscription.requestID)
	if wasActive {
		c.refreshAfterServerEndedSubscription()
	}
	return true
}

func (c *StdioConnection) noteSubscriptionResponse(response *Response) {
	c.listenMu.Lock()
	subscription := c.listen
	ends := subscription != nil && subscription.state != listenEnded &&
		normalizeID(response.ID) == subscription.requestID
	wasActive := ends && subscription.end()
	c.listenMu.Unlock()
	if wasActive && response.Error == nil {
		c.refreshAfterServerEndedSubscription()
	}
}

// end must be called with listenMu held.
func (s *stdioSubscription) end() (wasActive bool) {
	wasActive = s.state == listenActive
	s.state = listenEnded
	return wasActive
}

// Servers end subscriptions when shutting down, so this one isn't renewed; one refresh covers any
// change the ended subscription would have reported.
func (c *StdioConnection) refreshAfterServerEndedSubscription() {
	c.logger.Info("upstream ended its tool-change subscription")
	c.toolsChanged.NotifyToolsChanged()
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
