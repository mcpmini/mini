package toon

import (
	"encoding"
	"encoding/json"
	"errors"
	"reflect"
	"strconv"
)

// FromAny converts an arbitrary Go value to a Value via json.Marshal then
// FromJSON, inheriting FromJSON's ordering and canonicalization. Spec §3
// requires NaN and +/-Infinity to normalize to null rather than fail the
// encode. The fallback strictly delegates clean subtrees to encoding/json:
//  1. Exact: json.Marshal succeeds.
//  2. Surgical (scrubNonFinite): replaces non-finite floats while preserving
//     encoding/json semantics for all clean subtrees.
//
// Non-non-finite errors (chan, func, unsupported map key types) surface from
// whichever tier first encounters them.
func FromAny(v any) (Value, error) {
	raw, err := json.Marshal(v)
	if err == nil {
		return FromJSON(raw)
	}
	if !isNonFiniteFloatError(err) {
		return Value{}, err
	}
	normalized, err := scrubNonFinite(v)
	if err != nil {
		return Value{}, err
	}
	raw, err = json.Marshal(normalized)
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
var textMarshalerType = reflect.TypeFor[encoding.TextMarshaler]()

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
