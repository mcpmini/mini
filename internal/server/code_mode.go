package server

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/forge"
	"github.com/mcpmini/mini/internal/registry"
)

type toolBridge struct {
	s       *Server
	session *Session
}

func (b *toolBridge) ListTools(_ context.Context) (any, error) {
	return b.s.reg.AllDetailed(), nil
}

func (b *toolBridge) CallTool(ctx context.Context, server, tool string, params map[string]any) (json.RawMessage, error) {
	p := executeParams{Server: server, Tool: tool, Params: params}
	if err := validateExecuteParams(p); err != nil {
		return nil, err
	}
	entry, err := b.s.reg.Lookup(toolFullName(server, tool))
	if err != nil {
		return nil, err
	}
	if entry.Permission == config.PermProtected {
		return nil, fmt.Errorf("tool %q is protected — call it with perm_call outside execute_code", entry.FullName)
	}
	// Action default args are merged after this point and may satisfy required
	// params, so virtual tools skip the check.
	if entry.TargetTool == "" {
		if err := checkParamsAgainstSchema(entry, params); err != nil {
			return nil, err
		}
	}
	p.Tool = entry.ToolName.UpstreamName
	return b.callRaw(ctx, p, entry)
}

func (b *toolBridge) callRaw(ctx context.Context, p executeParams, entry *registry.ToolEntry) (json.RawMessage, error) {
	server, tool, params := resolveTarget(p, entry)
	upstream, err := b.s.getUpstream(server)
	if err != nil {
		return nil, err
	}
	raw, latencyMs, toolErr := b.s.dispatchRaw(ctx, dispatchParams{Upstream: upstream, Tool: tool, Params: params, Session: b.session})
	upstream.totalLatencyMs.Add(latencyMs)
	if toolErr != nil {
		b.session.recordCall(latencyMs, 0, true)
		b.s.logToolError(server, tool, latencyMs, toolErr)
		return nil, toolErr
	}
	upstream.recordSaved(b.session, latencyMs, 0)
	return raw, nil
}

type executeCodeRequest struct {
	Code     string          `json:"code"`
	Input    json.RawMessage `json:"input"`
	Packages []string        `json:"packages"`
}

func (s *Server) handleExecuteCode(ctx context.Context, raw json.RawMessage, session *Session) (any, error) {
	if !s.cfg.CodeMode.Enabled {
		return nil, fmt.Errorf("%w: unknown tool: execute_code", errInvalidParams)
	}
	req, err := parseExecuteCodeRequest(raw)
	if err != nil {
		return nil, err
	}
	return forge.Execute(ctx, forge.Params{
		Code:                 req.Code,
		Input:                req.Input,
		Packages:             req.Packages,
		Net:                  s.cfg.CodeMode.URLAllowList,
		Env:                  s.cfg.CodeMode.EnvVarAllowList,
		DangerousAllowAllNet: s.cfg.CodeMode.DangerousAllowAnyURL,
		ReadPaths:            s.cfg.CodeMode.FileReadAllowList,
		WritePaths:           s.cfg.CodeMode.FileWriteAllowList,
		Tools:                &toolBridge{s: s, session: session},
	})
}

func parseExecuteCodeRequest(raw json.RawMessage) (executeCodeRequest, error) {
	var req executeCodeRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return executeCodeRequest{}, fmt.Errorf("%w: execute_code: %w", errInvalidParams, err)
	}
	if req.Code == "" {
		return executeCodeRequest{}, fmt.Errorf("%w: execute_code requires code", errInvalidParams)
	}
	return req, nil
}
