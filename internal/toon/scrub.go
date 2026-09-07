package toon

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"reflect"
)

type scrubVisit struct {
	kind reflect.Kind
	typ  reflect.Type
	ptr  uintptr
	len  int
	cap  int
}

type scrubState struct {
	active map[scrubVisit]bool
}

func scrubNonFinite(v any) (any, error) {
	return scrubValue(reflect.ValueOf(v), &scrubState{active: make(map[scrubVisit]bool)})
}

func scrubValue(rv reflect.Value, state *scrubState) (any, error) {
	if raw, err, done := marshalSubtree(rv); done {
		return raw, err
	}
	if !rv.IsValid() {
		return nil, nil
	}
	return scrubKind(rv, state)
}

func scrubKind(rv reflect.Value, state *scrubState) (any, error) {
	switch rv.Kind() {
	case reflect.Float32, reflect.Float64:
		return scrubFloat(rv), nil
	case reflect.Ptr:
		return scrubPointer(rv, state)
	case reflect.Interface:
		if rv.IsNil() {
			return nil, nil
		}
		return scrubValue(rv.Elem(), state)
	case reflect.Slice, reflect.Array:
		return scrubSequence(rv, state)
	case reflect.Map:
		return scrubGenericMap(rv, state)
	case reflect.Struct:
		return scrubStruct(rv, state)
	}
	return nil, fmt.Errorf("encoding/json: unable to normalize %s", rv.Type())
}

func marshalSubtree(rv reflect.Value) (any, error, bool) {
	if !rv.IsValid() {
		return nil, nil, true
	}
	input, ok := marshalInput(rv)
	if !ok {
		return nil, nil, false
	}
	raw, err := json.Marshal(input)
	if err == nil {
		return json.RawMessage(raw), nil, true
	}
	if isNonFiniteFloatError(err) {
		if usesCustomMarshaler(rv) {
			return nil, err, true
		}
		return nil, nil, false
	}
	return nil, err, true
}

func marshalInput(rv reflect.Value) (any, bool) {
	if !rv.CanInterface() {
		return nil, false
	}
	if rv.Kind() != reflect.Pointer && rv.CanAddr() {
		return rv.Addr().Interface(), true
	}
	return rv.Interface(), true
}

func pointerMarshaler(t reflect.Type) bool {
	p := reflect.PointerTo(t)
	return p.Implements(jsonMarshalerType) || p.Implements(textMarshalerType)
}

func scrubFloat(rv reflect.Value) any {
	f := rv.Float()
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return nil
	}
	return rv.Interface()
}

func scrubPointer(rv reflect.Value, state *scrubState) (any, error) {
	if rv.IsNil() {
		return nil, nil
	}
	if err := state.enter(rv); err != nil {
		return nil, err
	}
	defer state.leave(rv)
	return scrubValue(rv.Elem(), state)
}

func scrubSequence(rv reflect.Value, state *scrubState) (any, error) {
	if rv.Kind() == reflect.Slice && rv.IsNil() {
		return nil, nil
	}
	if err := state.enter(rv); err != nil {
		return nil, err
	}
	defer state.leave(rv)
	out := make([]any, rv.Len())
	for i := range out {
		value, err := scrubValue(rv.Index(i), state)
		if err != nil {
			return nil, err
		}
		out[i] = value
	}
	return out, nil
}

func scrubGenericMap(rv reflect.Value, state *scrubState) (any, error) {
	if rv.IsNil() {
		return nil, nil
	}
	if err := state.enter(rv); err != nil {
		return nil, err
	}
	defer state.leave(rv)
	out := make(map[string]any, rv.Len())
	iter := rv.MapRange()
	for iter.Next() {
		k, ok := jsonMapKey(iter.Key())
		if !ok {
			return nil, fmt.Errorf("encoding/json: unsupported map key type %s", rv.Type().Key())
		}
		value, err := scrubValue(iter.Value(), state)
		if err != nil {
			return nil, err
		}
		out[k] = value
	}
	return out, nil
}

