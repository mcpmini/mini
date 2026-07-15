package toon

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"reflect"
	"strings"
)

// FromAny converts an arbitrary Go value to a Value via json.Marshal then
// FromJSON, so it inherits FromJSON's ordering and canonicalization and
// encoding/json's own rules for unsupported values (chan, func, non-string
// map keys) and its guarantee of sorted map keys. Spec §3 requires NaN and
// +/-Infinity to normalize to null rather than fail the encode, so a
// non-finite-float marshal error triggers one retry against a null-
// substituted copy of v; every other unsupported value still fails as before.
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

// normalizeNonFinite never mutates rv; it builds a fresh value tree with
// non-finite floats replaced by nil. Types implementing json.Marshaler are
// returned unwalked so the retry marshal invokes them directly, same as the
// fast path.
func normalizeNonFinite(rv reflect.Value) any {
	if !rv.IsValid() {
		return nil
	}
	if rv.Type().Implements(jsonMarshalerType) {
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
	case reflect.Struct:
		return normalizeStruct(rv)
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

func normalizeStruct(rv reflect.Value) any {
	t := rv.Type()
	out := make(map[string]any, t.NumField())
	for i := 0; i < t.NumField(); i++ {
		field := t.Field(i)
		if field.PkgPath != "" {
			continue
		}
		name, skip := jsonFieldName(field)
		if skip {
			continue
		}
		out[name] = normalizeNonFinite(rv.Field(i))
	}
	return out
}

func jsonFieldName(field reflect.StructField) (string, bool) {
	tag, ok := field.Tag.Lookup("json")
	if !ok {
		return field.Name, false
	}
	if tag == "-" {
		return "", true
	}
	name, _, _ := strings.Cut(tag, ",")
	if name == "" {
		name = field.Name
	}
	return name, false
}
