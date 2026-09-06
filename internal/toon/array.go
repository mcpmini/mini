package toon

import (
	"strconv"
	"strings"
)

// arrayCtx positions an array whose header-line prefix (indent or "- ") the
// caller has already written. ItemDepth is where rows/list items go: field
// depth+1 for keyed fields, hyphen depth+1 for keyless list-item arrays
// (§9.4), 1 at the root. AllowTabular is false in keyless list-item position
// where §9.4 forbids tabular form. FieldEmpty selects §9.1's `key: []` over
// §9.2's `[0]:` for empty arrays.
// See https://github.com/toon-format/spec/blob/f55b93ac489f297ff597d95e4c19ae84675eaeb7/SPEC.md#94-mixed--non-uniform-arrays--expanded-list
type arrayCtx struct {
	Key          string
	ItemDepth    int
	AllowTabular bool
	FieldEmpty   bool
}

func writeArray(sb *strings.Builder, items []Value, ctx arrayCtx) error {
	if len(items) == 0 {
		return writeEmptyArray(sb, ctx)
	}
	if allPrimitive(items) {
		return writeInlineArray(sb, items, ctx.Key)
	}
	if fields, ok := tabularFields(items); ok && ctx.AllowTabular {
		return writeTabularArray(sb, items, fields, ctx)
	}
	if err := appendString(sb, ctx.Key); err != nil {
		return err
	}
	if err := appendString(sb, "["+strconv.Itoa(len(items))+"]:\n"); err != nil {
		return err
	}
	return writeListItems(sb, items, ctx.ItemDepth)
}

func writeEmptyArray(sb *strings.Builder, ctx arrayCtx) error {
	if ctx.FieldEmpty {
		return appendString(sb, ctx.Key+": []\n")
	}
	return appendString(sb, "[0]:\n")
}

func writeInlineArray(sb *strings.Builder, items []Value, encodedKey string) error {
	row, err := joinPrimitives(items)
	if err != nil {
		return err
	}
	if err := appendString(sb, encodedKey); err != nil {
		return err
	}
	if err := appendString(sb, "["+strconv.Itoa(len(items))+"]: "); err != nil {
		return err
	}
	if err := appendString(sb, row); err != nil {
		return err
	}
	return appendString(sb, "\n")
}

func writeTabularArray(sb *strings.Builder, items []Value, fields []string, ctx arrayCtx) error {
	if err := checkDepth(ctx.ItemDepth); err != nil {
		return err
	}
	names := make([]string, len(fields))
	for i, f := range fields {
		if err := validateUTF8(f); err != nil {
			return err
		}
		names[i] = encodeKey(f)
	}
	if err := writeTabularHeader(sb, ctx.Key, len(items), names); err != nil {
		return err
	}
	indent := strings.Repeat(indentUnit, ctx.ItemDepth)
	for _, it := range items {
		if err := writeTabularRow(sb, indent, fieldValuesByKey(it, fields)); err != nil {
			return err
		}
	}
	return nil
}

func writeTabularHeader(sb *strings.Builder, key string, count int, names []string) error {
	if err := appendString(sb, key); err != nil {
		return err
	}
	if err := appendString(sb, "["+strconv.Itoa(count)+"]{"); err != nil {
		return err
	}
	for i, name := range names {
		if i > 0 {
			if err := appendString(sb, ","); err != nil {
				return err
			}
		}
		if err := appendString(sb, name); err != nil {
			return err
		}
	}
	if err := appendString(sb, "}:\n"); err != nil {
		return err
	}
	return nil
}

func writeTabularRow(sb *strings.Builder, indent string, values []Value) error {
	row, err := joinPrimitives(values)
	if err != nil {
		return err
	}
	if err := appendString(sb, indent); err != nil {
		return err
	}
	if err := appendString(sb, row); err != nil {
		return err
	}
	if err := appendString(sb, "\n"); err != nil {
		return err
	}
	return nil
}

func writeListItems(sb *strings.Builder, items []Value, depth int) error {
	if err := checkDepth(depth); err != nil {
		return err
	}
	if sb.Len() > maxEncodeBytes {
		return errEncodeTooLarge
	}
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
		if err := appendString(sb, strings.Repeat(indentUnit, depth)+"- "); err != nil {
			return err
		}
		return writeArray(sb, item.Items, arrayCtx{ItemDepth: depth + 1})
	default:
		s, err := encodePrimitive(item)
		if err != nil {
			return err
		}
		return appendString(sb, strings.Repeat(indentUnit, depth)+"- "+s+"\n")
	}
}

// §10: the first field shares the hyphen line; all others render at hyphen depth+1.
// See https://github.com/toon-format/spec/blob/f55b93ac489f297ff597d95e4c19ae84675eaeb7/SPEC.md#10-objects-as-list-items
func writeObjectListItem(sb *strings.Builder, item Value, depth int) error {
	indent := strings.Repeat(indentUnit, depth)
	if len(item.Fields) == 0 {
		return appendString(sb, indent+"-\n")
	}
	if err := appendString(sb, indent+"- "); err != nil {
		return err
	}
	if err := writeFieldBody(sb, item.Fields[0], depth+1); err != nil {
		return err
	}
	return writeFields(sb, item.Fields[1:], depth+1)
}

func joinPrimitives(items []Value) (string, error) {
	var sb strings.Builder
	for i, it := range items {
		s, err := encodePrimitive(it)
		if err != nil {
			return "", err
		}
		if i > 0 {
			if err := appendString(&sb, ","); err != nil {
				return "", err
			}
		}
		if err := appendString(&sb, s); err != nil {
			return "", err
		}
	}
	return sb.String(), nil
}

// §9.3 eligibility: shared primitive-value key set across all elements; header order comes from the first element.
// See https://github.com/toon-format/spec/blob/f55b93ac489f297ff597d95e4c19ae84675eaeb7/SPEC.md#93-arrays-of-objects--tabular-form
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
