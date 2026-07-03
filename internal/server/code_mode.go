package server

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/mcpmini/mini/internal/forge"
)

type executeCodeRequest struct {
	Code     string          `json:"code"`
	Input    json.RawMessage `json:"input"`
	Packages []string        `json:"packages"`
}

func (s *Server) handleExecuteCode(ctx context.Context, raw json.RawMessage) (any, error) {
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