type orderedEntry struct {
	key string
	val any
}

// orderedObject serializes its entries in insertion order, satisfying TOON
// §2 and §8 which require object key order to be preserved as encountered
// by the encoder. map[string]any cannot be used here because json.Marshal
// sorts map keys alphabetically.
type orderedObject []orderedEntry

func (o orderedObject) MarshalJSON() ([]byte, error) {
	var buf bytes.Buffer
	buf.WriteByte('{')
	for i, e := range o {
		if i > 0 {
			buf.WriteByte(',')
		}
		if err := writeOrderedEntry(&buf, e); err != nil {
			return nil, err
		}
	}
	buf.WriteByte('}')
	return buf.Bytes(), nil
}

func writeOrderedEntry(buf *bytes.Buffer, e orderedEntry) error {
	key, err := json.Marshal(e.key)
	if err != nil {
		return err
	}
	val, err := json.Marshal(e.val)
	if err != nil {
		return err
	}
	buf.Write(key)
	buf.WriteByte(':')
	buf.Write(val)
	return nil
}

func scrubStruct(rv reflect.Value, state *scrubState) (any, error) {
	fields := scrubFields(rv.Type())
	out := make(orderedObject, 0, len(fields))
	for _, f := range fields {
		fv, ok := fieldByIndex(rv, f.index)
		if !ok || f.omitEmpty && isJSONEmptyValue(fv) || f.omitZero && isJSONZeroValue(fv) {
			continue
		}
		value, err := scrubFieldValue(fv, f, state)
		if err != nil {
			return nil, err
		}
		out = append(out, orderedEntry{key: f.name, val: value})
	}
	return out, nil
}

func scrubFieldValue(rv reflect.Value, field scrubField, state *scrubState) (any, error) {
	value, err := scrubValue(rv, state)
	if err != nil || !field.quoted || usesCustomMarshaler(rv) || isJSONNull(value) {
		return value, err
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	quoted, err := json.Marshal(string(raw))
	if err != nil {
		return nil, err
	}
	return json.RawMessage(quoted), nil
}

func usesCustomMarshaler(rv reflect.Value) bool {
	if !rv.IsValid() {
		return false
	}
	t := rv.Type()
	if t.Implements(jsonMarshalerType) || t.Implements(textMarshalerType) {
		return true
	}
	return rv.Kind() != reflect.Pointer && rv.CanAddr() && pointerMarshaler(t)
}

func isJSONNull(v any) bool {
	if v == nil {
		return true
	}
	raw, ok := v.(json.RawMessage)
	return ok && string(raw) == "null"
}

func (s *scrubState) enter(rv reflect.Value) error {
	visit, ok := makeScrubVisit(rv)
	if !ok {
		return nil
	}
	if s.active[visit] {
		return fmt.Errorf("encoding/json: unsupported value: encountered a cycle via %s", rv.Type())
	}
	s.active[visit] = true
	return nil
}

func (s *scrubState) leave(rv reflect.Value) {
	if visit, ok := makeScrubVisit(rv); ok {
		delete(s.active, visit)
	}
}

func makeScrubVisit(rv reflect.Value) (scrubVisit, bool) {
	if !rv.IsValid() {
		return scrubVisit{}, false
	}
	switch rv.Kind() {
	case reflect.Pointer, reflect.Map:
		if rv.IsNil() {
			return scrubVisit{}, false
		}
		return scrubVisit{kind: rv.Kind(), typ: rv.Type(), ptr: rv.Pointer()}, true
	case reflect.Slice:
		if rv.IsNil() || rv.Len() == 0 {
			return scrubVisit{}, false
		}
		return scrubVisit{kind: rv.Kind(), typ: rv.Type(), ptr: rv.Pointer(), len: rv.Len(), cap: rv.Cap()}, true
	default:
		return scrubVisit{}, false
	}
}
