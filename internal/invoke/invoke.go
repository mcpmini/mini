package invoke

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/mcpmini/mini/internal/clock"
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
	Clock    clock.Clock
}

type InvokeResult struct {
	Envelope  *response.Envelope
	LatencyMs int64 // internal only — not in envelope
}

func Invoke(ctx context.Context, p InvokeParams) (*InvokeResult, error) {
	raw, latencyMs, err := InvokeRaw(ctx, InvokeRawParams{Clock: p.Clock, Conn: p.Conn, Tool: p.Tool, Params: p.Params})
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

type InvokeRawParams struct {
	Clock  clock.Clock
	Conn   transport.Connection
	Tool   string
	Params map[string]any
}

func InvokeRaw(ctx context.Context, p InvokeRawParams) (json.RawMessage, int64, error) {
	args, err := json.Marshal(transport.ToolCallParams{Name: p.Tool, Arguments: p.Params})
	if err != nil {
		return nil, 0, fmt.Errorf("marshal params: %w", err)
	}
	start := p.Clock.Now()
	raw, err := p.Conn.Call(ctx, "tools/call", args)
	latency := p.Clock.Since(start).Milliseconds()
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
	BypassProjection bool
}

func BuildEnvelope(p BuildEnvelopeParams) (*response.Envelope, response.CallStats, error) {
	if p.BypassProjection {
		result := projection.Result{Summary: p.Raw}
		return p.Builder.Build(buildResponseParams(p, result))
	}
	var value any
	dec := json.NewDecoder(bytes.NewReader(p.Raw))
	dec.UseNumber()
	if err := dec.Decode(&value); err != nil {
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
		Excluded:    result.ExcludedKeys,
		Truncated:   result.Truncated,
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
	// structuredContent is the canonical form; text in content is a backward-compat duplicate.
	// https://github.com/modelcontextprotocol/modelcontextprotocol/blob/357adac4b75939ef8ea37d3c40681be0b1db7d26/schema/2025-11-25/schema.ts#L1115
	if len(result.StructuredContent) > 0 {
		return result.StructuredContent, nil
	}
	texts := textBlocks(result.Content)
	if len(result.Content) == 1 && len(texts) == 1 {
		return extractSingleText(texts[0])
	}
	if len(result.Content) == 0 {
		return json.RawMessage("[]"), nil
	}
	return rawContentArray(raw)
}

func extractSingleText(text string) (json.RawMessage, error) {
	text = strings.TrimSpace(text)
	if json.Valid([]byte(text)) {
		return json.RawMessage(text), nil
	}
	b, err := json.Marshal(text)
	return b, err
}

func rawContentArray(raw json.RawMessage) (json.RawMessage, error) {
	var envelope struct {
		Content json.RawMessage `json:"content"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return nil, err
	}
	return envelope.Content, nil
}

func textBlocks(items []transport.ContentItem) []string {
	var texts []string
	for _, item := range items {
		if item.Type == "text" && item.Text != "" {
			texts = append(texts, item.Text)
		}
	}
	return texts
}

func joinText(items []transport.ContentItem) string {
	return strings.Join(textBlocks(items), "\n")
}
