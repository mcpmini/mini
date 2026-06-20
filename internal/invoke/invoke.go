package invoke

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/projection"
	"github.com/mcpmini/mini/internal/response"
	"github.com/mcpmini/mini/internal/transport"
)

type InvokeParams struct {
	Server   string
	Tool     string
	Params   map[string]any
	Conn     transport.Connection
	ProjCfg  *config.ProjectionConfig
	ProjDefs *projection.Defaults
	Builder  *response.Builder
}

type InvokeResult struct {
	Envelope  *response.Envelope
	LatencyMs int64 // internal only — not in envelope
}

func Invoke(ctx context.Context, p InvokeParams) (*InvokeResult, error) {
	raw, latencyMs, err := InvokeRaw(ctx, p.Conn, p.Tool, p.Params)
	if err != nil {
		return nil, err
	}
	env, err := buildEnvelopeFromParams(raw, p)
	if err != nil {
		return nil, err
	}
	return &InvokeResult{Envelope: env, LatencyMs: latencyMs}, nil
}

func buildEnvelopeFromParams(raw json.RawMessage, p InvokeParams) (*response.Envelope, error) {
	env, _, err := BuildEnvelope(BuildEnvelopeParams{
		Server:   p.Server,
		Tool:     p.Tool,
		Raw:      raw,
		ProjCfg:  p.ProjCfg,
		ProjDefs: p.ProjDefs,
		Builder:  p.Builder,
	})
	return env, err
}

func InvokeRaw(ctx context.Context, conn transport.Connection, tool string, params map[string]any) (json.RawMessage, int64, error) {
	args, err := json.Marshal(transport.ToolCallParams{Name: tool, Arguments: params})
	if err != nil {
		return nil, 0, fmt.Errorf("marshal params: %w", err)
	}
	start := time.Now()
	raw, err := conn.Call(ctx, "tools/call", args)
	latency := time.Since(start).Milliseconds()
	if err != nil {
		return nil, latency, err
	}
	extracted, err := ExtractContent(raw)
	return extracted, latency, err
}

type BuildEnvelopeParams struct {
	Server   string
	Tool     string
	Raw      json.RawMessage
	ProjCfg  *config.ProjectionConfig
	ProjDefs *projection.Defaults
	Builder  *response.Builder
}

func BuildEnvelope(p BuildEnvelopeParams) (*response.Envelope, response.CallStats, error) {
	var value any
	if err := json.Unmarshal(p.Raw, &value); err != nil {
		return nil, response.CallStats{}, fmt.Errorf("parse upstream response: %w", err)
	}
	result := projection.Apply(value, p.ProjCfg, p.ProjDefs)
	return p.Builder.Build(buildResponseParams(p, result))
}

func buildResponseParams(p BuildEnvelopeParams, result projection.Result) response.BuildParams {
	return response.BuildParams{
		Server:      p.Server,
		Tool:        p.Tool,
		Raw:         p.Raw,
		Summary:     result.Summary,
		Elided:      result.ElidedKeys,
		Truncated:     result.Truncated,
		Hint:        result.Hint,
		Passthrough: result.Passthrough,
	}
}

func ExtractContent(raw json.RawMessage) (json.RawMessage, error) {
	var result transport.ToolCallResult
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, fmt.Errorf("upstream returned non-standard response: %w", err)
	}
	if result.IsError {
		return nil, fmt.Errorf("tool returned error: %s", joinText(result.Content))
	}
	text := strings.TrimSpace(joinText(result.Content))
	if text == "" && len(result.StructuredContent) > 0 {
		// Spec 2025-06-18: servers SHOULD include text for backwards compat, but
		// if they don't, use structuredContent directly.
		return result.StructuredContent, nil
	}
	if json.Valid([]byte(text)) {
		return json.RawMessage(text), nil
	}
	b, err := json.Marshal(text)
	return b, err
}

func joinText(items []transport.ContentItem) string {
	var parts []string
	for _, item := range items {
		if item.Type == "text" && item.Text != "" {
			parts = append(parts, item.Text)
		}
	}
	return strings.Join(parts, "\n")
}
