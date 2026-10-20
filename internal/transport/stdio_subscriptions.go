package transport

import (
	"context"
	"encoding/json"
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
	requestID    int64
	response     chan *Response
	acknowledged chan struct{}
	ended        chan struct{}
	state        listenState // guarded by listenMu
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
	params, err := modernListenParams(c.negotiatedModernVersion())
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
	if json.Unmarshal(raw, &ack) != nil || !ack.Notifications.ToolsListChanged {
		return true
	}
	c.listenMu.Lock()
	subscription := c.listen
	if subscription == nil || subscription.state != listenAwaitingAck ||
		!subscription.namedBy(ack.Meta.SubscriptionID) {
		c.listenMu.Unlock()
		return true
	}
	subscription.state = listenActive
	close(subscription.acknowledged)
	c.listenMu.Unlock()
	c.logger.Debug("upstream acknowledged tool-change subscription")
	c.toolsChanged.NotifyToolsChanged()
	return true
}

func (c *StdioConnection) forwardSubscriptionChange(raw json.RawMessage) bool {
	var changed SubscriptionNotificationParams
	if json.Unmarshal(raw, &changed) != nil {
		return true
	}
	c.listenMu.Lock()
	subscription := c.listen
	accepted := subscription != nil && subscription.state == listenActive &&
		subscription.namedBy(changed.Meta.SubscriptionID)
	c.listenMu.Unlock()
	if accepted {
		c.toolsChanged.NotifyToolsChanged()
	}
	return true
}

func (c *StdioConnection) cancelSubscription(raw json.RawMessage) bool {
	var params CancelledParams
	if json.Unmarshal(raw, &params) != nil {
		return true
	}
	c.listenMu.Lock()
	subscription := c.listen
	if subscription == nil || subscription.state == listenEnded || !subscription.namedBy(params.RequestID) {
		c.listenMu.Unlock()
		return true
	}
	wasActive := subscription.endLocked()
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
	wasActive := ends && subscription.endLocked()
	c.listenMu.Unlock()
	if wasActive && response.Error == nil {
		c.refreshAfterServerEndedSubscription()
	}
}

func (s *stdioSubscription) namedBy(id json.RawMessage) bool {
	return sameJSONID(id, s.requestID)
}

func (s *stdioSubscription) endLocked() (wasActive bool) {
	wasActive = s.state == listenActive
	s.state = listenEnded
	return wasActive
}

// A server ends a subscription when it shuts down, so this one isn't renewed.
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
