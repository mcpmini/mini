package toon

import (
	"strconv"
	"strings"
)

func keyedTabularCols(v Value) ([]tabularCol, bool) {
	if len(v.Fields) < 2 {
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
	return classifyColumns(entries, keys)
}

func entryValues(v Value) []Value {
	vals := make([]Value, len(v.Fields))
	for i, f := range v.Fields {
		vals[i] = f.Val
	}
	return vals
}

func writeKeyedTabular(sb *strings.Builder, key string, obj Value, cols []tabularCol, entryDepth int) error {
	if err := checkDepth(entryDepth); err != nil {
		return err
	}
	if err := writeKeyedHeader(sb, key, len(obj.Fields), cols); err != nil {
		return err
	}
	indent := strings.Repeat(indentUnit, entryDepth)
	for _, f := range obj.Fields {
		if err := writeKeyedEntry(sb, indent, f, cols); err != nil {
			return err
		}
	}
	return nil
}

func writeKeyedHeader(sb *strings.Builder, key string, count int, cols []tabularCol) error {
	if err := appendString(sb, key); err != nil {
		return err
	}
	if err := appendString(sb, "["+strconv.Itoa(count)+":]{"); err != nil {
		return err
	}
	if err := writeColHeaders(sb, cols); err != nil {
		return err
	}
	return appendString(sb, "}:\n")
}

func writeKeyedEntry(sb *strings.Builder, indent string, f Field, cols []tabularCol) error {
	if err := validateUTF8(f.Key); err != nil {
		return err
	}
	cells, err := joinPrimitives(leafValues(f.Val, cols))
	if err != nil {
		return err
	}
	if err := appendString(sb, indent+encodeKey(f.Key)+": "+cells+"\n"); err != nil {
		return err
	}
	return nil
}
