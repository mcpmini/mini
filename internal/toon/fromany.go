package toon

import (
	"encoding"
	"encoding/json"
	"errors"
	"math"
	"reflect"
	"strconv"
)

// FromAny converts an arbitrary Go value to a Value via json.Marshal then
// FromJSON, inheriting FromJSON's ordering and canonicalization. Spec §3
// requires NaN and +/-Infinity to normalize to null rather than fail the
// encode, so a non-finite-float marshal error triggers one retry against a
// null-substituted copy of v. The rescue applies to JSON-shaped data — bare
// floats, pointers, interfaces, maps, and slices. Structs are not walked; a
// non-finite float inside a struct surfaces encoding/json's UnsupportedValueError.
// Every other unsupported value (chan, func, unsupported map key types) still
// fails as before.
func FromAny(v any) (Value, error) {
	raw, err := json.Marshal(v)
	if err == nil {
		return FromJSON(raw)
	}
	if !isNonFiniteFloatError(err) {
		return Value{}, err
	}
	raw, err = json.Marshal(normalizeNonFinite(reflect.ValueOf(v)))
	if err != nil {
		return Value{}, err
	}
	return FromJSON(raw)
}

func isNonFiniteFloatError(err error) bool {
	var uve *json.UnsupportedValueError
	if !errors.As(err, &uve) {
		return false
	}
	k := uve.Value.Kind()
	return k == reflect.Float32 || k == reflect.Float64
}

var jsonMarshalerType = reflect.TypeFor[json.Marshaler]()

// normalizeNonFinite rescues a non-finite float by delegating each subtree to
// encoding/json and splicing the verbatim JSON; only the path containing a
// non-finite float is rebuilt, so unrelated values keep exact encoding/json
// semantics (nil maps stay null, float32 keeps its shortest representation,
// custom Marshaler/TextMarshaler output is preserved verbatim).
func normalizeNonFinite(rv reflect.Value) any {
	if !rv.IsValid() {
		return nil
	}
	if rv.CanInterface() {
		result, done := tryMarshalSubtree(rv)
		if done {
			return result
		}
		// A custom Marshaler that failed non-finite must surface its own error;
		// rebuilding it as a generic container would silently bypass its
		// representation.
		if rv.Type().Implements(jsonMarshalerType) {
			return rv.Interface()
		}
	}
	return normalizeByKind(rv)
}

// tryMarshalSubtree attempts to marshal the subtree with encoding/json.
// Returns (raw, true) on success. Returns (iface, true) on a non-non-finite
// error so the caller passes the original value to the outer retry, surfacing
// encoding/json's own error. Returns (nil, false) when the subtree itself
// contains a non-finite float and needs per-kind recursion.
func tryMarshalSubtree(rv reflect.Value) (any, bool) {
	b, err := json.Marshal(rv.Interface())
	if err == nil {
		return json.RawMessage(b), true
	}
	if !isNonFiniteFloatError(err) {
		return rv.Interface(), true
	}
	return nil, false
}

func normalizeByKind(rv reflect.Value) any {
	switch rv.Kind() {
	case reflect.Float32, reflect.Float64:
		return normalizeFloat(rv.Float())
	case reflect.Ptr, reflect.Interface:
		return normalizeIndirect(rv)
	case reflect.Slice, reflect.Array:
		return normalizeSliceNonFinite(rv)
	case reflect.Map:
		return normalizeMapNonFinite(rv)
	default:
		if rv.CanInterface() {
			return rv.Interface()
		}
		return nil
	}
}

func normalizeFloat(f float64) any {
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return nil
	}
	return f
}

func normalizeIndirect(rv reflect.Value) any {
	if rv.IsNil() {
		return nil
	}
	return normalizeNonFinite(rv.Elem())
}

// normalizeMapNonFinite rebuilds a map whose subtree contains a non-finite float.
// If any key is not resolvable by jsonMapKey, the original value is returned
// unchanged so the outer retry surfaces encoding/json's own unsupported-key error.
func normalizeMapNonFinite(rv reflect.Value) any {
	out := make(map[string]any, rv.Len())
	iter := rv.MapRange()
	for iter.Next() {
		k, ok := jsonMapKey(iter.Key())
		if !ok {
			return rv.Interface()
		}
		out[k] = normalizeNonFinite(iter.Value())
	}
	return out
}

func normalizeSliceNonFinite(rv reflect.Value) any {
	if rv.Kind() == reflect.Slice && rv.IsNil() {
		return nil
	}
	out := make([]any, rv.Len())
	for i := range out {
		out[i] = normalizeNonFinite(rv.Index(i))
	}
	return out
}

// jsonMapKey mirrors encoding/json's map-key resolution order: a string-kinded
// key uses its string value even when the type implements TextMarshaler.
func jsonMapKey(k reflect.Value) (string, bool) {
	if k.Kind() == reflect.String {
		return k.String(), true
	}
	if tm, ok := k.Interface().(encoding.TextMarshaler); ok {
		if k.Kind() == reflect.Pointer && k.IsNil() {
			return "", false
		}
		return invokeMarshalText(tm)
	}
	return jsonPrimitiveKey(k)
}

func invokeMarshalText(tm encoding.TextMarshaler) (string, bool) {
	b, err := tm.MarshalText()
	return string(b), err == nil
}

func jsonPrimitiveKey(k reflect.Value) (string, bool) {
	switch k.Kind() {
	case reflect.String:
		return k.String(), true
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return strconv.FormatInt(k.Int(), 10), true
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return strconv.FormatUint(k.Uint(), 10), true
	}
	return "", false
}
