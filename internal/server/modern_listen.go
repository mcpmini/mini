package server

import (
	"context"
	"encoding/json"
	"slices"

	"github.com/mcpmini/mini/internal/transport"
)

type listenRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Params  struct {
		Notifications struct {
			ToolsListChanged bool `json:"toolsListChanged"`
		} `json:"notifications"`
	} `json:"params"`
}

type subscriptionAck struct {
	JSONRPC string                `json:"jsonrpc"`
	Method  string                `json:"method"`
	Params  subscriptionAckParams `json:"params"`
}

type subscriptionAckParams struct {
	Meta          subscriptionMeta    `json:"_meta"`
	Notifications listenNotifications `json:"notifications"`
}

type subscriptionMeta struct {
	SubscriptionID json.RawMessage `json:"io.modelcontextprotocol/subscriptionId"`
}

type listenNotifications struct {
	ToolsListChanged bool `json:"toolsListChanged,omitempty"`
}

func (s *Server) startListen(ctx context.Context, p handleScannedLineParams, line []byte) {
	var req listenRequest
	if err := json.Unmarshal(line, &req); err != nil || len(req.ID) == 0 {
		p.writeOut(
			errorResponse(peekRequestID(line), transport.CodeInvalidParams, "invalid subscriptions/listen params"),
		)
		return
	}
	if req.JSONRPC != "2.0" {
		p.writeOut(errorResponse(req.ID, transport.CodeInvalidRequest, "jsonrpc must be \"2.0\""))
		return
	}
	version, modern := modernRequestVersion(line)
	if !modern || !slices.Contains(transport.ModernVersions, version) {
		s.rejectListenVersion(p, req.ID, version)
		return
	}
	acknowledged := listenNotifications{ToolsListChanged: req.Params.Notifications.ToolsListChanged}
	p.writeOut(listenAcknowledgment(req.ID, acknowledged))
	s.waitOnListen(ctx, p, req.ID, acknowledged.ToolsListChanged)
}

func (s *Server) rejectListenVersion(p handleScannedLineParams, id json.RawMessage, version string) {
	response, err := unsupportedVersionResponse(id, version)
	if err != nil {
		p.writeOut(errorResponse(id, transport.CodeInternalError, "encode unsupported protocol version"))
		return
	}
	p.writeOut(response)
}

func listenAcknowledgment(id json.RawMessage, notifications listenNotifications) subscriptionAck {
	return subscriptionAck{
		JSONRPC: "2.0", Method: "notifications/subscriptions/acknowledged",
		Params: subscriptionAckParams{Meta: subscriptionMeta{SubscriptionID: id}, Notifications: notifications},
	}
}

func (s *Server) waitOnListen(ctx context.Context, p handleScannedLineParams, id json.RawMessage, tools bool) {
	ctx, cancel := context.WithCancel(ctx)
	p.session.registerInFlight(id, cancel)
	p.session.addListen(id, cancel, tools)
	if tools {
		p.session.markClientReady()
	}
	p.wg.Add(1)
	go func() {
		defer p.wg.Done()
		<-ctx.Done()
		p.session.removeListen(id)
		p.session.removeInFlight(id)
		cancel()
	}()
}
