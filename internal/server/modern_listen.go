package server

import (
	"context"
	"encoding/json"
	"slices"

	"github.com/mcpmini/mini/internal/transport"
)

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
	version, modern := modernRequestVersion(line)
	if !modern || !slices.Contains(transport.ModernVersions, version) {
		s.rejectListenVersion(p, req.ID, version)
		return
	}
	acknowledged := transport.ListenNotifications{ToolsListChanged: req.Params.Notifications.ToolsListChanged}
	p.writeOut(transport.NewSubscriptionAck(req.ID, acknowledged))
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
	p.session.addListen(listen, cancel)
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
