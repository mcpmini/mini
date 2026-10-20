package toon

import (
	"strconv"
	"strings"
)

func keyedTabularCols(v Value) ([]tabularCol, bool) {
	if len(v.fields()) < 2 {
		return nil, false
	}
	entries := entryValues(v)
	if !allNonEmptyObjects(entries) {
		return nil, false
	}
	keys := fieldKeys(entries[0])
	set, ok := noDupKeySet(keys)
	if !ok {
		return nil, false
	}
	for _, ev := range entries[1:] {
		if !matchesKeySet(ev, set) {
			return nil, false
		}
	}
	indexes := make([]map[string]Value, len(entries))
	for i, e := range entries {
		indexes[i] = fieldIndex(e)
	}
	return classifyColumns(indexes, keys)
}

func entryValues(v Value) []Value {
	vals := make([]Value, len(v.fields()))
	for i, f := range v.fields() {
		vals[i] = f.Val
	}
	return vals
}

type keyedTabularParams struct {
	builder    *strings.Builder
	key        string
	object     Value
	columns    []tabularCol
	entryDepth int
}

type keyedEntryParams struct {
	builder *strings.Builder
	indent  string
	field   Field
	columns []tabularCol
}

func writeKeyedTabular(p keyedTabularParams) error {
	if err := checkDepth(p.entryDepth); err != nil {
		return err
	}
	if err := writeKeyedHeader(tabularHeaderParams{
		builder: p.builder, key: p.key, count: len(p.object.fields()), columns: p.columns,
	}); err != nil {
		return err
	}
	indent := strings.Repeat(indentUnit, p.entryDepth)
	for _, f := range p.object.fields() {
		if err := writeKeyedEntry(keyedEntryParams{
			builder: p.builder, indent: indent, field: f, columns: p.columns,
		}); err != nil {
			return err
		}
	}
	return nil
}

func writeKeyedHeader(p tabularHeaderParams) error {
	if err := appendString(p.builder, p.key); err != nil {
		return err
	}
	if err := appendString(p.builder, "["+strconv.Itoa(p.count)+":]{"); err != nil {
		return err
	}
	if err := writeColHeaders(p.builder, p.columns); err != nil {
		return err
	}
	return appendString(p.builder, "}:\n")
}

func writeKeyedEntry(p keyedEntryParams) error {
	if err := validateUTF8(p.field.Key); err != nil {
		return err
	}
	cells, err := joinPrimitives(leafValues(fieldIndex(p.field.Val), p.columns))
	if err != nil {
		return err
	}
	if err := appendString(p.builder, p.indent); err != nil {
		return err
	}
	if err := appendString(p.builder, encodeKey(p.field.Key)+": "); err != nil {
		return err
	}
	return appendString(p.builder, cells+"\n")
}
