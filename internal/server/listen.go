package server

import (
	"context"
	"encoding/json"

	"github.com/mcpmini/mini/internal/transport"
)

func (s *Server) handleListenLine(p handleScannedLineParams, line []byte) bool {
	if len(peekRequestID(line)) == 0 {
		return false
	}
	if peekMethod(line) == transport.MethodSubscriptionsListen {
		var request struct {
			Params json.RawMessage `json:"params"`
		}
		if json.Unmarshal(line, &request) != nil || !transport.HasModernRequestIntent(request.Params) {
			return false
		}
		s.startListen(p.ctx, p, line)
		return true
	}
	return false
}

type listenSubscription struct {
	id            json.RawMessage
	notifications transport.ListenNotifications
}

func (s *Server) startListen(ctx context.Context, p handleScannedLineParams, line []byte) {
	var req transport.ListenRequest
	if err := json.Unmarshal(line, &req); err != nil || len(req.ID) == 0 {
		p.writeOut(
			errorResponse(peekRequestID(line), transport.CodeInvalidParams, transport.InvalidListenParamsMessage),
		)
		return
	}
	if req.JsonRPC != transport.JsonRPCVersion {
		p.writeOut(errorResponse(req.ID, transport.CodeInvalidRequest, "jsonrpc must be \"2.0\""))
		return
	}
	var rawRequest transport.Request
	if err := json.Unmarshal(line, &rawRequest); err != nil {
		p.writeOut(errorResponse(req.ID, transport.CodeInvalidParams, transport.InvalidListenParamsMessage))
		return
	}
	if err := transport.ValidateModernRequestMeta(rawRequest.Params); err != nil {
		p.writeOut(errorResponse(req.ID, transport.CodeInvalidParams, err.Error()))
		return
	}
	version := modernRequestVersion(line)
	if !transport.IsModernVersion(version) {
		s.rejectListenVersion(p, req.ID, version)
		return
	}
	s.activateModern(p.session)
	acknowledged := transport.ListenNotifications{ToolsListChanged: req.Params.Notifications.ToolsListChanged}
	s.waitOnListen(ctx, p, listenSubscription{id: req.ID, notifications: acknowledged})
}

func (s *Server) rejectListenVersion(p handleScannedLineParams, id json.RawMessage, version string) {
	response, err := unsupportedVersionResponse(id, version)
	if err != nil {
		p.writeOut(errorResponse(id, transport.CodeInternalError, "encode unsupported protocol version"))
		return
	}
	p.writeOut(response)
}

func (s *Server) waitOnListen(ctx context.Context, p handleScannedLineParams, listen listenSubscription) {
	id := listen.id
	ctx, cancel := context.WithCancel(ctx)
	p.session.registerInFlight(id, cancel)
	p.session.acknowledgeListen(listen, cancel, p.writeOut)
	if listen.notifications.ToolsListChanged {
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

func (s *Session) acknowledgeListen(listen listenSubscription, cancel context.CancelFunc, writeOut func(any)) {
	// Held across the write: a tool change written after the acknowledgement must find this listen.
	s.listensMu.Lock()
	defer s.listensMu.Unlock()
	writeOut(transport.NewSubscriptionAck(listen.id, listen.notifications))
	s.listens[string(listen.id)] = cancel
	s.listenTools[string(listen.id)] = listen.notifications.ToolsListChanged
}

func (s *Session) removeListen(id json.RawMessage) {
	s.listensMu.Lock()
	delete(s.listens, string(id))
	delete(s.listenTools, string(id))
	s.listensMu.Unlock()
}

func (s *Session) cancelListens() {
	s.listensMu.Lock()
	for _, cancel := range s.listens {
		cancel()
	}
	s.listens = make(map[string]context.CancelFunc)
	s.listenTools = make(map[string]bool)
	s.listensMu.Unlock()
}
