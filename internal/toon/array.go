package toon

import (
	"fmt"
	"strings"
)

// arrayCtx positions an array whose header-line prefix (indent or "- ") the
// caller has already written. ItemDepth is where rows/list items go: field
// depth+1 for keyed fields, hyphen depth+1 for keyless list-item arrays
// (§9.4), 1 at the root. AllowTabular is false in keyless list-item position
// where §9.4 forbids tabular form. FieldEmpty selects §9.1's `key: []` over
// §9.2's `[0]:` for empty arrays.
type arrayCtx struct {
	Key          string
	ItemDepth    int
	AllowTabular bool
	FieldEmpty   bool
}

func writeArray(sb *strings.Builder, items []Value, ctx arrayCtx) error {
	if len(items) == 0 {
		writeEmptyArray(sb, ctx)
		return nil
	}
	if allPrimitive(items) {
		return writeInlineArray(sb, items, ctx.Key)
	}
	if fields, ok := tabularFields(items); ok && ctx.AllowTabular {
		return writeTabularArray(sb, items, fields, ctx)
	}
	fmt.Fprintf(sb, "%s[%d]:\n", ctx.Key, len(items))
	return writeListItems(sb, items, ctx.ItemDepth)
}

func writeEmptyArray(sb *strings.Builder, ctx arrayCtx) {
	if ctx.FieldEmpty {
		sb.WriteString(ctx.Key + ": []\n")
		return
	}
	sb.WriteString("[0]:\n")
}

func writeInlineArray(sb *strings.Builder, items []Value, encodedKey string) error {
	row, err := joinPrimitives(items)
	if err != nil {
		return err
	}
	fmt.Fprintf(sb, "%s[%d]: %s\n", encodedKey, len(items), row)
	return nil
}

func writeTabularArray(sb *strings.Builder, items []Value, fields []string, ctx arrayCtx) error {
	names := make([]string, len(fields))
	for i, f := range fields {
		names[i] = encodeKey(f)
	}
	fmt.Fprintf(sb, "%s[%d]{%s}:\n", ctx.Key, len(items), strings.Join(names, ","))
	indent := strings.Repeat(indentUnit, ctx.ItemDepth)
	for _, it := range items {
		row, err := joinPrimitives(fieldValuesByKey(it, fields))
		if err != nil {
			return err
		}
		sb.WriteString(indent + row + "\n")
	}
	return nil
}

func writeListItems(sb *strings.Builder, items []Value, depth int) error {
	for _, it := range items {
		if err := writeListItem(sb, it, depth); err != nil {
			return err
		}
	}
	return nil
}

func writeListItem(sb *strings.Builder, item Value, depth int) error {
	switch item.Kind {
	case KindObject:
		return writeObjectListItem(sb, item, depth)
	case KindArray:
		sb.WriteString(strings.Repeat(indentUnit, depth) + "- ")
		return writeArray(sb, item.Items, arrayCtx{ItemDepth: depth + 1})
	default:
		s, err := encodePrimitive(item)
		if err != nil {
			return err
		}
		sb.WriteString(strings.Repeat(indentUnit, depth) + "- " + s + "\n")
		return nil
	}
}

// writeObjectListItem implements §10: the first field shares the hyphen line
// and all fields render at hyphen depth+1, which lands tabular rows and
// nested list items of a leading array field at hyphen depth+2.
func writeObjectListItem(sb *strings.Builder, item Value, depth int) error {
	indent := strings.Repeat(indentUnit, depth)
	if len(item.Fields) == 0 {
		sb.WriteString(indent + "-\n")
		return nil
	}
	sb.WriteString(indent + "- ")
	if err := writeFieldBody(sb, item.Fields[0], depth+1); err != nil {
		return err
	}
	return writeFields(sb, item.Fields[1:], depth+1)
}

func joinPrimitives(items []Value) (string, error) {
	parts := make([]string, len(items))
	for i, it := range items {
		s, err := encodePrimitive(it)
		if err != nil {
			return "", err
		}
		parts[i] = s
	}
	return strings.Join(parts, ","), nil
}

// tabularFields reports §9.3 eligibility: every element is a non-empty object
// over one shared key set (order per object may vary) with primitive values
// only. Header order comes from the first element.
func tabularFields(items []Value) ([]string, bool) {
	keys, ok := primitiveObjectKeys(items[0])
	if !ok || len(keys) == 0 {
		return nil, false
	}
	set := make(map[string]bool, len(keys))
	for _, k := range keys {
		if set[k] {
			return nil, false
		}
		set[k] = true
	}
	for _, it := range items[1:] {
		if !matchesKeySet(it, set) {
			return nil, false
		}
	}
	return keys, true
}

func primitiveObjectKeys(v Value) ([]string, bool) {
	if v.Kind != KindObject {
		return nil, false
	}
	keys := make([]string, len(v.Fields))
	for i, f := range v.Fields {
		if !isPrimitive(f.Val) {
			return nil, false
		}
		keys[i] = f.Key
	}
	return keys, true
}

func matchesKeySet(v Value, set map[string]bool) bool {
	if v.Kind != KindObject || len(v.Fields) != len(set) {
		return false
	}
	seen := make(map[string]bool, len(set))
	for _, f := range v.Fields {
		if !set[f.Key] || seen[f.Key] || !isPrimitive(f.Val) {
			return false
		}
		seen[f.Key] = true
	}
	return true
}

func fieldValuesByKey(obj Value, keys []string) []Value {
	vals := make([]Value, len(keys))
	for i, k := range keys {
		for _, f := range obj.Fields {
			if f.Key == k {
				vals[i] = f.Val
				break
			}
		}
	}
	return vals
}

func allPrimitive(items []Value) bool {
	for _, it := range items {
		if !isPrimitive(it) {
			return false
		}
	}
	return true
}

func isPrimitive(v Value) bool {
	switch v.Kind {
	case KindNull, KindBool, KindNumber, KindString:
		return true
	}
	return false
}
