package toon

import (
	"strconv"
	"strings"
)

// arrayCtx positions an array whose header-line prefix (indent or "- ") the
// caller has already written. ItemDepth is where rows/list items go: field
// depth+1 for keyed fields, hyphen depth+1 for keyless list-item arrays
// (§9.4: https://github.com/toon-format/spec/blob/62f16b369408180f1faf1cba7da1b46d1f336f12/SPEC.md?plain=1#L531),
// 1 at the root. AllowTabular is false in keyless list-item position
// where §9.4 forbids tabular form. FieldEmpty selects §9.1
// (https://github.com/toon-format/spec/blob/62f16b369408180f1faf1cba7da1b46d1f336f12/SPEC.md?plain=1#L467)
// `key: []` over §9.2
// (https://github.com/toon-format/spec/blob/62f16b369408180f1faf1cba7da1b46d1f336f12/SPEC.md?plain=1#L481)
// `[0]:` for empty arrays.
type arrayCtx struct {
	Key          string
	ItemDepth    int
	AllowTabular bool
	FieldEmpty   bool
}

// tabularCol describes one column in a tabular header. Children is nil for
// primitive leaf columns and non-nil for nested-uniform object columns
// (§9.3: https://github.com/toon-format/spec/blob/62f16b369408180f1faf1cba7da1b46d1f336f12/SPEC.md?plain=1#L495).
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
		return writeTabularArray(tabularArrayParams{builder: sb, items: items, columns: cols, context: ctx})
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

type tabularArrayParams struct {
	builder *strings.Builder
	items   []Value
	columns []tabularCol
	context arrayCtx
}

type tabularHeaderParams struct {
	builder *strings.Builder
	key     string
	count   int
	columns []tabularCol
}

func writeTabularArray(p tabularArrayParams) error {
	if err := checkDepth(p.context.ItemDepth); err != nil {
		return err
	}
	if err := writeTabularHeader(tabularHeaderParams{
		builder: p.builder, key: p.context.Key, count: len(p.items), columns: p.columns,
	}); err != nil {
		return err
	}
	indent := strings.Repeat(indentUnit, p.context.ItemDepth)
	for _, it := range p.items {
		if err := writeTabularRow(p.builder, indent, leafValues(fieldIndex(it), p.columns)); err != nil {
			return err
		}
	}
	return nil
}

func writeTabularHeader(p tabularHeaderParams) error {
	if err := appendString(p.builder, p.key); err != nil {
		return err
	}
	if err := appendString(p.builder, "["+strconv.Itoa(p.count)+"]{"); err != nil {
		return err
	}
	if err := writeColHeaders(p.builder, p.columns); err != nil {
		return err
	}
	return appendString(p.builder, "}:\n")
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
	switch item.kind() {
	case KindObject:
		return writeObjectListItem(sb, item, depth)
	case KindArray:
		if err := appendString(sb, strings.Repeat(indentUnit, depth)+"- "); err != nil {
			return err
		}
		return writeArray(sb, item.items(), arrayCtx{ItemDepth: depth + 1})
	case KindNull, KindBool, KindNumber, KindString:
		return writePrimitiveListItem(sb, item, depth)
	}
	return writePrimitiveListItem(sb, item, depth)
}

func writePrimitiveListItem(sb *strings.Builder, item Value, depth int) error {
	s, err := encodePrimitive(item)
	if err != nil {
		return err
	}
	return appendString(sb, strings.Repeat(indentUnit, depth)+"- "+s+"\n")
}

// §10: the first field shares the hyphen line; all others render at hyphen depth+1.
// https://github.com/toon-format/spec/blob/62f16b369408180f1faf1cba7da1b46d1f336f12/SPEC.md?plain=1#L577
func writeObjectListItem(sb *strings.Builder, item Value, depth int) error {
	indent := strings.Repeat(indentUnit, depth)
	if len(item.fields()) == 0 {
		return appendString(sb, indent+"-\n")
	}
	if err := appendString(sb, indent+"- "); err != nil {
		return err
	}
	if err := writeFieldBody(sb, item.fields()[0], depth+1); err != nil {
		return err
	}
	return writeFields(sb, item.fields()[1:], depth+1)
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
// https://github.com/toon-format/spec/blob/62f16b369408180f1faf1cba7da1b46d1f336f12/SPEC.md?plain=1#L495
func tabularFields(items []Value) ([]tabularCol, bool) {
	if items[0].kind() != KindObject || len(items[0].fields()) == 0 {
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
	indexes := make([]map[string]Value, len(items))
	for i, it := range items {
		indexes[i] = fieldIndex(it)
	}
	return classifyColumns(indexes, keys)
}

func classifyColumns(indexes []map[string]Value, keys []string) ([]tabularCol, bool) {
	cols := make([]tabularCol, len(keys))
	for i, key := range keys {
		col, ok := classifyColumn(indexes, key)
		if !ok {
			return nil, false
		}
		cols[i] = col
	}
	return cols, true
}

func classifyColumn(indexes []map[string]Value, key string) (tabularCol, bool) {
	vals := columnValues(indexes, key)
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
	indexes := make([]map[string]Value, len(vals))
	for i, v := range vals {
		indexes[i] = fieldIndex(v)
	}
	children, ok := classifyColumns(indexes, subKeys)
	if !ok {
		return tabularCol{}, false
	}
	return tabularCol{key: key, children: children}, true
}

func columnValues(indexes []map[string]Value, key string) []Value {
	vals := make([]Value, len(indexes))
	for i, idx := range indexes {
		vals[i] = idx[key]
	}
	return vals
}

func fieldIndex(v Value) map[string]Value {
	idx := make(map[string]Value, len(v.fields()))
	for _, f := range v.fields() {
		idx[f.Key] = f.Val
	}
	return idx
}

func allNonEmptyObjects(vals []Value) bool {
	for _, v := range vals {
		if v.kind() != KindObject || len(v.fields()) == 0 {
			return false
		}
	}
	return true
}

func fieldKeys(v Value) []string {
	keys := make([]string, len(v.fields()))
	for i, f := range v.fields() {
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

func leafValues(idx map[string]Value, cols []tabularCol) []Value {
	var vals []Value
	for _, col := range cols {
		v := idx[col.key]
		if len(col.children) == 0 {
			vals = append(vals, v)
		} else {
			vals = append(vals, leafValues(fieldIndex(v), col.children)...)
		}
	}
	return vals
}

func matchesKeySet(v Value, set map[string]bool) bool {
	if v.kind() != KindObject || len(v.fields()) != len(set) {
		return false
	}
	seen := make(map[string]bool, len(set))
	for _, f := range v.fields() {
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
	switch v.kind() {
	case KindNull, KindBool, KindNumber, KindString:
		return true
	case KindObject, KindArray:
		return false
	}
	return false
}
