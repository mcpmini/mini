package server

import (
	"bytes"
	"encoding/json"
)

// toolResult is an MCP CallToolResult that mini can edit in place. Members it doesn't model, such as
// _meta, isError or fields from newer spec versions, are kept and written back unchanged.
// https://github.com/modelcontextprotocol/modelcontextprotocol/blob/459f1355af9ab1eec00bfa8124d10d4f1d0ab09c/schema/2025-06-18/schema.ts#L822
type toolResult struct {
	Content           []contentBlock
	StructuredContent json.RawMessage
	hasContent        bool
	extra             extraFields
}

type toolResultFields struct {
	Content           *[]contentBlock `json:"content,omitempty"`
	StructuredContent json.RawMessage `json:"structuredContent,omitempty"`
}

func (r *toolResult) UnmarshalJSON(raw []byte) error {
	var known toolResultFields
	extra, err := splitObject(raw, &known, "content", "structuredContent")
	if err != nil {
		return err
	}
	if known.Content != nil {
		r.Content, r.hasContent = *known.Content, true
	}
	r.StructuredContent, r.extra = known.StructuredContent, extra
	return nil
}

func (r toolResult) MarshalJSON() ([]byte, error) {
	known := toolResultFields{StructuredContent: r.StructuredContent}
	if r.hasContent || len(r.Content) > 0 {
		known.Content = &r.Content
	}
	return joinObject(known, r.extra)
}

// contentBlock is one MCP content block. Until mini edits it, it marshals back to the upstream bytes.
type contentBlock struct {
	Type     string
	Text     *string
	Resource *embeddedResource
	upstream json.RawMessage
	edited   bool
	extra    extraFields
}

type contentBlockFields struct {
	Type     string            `json:"type"`
	Text     *string           `json:"text,omitempty"`
	Resource *embeddedResource `json:"resource,omitempty"`
}

func newTextBlock(text string) contentBlock {
	return contentBlock{Type: "text", Text: &text, edited: true}
}

func (b *contentBlock) setText(text string) {
	b.Text, b.edited = &text, true
}

func (b *contentBlock) setResourceText(text string) {
	b.Resource.Text, b.edited = &text, true
}

func (b *contentBlock) UnmarshalJSON(raw []byte) error {
	var known contentBlockFields
	extra, err := splitObject(raw, &known, "type", "text", "resource")
	if err != nil {
		return err
	}
	*b = contentBlock{
		Type: known.Type, Text: known.Text, Resource: known.Resource,
		upstream: bytes.Clone(raw), extra: extra,
	}
	return nil
}

func (b contentBlock) MarshalJSON() ([]byte, error) {
	if !b.edited {
		return b.upstream, nil
	}
	return joinObject(contentBlockFields{Type: b.Type, Text: b.Text, Resource: b.Resource}, b.extra)
}

// embeddedResource is the resource of an MCP EmbeddedResource block; mini only edits its text.
type embeddedResource struct {
	URI      string
	MimeType *string
	Text     *string
	extra    extraFields
}

type embeddedResourceFields struct {
	URI      string  `json:"uri"`
	MimeType *string `json:"mimeType,omitempty"`
	Text     *string `json:"text,omitempty"`
}

func (e *embeddedResource) UnmarshalJSON(raw []byte) error {
	var known embeddedResourceFields
	extra, err := splitObject(raw, &known, "uri", "mimeType", "text")
	if err != nil {
		return err
	}
	*e = embeddedResource{URI: known.URI, MimeType: known.MimeType, Text: known.Text, extra: extra}
	return nil
}

func (e embeddedResource) MarshalJSON() ([]byte, error) {
	return joinObject(embeddedResourceFields{URI: e.URI, MimeType: e.MimeType, Text: e.Text}, e.extra)
}

// extraFields holds the members of a spec object that its Go type doesn't model.
type extraFields map[string]json.RawMessage

// splitObject decodes raw into known and returns every other member. A modeled member whose value
// is null stays in the returned set, because known's omitempty fields would drop it on the way out.
func splitObject(raw []byte, known any, modeled ...string) (extraFields, error) {
	if err := json.Unmarshal(raw, known); err != nil {
		return nil, err
	}
	var all extraFields
	if err := json.Unmarshal(raw, &all); err != nil {
		return nil, err
	}
	for _, name := range modeled {
		if string(all[name]) != "null" {
			delete(all, name)
		}
	}
	return all, nil
}

func joinObject(known any, extra extraFields) ([]byte, error) {
	encoded, err := marshalCompact(known)
	if err != nil || len(extra) == 0 {
		return encoded, err
	}
	var all extraFields
	if err := json.Unmarshal(encoded, &all); err != nil {
		return nil, err
	}
	for name, value := range extra {
		all[name] = value
	}
	return marshalCompact(all)
}
