package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"

	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/projection"
	"github.com/mcpmini/mini/internal/response"
)

type unitKind int

const (
	unitStructured unitKind = iota
	unitJSONText
	unitPlainText
	unitResourceText
)

type projectUnit struct {
	kind    unitKind
	block   int
	prefix  string
	value   any
	result  projection.Result
	changed bool
}

func (u *projectUnit) project(cfg *config.ProjectionConfig, defaults *projection.Defaults) {
	u.result = projection.Apply(u.value, cfg, defaults)
	u.changed = !reflect.DeepEqual(u.result.Summary, u.value)
}

func (u *projectUnit) carry(notice *response.ProjectionMeta) {
	switch v := u.result.Summary.(type) {
	case map[string]any:
		v["__mini"] = notice
	case []any: // Clients that show only structuredContent would otherwise never see the list was cut.
		v = append(v, map[string]any{"__mini": notice})
		u.result.Summary = v
	default:
		return
	}
	u.changed = true
}

func (u *projectUnit) text() (string, error) {
	if u.kind == unitJSONText {
		b, err := marshalCompact(u.result.Summary)
		return string(b), err
	}
	text, _ := u.result.Summary.(string)
	return text, nil
}

type resultDoc struct {
	result        toolResult
	structured    any
	hasStructured bool
	mirrors       []int
}

func parseResultDoc(raw json.RawMessage) (*resultDoc, bool) {
	d := &resultDoc{}
	if json.Unmarshal(raw, &d.result) != nil {
		return nil, false
	}
	if len(d.result.StructuredContent) > 0 {
		d.structured, d.hasStructured = decodeJSONValue(d.result.StructuredContent)
	}
	return d, true
}

// Before 2026-07-28 structuredContent must be an object, and clients that validate results reject the
// whole call when a newer upstream returns a list there:
// https://github.com/modelcontextprotocol/modelcontextprotocol/blob/c4c367f9f58296a7053f5c78a52fd02bfbb56a49/schema/2025-11-25/schema.ts#L1110-L1113
func legacyStructuredContent(raw json.RawMessage) json.RawMessage {
	d, ok := parseResultDoc(raw)
	if !ok || !nonObjectStructured(d.result.StructuredContent) {
		return raw
	}
	d.collectUnits() // Fills d.mirrors with the text blocks that already carry structuredContent.
	if len(d.mirrors) == 0 {
		d.result.Content = append(d.result.Content, newTextBlock(string(d.result.StructuredContent)))
	}
	d.result.StructuredContent = nil
	encoded, err := d.encode()
	if err != nil {
		return raw // Re-encoding a result that was just decoded doesn't fail.
	}
	return encoded
}

func nonObjectStructured(raw json.RawMessage) bool {
	trimmed := bytes.TrimSpace(raw)
	return len(trimmed) > 0 && trimmed[0] != '{' && !bytes.Equal(trimmed, []byte("null"))
}

func (d *resultDoc) collectUnits() []projectUnit {
	var units []projectUnit
	if d.hasStructured {
		units = append(
			units,
			projectUnit{kind: unitStructured, block: -1, prefix: ".structuredContent", value: d.structured},
		)
	}
	for i := range d.result.Content {
		units = d.appendBlockUnit(units, i, d.result.Content[i])
	}
	return units
}

func (d *resultDoc) appendBlockUnit(units []projectUnit, i int, b contentBlock) []projectUnit {
	switch {
	case b.Type == "text" && b.Text != nil:
		return d.appendTextUnit(units, i, *b.Text)
	case b.Type == "resource" && b.Resource != nil && b.Resource.Text != nil:
		prefix := fmt.Sprintf(".content[%d].resource.text", i)
		return append(units, projectUnit{kind: unitResourceText, block: i, prefix: prefix, value: *b.Resource.Text})
	}
	return units
}

func (d *resultDoc) appendTextUnit(units []projectUnit, i int, text string) []projectUnit {
	prefix := fmt.Sprintf(".content[%d].text", i)
	value, isJSON := trimmedJSONValue(text)
	if !isJSON {
		return append(units, projectUnit{kind: unitPlainText, block: i, prefix: prefix, value: text})
	}
	if d.hasStructured && reflect.DeepEqual(value, d.structured) {
		d.mirrors = append(d.mirrors, i)
		return units
	}
	return append(units, projectUnit{kind: unitJSONText, block: i, prefix: prefix + " | fromjson", value: value})
}

func pickCarrier(units []projectUnit, hasStructured bool) *projectUnit {
	if hasStructured {
		if _, isArray := units[0].value.([]any); isArray || isMiniFreeObject(units[0].value) {
			return &units[0]
		}
		return nil
	}
	var found *projectUnit
	for i := range units {
		if units[i].kind != unitJSONText || !isMiniFreeObject(units[i].value) {
			continue
		}
		if found != nil {
			return nil
		}
		found = &units[i]
	}
	return found
}

func (d *resultDoc) applyUnit(u *projectUnit) error {
	if !u.changed {
		return nil
	}
	if u.kind == unitStructured {
		return d.applyStructured(u)
	}
	text, err := u.text()
	if err != nil {
		return err
	}
	block := &d.result.Content[u.block]
	if u.kind == unitResourceText {
		block.setResourceText(text)
	} else {
		block.setText(text)
	}
	return nil
}

func (d *resultDoc) applyStructured(u *projectUnit) error {
	encoded, err := marshalCompact(u.result.Summary)
	if err != nil {
		return err
	}
	d.result.StructuredContent = encoded
	for _, i := range d.mirrors {
		d.result.Content[i].setText(string(encoded))
	}
	return nil
}
