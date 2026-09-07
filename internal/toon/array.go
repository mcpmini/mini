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
// See https://github.com/toon-format/spec/blob/main/SPEC.md#94-mixed--non-uniform-arrays--expanded-list
type arrayCtx struct {
	Key          string
	ItemDepth    int
	AllowTabular bool
	FieldEmpty   bool
}

// tabularCol describes one column in a tabular header. Children is nil for
// primitive leaf columns and non-nil for nested-uniform object columns (§9.3).
type tabularCol struct {
	key      string
	children []tabularCol
}

func writeArray(sb *strings.Builder, items []Value, ctx arrayCtx) error {
	if len(items) == 0 {
		return writeEmptyArray(sb, ctx)
	}
	if allPrimitive(items) {
		return writeInlineArray(sb, items, ctx.Key)
	}
	if cols, ok := tabularFields(items); ok && ctx.AllowTabular {
		return writeTabularArray(sb, items, cols, ctx)
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

func writeTabularArray(sb *strings.Builder, items []Value, cols []tabularCol, ctx arrayCtx) error {
	if err := checkDepth(ctx.ItemDepth); err != nil {
		return err
	}
	if err := writeTabularHeader(sb, ctx.Key, len(items), cols); err != nil {
		return err
	}
	indent := strings.Repeat(indentUnit, ctx.ItemDepth)
	for _, it := range items {
		if err := writeTabularRow(sb, indent, leafValues(it, cols)); err != nil {
			return err
		}
	}
	return nil
}

func writeTabularHeader(sb *strings.Builder, key string, count int, cols []tabularCol) error {
	if err := appendString(sb, key); err != nil {
		return err
	}
	if err := appendString(sb, "["+strconv.Itoa(count)+"]{"); err != nil {
		return err
	}
	if err := writeColHeaders(sb, cols); err != nil {
		return err
	}
	return appendString(sb, "}:\n")
}

func writeColHeaders(sb *strings.Builder, cols []tabularCol) error {
	for i, col := range cols {
		if i > 0 {
			if err := appendString(sb, ","); err != nil {
				return err
			}
		}
		if err := writeColHeader(sb, col); err != nil {
			return err
		}
	}
	return nil
}

func writeColHeader(sb *strings.Builder, col tabularCol) error {
	if err := validateUTF8(col.key); err != nil {
		return err
	}
	if err := appendString(sb, encodeKey(col.key)); err != nil {
		return err
	}
	if len(col.children) == 0 {
		return nil
	}
	if err := appendString(sb, "{"); err != nil {
		return err
	}
	if err := writeColHeaders(sb, col.children); err != nil {
		return err
	}
	return appendString(sb, "}")
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
// See https://github.com/toon-format/spec/blob/main/SPEC.md#10-objects-as-list-items
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

// §9.3 eligibility: shared key set across all elements, every column is
// uniform-primitive or nested-uniform (recursive). Header order follows the first element.
// See https://github.com/toon-format/spec/blob/main/SPEC.md#93-arrays-of-objects--tabular-form
func tabularFields(items []Value) ([]tabularCol, bool) {
	if items[0].Kind != KindObject || len(items[0].Fields) == 0 {
		return nil, false
	}
	keys := fieldKeys(items[0])
	set, ok := noDupKeySet(keys)
	if !ok {
		return nil, false
	}
	for _, it := range items[1:] {
		if !matchesKeySet(it, set) {
			return nil, false
		}
	}
	return classifyColumns(items, keys)
}

func classifyColumns(items []Value, keys []string) ([]tabularCol, bool) {
	cols := make([]tabularCol, len(keys))
	for i, key := range keys {
		col, ok := classifyColumn(items, key)
		if !ok {
			return nil, false
		}
		cols[i] = col
	}
	return cols, true
}

func classifyColumn(items []Value, key string) (tabularCol, bool) {
	vals := columnValues(items, key)
	if allPrimitive(vals) {
		return tabularCol{key: key}, true
	}
	if !allNonEmptyObjects(vals) {
		return tabularCol{}, false
	}
	return classifyNestedColumn(key, vals)
}

func classifyNestedColumn(key string, vals []Value) (tabularCol, bool) {
	subKeys := fieldKeys(vals[0])
	subSet, ok := noDupKeySet(subKeys)
	if !ok {
		return tabularCol{}, false
	}
	for _, v := range vals[1:] {
		if !matchesKeySet(v, subSet) {
			return tabularCol{}, false
		}
	}
	children, ok := classifyColumns(vals, subKeys)
	if !ok {
		return tabularCol{}, false
	}
	return tabularCol{key: key, children: children}, true
}

func columnValues(items []Value, key string) []Value {
	vals := make([]Value, len(items))
	for i, it := range items {
		for _, f := range it.Fields {
			if f.Key == key {
				vals[i] = f.Val
				break
			}
		}
	}
	return vals
}

func allNonEmptyObjects(vals []Value) bool {
	for _, v := range vals {
		if v.Kind != KindObject || len(v.Fields) == 0 {
			return false
		}
	}
	return true
}

func fieldKeys(v Value) []string {
	keys := make([]string, len(v.Fields))
	for i, f := range v.Fields {
		keys[i] = f.Key
	}
	return keys
}

func noDupKeySet(keys []string) (map[string]bool, bool) {
	set := make(map[string]bool, len(keys))
	for _, k := range keys {
		if set[k] {
			return nil, false
		}
		set[k] = true
	}
	return set, true
}

func leafValues(obj Value, cols []tabularCol) []Value {
	var vals []Value
	for _, col := range cols {
		v := fieldValuesByKey(obj, []string{col.key})[0]
		if len(col.children) == 0 {
			vals = append(vals, v)
		} else {
			vals = append(vals, leafValues(v, col.children)...)
		}
	}
	return vals
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

func matchesKeySet(v Value, set map[string]bool) bool {
	if v.Kind != KindObject || len(v.Fields) != len(set) {
		return false
	}
	seen := make(map[string]bool, len(set))
	for _, f := range v.Fields {
		if !set[f.Key] || seen[f.Key] {
			return false
		}
		seen[f.Key] = true
	}
	return true
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
