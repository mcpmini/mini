package server

import (
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"

	"github.com/mcpmini/mini/internal/response"
)

func projectionNote(env *response.Envelope) string {
	var parts []string
	if len(env.Excluded) > 0 {
		parts = append(parts, strings.Join(env.Excluded, ", ")+" excluded")
	}
	for _, o := range env.Truncated {
		if o.Items > 0 && o.JQPath != "" {
			parts = append(parts, fmt.Sprintf("%s truncated (%d items removed)", o.JQPath, o.Items))
		} else if o.Items > 0 {
			parts = append(parts, fmt.Sprintf("truncated (%d items removed)", o.Items))
		} else if o.JQPath != "" {
			parts = append(parts, fmt.Sprintf("%s truncated (%d chars)", o.JQPath, o.Chars))
		} else {
			parts = append(parts, fmt.Sprintf("truncated (%d chars)", o.Chars))
		}
	}
	return strings.Join(parts, "; ")
}

func RenderLines(server, tool string, e *response.Envelope) string {
	var b strings.Builder
	fmt.Fprintf(&b, "[%s.%s]", server, tool)
	if e.File != nil {
		fmt.Fprintf(&b, " file:%s", *e.File)
	}
	b.WriteByte('\n')
	if e.Error != "" {
		fmt.Fprintf(&b, "ERROR %s: %s\n", e.Error, e.Message)
		return b.String()
	}
	if note := projectionNote(e); note != "" {
		fmt.Fprintf(&b, "note: %s\n", note)
	}
	writeLineData(&b, e.Data)
	return b.String()
}

func writeLineData(b *strings.Builder, data any) {
	switch v := data.(type) {
	case []any:
		writeItems(b, v)
	case string:
		b.WriteString(v)
		b.WriteByte('\n')
	case map[string]any:
		writeMapLines(b, v)
	default:
		raw, _ := json.Marshal(data)
		b.Write(raw)
		b.WriteByte('\n')
	}
}

func writeMapLines(b *strings.Builder, m map[string]any) {
	mainKey, mainArr := findPrimaryArray(m)
	for k, v := range m {
		if k == mainKey {
			continue
		}
		if isScalarValue(v) {
			fmt.Fprintf(b, "%s:%s\n", k, formatScalar(v))
		}
	}
	writeItems(b, mainArr)
}

func findPrimaryArray(m map[string]any) (string, []any) {
	var key string
	var arr []any
	for k, v := range m {
		if a, ok := v.([]any); ok && len(a) > len(arr) {
			key, arr = k, a
		}
	}
	return key, arr
}

func isScalarValue(v any) bool {
	switch v.(type) {
	case map[string]any, []any:
		return false
	default:
		return true
	}
}

// Header-then-values layout saves tokens vs key:value per line by hoisting field names to a single shared header row.
func writeItems(b *strings.Builder, items []any) {
	if headers := uniformKeys(items); headers != nil {
		writeUniformItems(b, items, headers)
		return
	}
	for _, item := range items {
		switch v := item.(type) {
		case string:
			b.WriteString(v + "\n")
		case map[string]any:
			b.WriteString(renderItemLine(v) + "\n")
		}
	}
}

func writeUniformItems(b *strings.Builder, items []any, headers []string) {
	b.WriteString(strings.Join(headers, " ") + "\n")
	for _, item := range items {
		if m, ok := item.(map[string]any); ok {
			b.WriteString(renderValueRow(m, headers) + "\n")
		}
	}
}

// Requires at least 2 items; single-item lists aren't worth the header overhead.
func uniformKeys(items []any) []string {
	if len(items) < 2 {
		return nil
	}
	first, ok := items[0].(map[string]any)
	if !ok {
		return nil
	}
	keys := sortedKeys(first)
	for _, item := range items[1:] {
		if !matchesUniformKeys(item, keys) {
			return nil
		}
	}
	return keys
}

func matchesUniformKeys(item any, keys []string) bool {
	m, ok := item.(map[string]any)
	if !ok || len(m) != len(keys) {
		return false
	}
	for _, k := range keys {
		if _, exists := m[k]; !exists {
			return false
		}
	}
	return true
}

