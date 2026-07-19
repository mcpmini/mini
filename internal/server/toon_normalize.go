package server

import (
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"strings"

	"github.com/mcpmini/mini/internal/response"
)

var jsonMarshalerTypeNF = reflect.TypeFor[json.Marshaler]()

// normalizeEnvelopeNonFinite returns a copy of env with non-finite
// float32/float64 values in Data and Passthrough replaced by nil, recursively
// through maps, slices, arrays, pointers, interfaces, and exported struct
// fields — without calling MarshalJSON. Types implementing json.Marshaler are
// left as-is; if their MarshalJSON independently produces a non-finite float,
// the EncodeToon fallback path handles it. Structs are rebuilt as generic maps
// (omitempty and embedded-field flattening are lost), so callers must invoke
// this only after plain encoding has already failed.
func normalizeEnvelopeNonFinite(env *response.Envelope) *response.Envelope {
	cp := *env
	cp.Data = normalizeAnyNF(env.Data)
	cp.Passthrough = normalizeMapNF(env.Passthrough)
	return &cp
}

func normalizeMapNF(m map[string]any) map[string]any {
	if m == nil {
		return nil
	}
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = normalizeAnyNF(v)
	}
	return out
}

func normalizeSliceAnyNF(s []any) []any {
	out := make([]any, len(s))
	for i, elem := range s {
		out[i] = normalizeAnyNF(elem)
	}
	return out
}

func normalizeAnyNF(v any) any {
	switch val := v.(type) {
	case nil:
		return nil
	case map[string]any:
		return normalizeMapNF(val)
	case []any:
		return normalizeSliceAnyNF(val)
	}
	return normalizeRV(reflect.ValueOf(v))
}

func normalizeRVPre(rv reflect.Value) (any, bool) {
	if !rv.IsValid() {
		return nil, true
	}
	if rv.CanInterface() && isJSONMarshaler(rv.Type()) {
		return rv.Interface(), true
	}
	return nil, false
}

func normalizeRV(rv reflect.Value) any {
	if pre, ok := normalizeRVPre(rv); ok {
		return pre
	}
	switch rv.Kind() {
	case reflect.Float32, reflect.Float64:
		return normalizeFloatRV(rv)
	case reflect.Ptr, reflect.Interface:
		return normalizePtrRV(rv)
	case reflect.Slice, reflect.Array:
		return normalizeSliceOrArray(rv)
	case reflect.Map:
		return normalizeGenMapRV(rv)
	case reflect.Struct:
		return normalizeStructRV(rv)
	}
	if rv.CanInterface() {
		return rv.Interface()
	}
	return nil
}

func normalizeFloatRV(rv reflect.Value) any {
	f := rv.Float()
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return nil
	}
	return rv.Interface()
}

func normalizePtrRV(rv reflect.Value) any {
	if rv.IsNil() {
		return nil
	}
	return normalizeAnyNF(rv.Elem().Interface())
}

func normalizeSliceOrArray(rv reflect.Value) any {
	if rv.Kind() == reflect.Slice && rv.IsNil() {
		return nil
	}
	return normalizeSliceRV(rv)
}

func normalizeSliceRV(rv reflect.Value) any {
	out := make([]any, rv.Len())
	for i := range out {
		out[i] = normalizeAnyNF(rv.Index(i).Interface())
	}
	return out
}

func normalizeGenMapRV(rv reflect.Value) any {
	if rv.IsNil() {
		return nil
	}
	out := make(map[string]any, rv.Len())
	iter := rv.MapRange()
	for iter.Next() {
		k := iter.Key()
		var key string
		if k.Kind() == reflect.String {
			key = k.String()
		} else {
			key = fmt.Sprint(k.Interface())
		}
		out[key] = normalizeAnyNF(iter.Value().Interface())
	}
	return out
}

func normalizeStructRV(rv reflect.Value) any {
	t := rv.Type()
	out := make(map[string]any, t.NumField())
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if !f.IsExported() {
			continue
		}
		name := jsonTagName(f)
		if name == "-" {
			continue
		}
		out[name] = normalizeAnyNF(rv.Field(i).Interface())
	}
	return out
}

func jsonTagName(f reflect.StructField) string {
	tag := f.Tag.Get("json")
	if tag == "" {
		return f.Name
	}
	if idx := strings.Index(tag, ","); idx >= 0 {
		tag = tag[:idx]
	}
	if tag == "" {
		return f.Name
	}
	return tag
}

func isJSONMarshaler(t reflect.Type) bool {
	return t.Implements(jsonMarshalerTypeNF) || reflect.PointerTo(t).Implements(jsonMarshalerTypeNF)
}
