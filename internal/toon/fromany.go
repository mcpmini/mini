package toon

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"reflect"
)

// FromAny converts an arbitrary Go value to a Value via json.Marshal then
// FromJSON, inheriting FromJSON's ordering and canonicalization. Spec §3
// requires NaN and +/-Infinity to normalize to null rather than fail the
// encode, so a non-finite-float marshal error triggers one retry against a
// null-substituted copy of v. The rescue applies to JSON-shaped data — bare
// floats, pointers, interfaces, maps, and slices. Structs are not walked; a
// non-finite float inside a struct surfaces encoding/json's UnsupportedValueError.
// Every other unsupported value (chan, func, non-string map keys) still fails
// as before.
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

// normalizeNonFinite never mutates rv. Types implementing json.Marshaler are
// returned unwalked so the retry marshal invokes them directly.
func normalizeNonFinite(rv reflect.Value) any {
	if !rv.IsValid() {
		return nil
	}
	if rv.CanInterface() && rv.Type().Implements(jsonMarshalerType) {
		return rv.Interface()
	}
	switch rv.Kind() {
	case reflect.Float32, reflect.Float64:
		return normalizeFloat(rv.Float())
	case reflect.Ptr, reflect.Interface:
		return normalizeIndirect(rv)
	case reflect.Map:
		return normalizeMap(rv)
	case reflect.Slice, reflect.Array:
		return normalizeSlice(rv)
	default:
		return rv.Interface()
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

func normalizeMap(rv reflect.Value) any {
	out := make(map[string]any, rv.Len())
	iter := rv.MapRange()
	for iter.Next() {
		out[mapKeyString(iter.Key())] = normalizeNonFinite(iter.Value())
	}
	return out
}

func mapKeyString(k reflect.Value) string {
	if k.Kind() == reflect.String {
		return k.String()
	}
	return fmt.Sprint(k.Interface())
}

// normalizeSlice leaves []byte untouched since encoding/json base64-encodes
// it as a scalar string rather than an array of numbers.
func normalizeSlice(rv reflect.Value) any {
	if rv.Kind() == reflect.Slice {
		if rv.IsNil() {
			return nil
		}
		if rv.Type().Elem().Kind() == reflect.Uint8 {
			return rv.Interface()
		}
	}
	out := make([]any, rv.Len())
	for i := range out {
		out[i] = normalizeNonFinite(rv.Index(i))
	}
	return out
}