func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func renderValueRow(m map[string]any, headers []string) string {
	parts := make([]string, len(headers))
	for i, k := range headers {
		parts[i] = formatScalar(m[k])
	}
	return strings.Join(parts, " ")
}

func sanitizeLine(s string) string {
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "\r", "")
	if len(s) > 80 {
		s = s[:77] + "..."
	}
	return s
}

// nil/false/zero all collapse to "-" so the compact table stays token-cheap;
// callers can't distinguish "absent" from "explicit zero" from this alone.
func formatScalar(v any) string {
	switch sv := v.(type) {
	case nil:
		return "-"
	case bool:
		return formatBool(sv)
	case float64:
		return formatFloat(sv)
	case json.Number:
		return formatJSONNumber(sv)
	case int:
		return formatInt(int64(sv))
	case int64:
		return formatInt(sv)
	case string:
		return formatString(sv)
	}
	return marshalScalar(v)
}

func formatJSONNumber(n json.Number) string {
	if jsonNumberIsZero(n) {
		return "-"
	}
	return n.String()
}

func jsonNumberIsZero(n json.Number) bool {
	f, _ := n.Float64() // n is always decoder-produced from a valid JSON number literal, so this cannot fail
	return f == 0
}

func marshalScalar(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

func formatBool(b bool) string {
	if b {
		return "true"
	}
	return "-"
}

func formatFloat(f float64) string {
	if f == 0 {
		return "-"
	}
	if f == math.Trunc(f) && math.Abs(f) < 1e15 {
		return fmt.Sprintf("%d", int64(f))
	}
	return fmt.Sprintf("%g", f)
}

func formatString(s string) string {
	if s == "" {
		return "-"
	}
	return sanitizeLine(s)
}

func formatInt(n int64) string {
	if n == 0 {
		return "-"
	}
	return fmt.Sprintf("%d", n)
}

type fieldGroups struct {
	nums []string
	strs []string
	arrs []string
}

func renderItemLine(m map[string]any) string {
	var g fieldGroups
	for k, v := range m {
		g.classifyField(k, v)
	}
	return strings.Join(append(append(g.nums, g.strs...), g.arrs...), " ")
}

func (g *fieldGroups) classifyField(k string, v any) {
	switch sv := v.(type) {
	case string:
		g.strs = classifyString(g.strs, k, sv)
	case []any:
		g.arrs = classifyArray(g.arrs, k, sv)
	default:
		g.nums = classifyNumeric(g.nums, k, v)
	}
}

func classifyNumeric(nums []string, k string, v any) []string {
	switch sv := v.(type) {
	case float64:
		return appendNumeric(nums, sv != 0, fmt.Sprintf("%s:%s", k, formatFloat(sv)))
	case json.Number:
		return appendNumeric(nums, !jsonNumberIsZero(sv), fmt.Sprintf("%s:%s", k, sv.String()))
	case int:
		return appendNumeric(nums, sv != 0, fmt.Sprintf("%s:%d", k, sv))
	case int64:
		return appendNumeric(nums, sv != 0, fmt.Sprintf("%s:%d", k, sv))
	case bool:
		return appendNumeric(nums, sv, "+"+k)
	}
	return nums
}

func appendNumeric(nums []string, keep bool, value string) []string {
	if keep {
		return append(nums, value)
	}
	return nums
}

func classifyString(strs []string, k, sv string) []string {
	if sv != "" {
		return append(strs, fmt.Sprintf("%s:%s", k, sanitizeLine(sv)))
	}
	return strs
}

func classifyArray(arrs []string, k string, sv []any) []string {
	if s := renderStringArray(k, sv); s != "" {
		return append(arrs, s)
	}
	return arrs
}

func renderStringArray(k string, items []any) string {
	var parts []string
	for _, elem := range items {
		if s, ok := elem.(string); ok {
			parts = append(parts, s)
		}
	}
	if len(parts) == 0 {
		return ""
	}
	return fmt.Sprintf("%s:[%s]", k, strings.Join(parts, ","))
}
