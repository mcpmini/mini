package server

import (
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"strings"

	"github.com/mcpmini/mini/internal/response"
)

var jsonMarshalerType = reflect.TypeFor[json.Marshaler]()

// normalizeEnvelopeNonFinite replaces non-finite float32/64 values in Data and
// Passthrough with nil, recursively through containers and exported struct
// fields, without invoking MarshalJSON (json.Marshaler values are left as-is).
// Structs are rebuilt as generic maps, losing omitempty/embedding semantics —
// callers must invoke this only after plain encoding has already failed.
func normalizeEnvelopeNonFinite(env *response.Envelope) *response.Envelope {
	cp := *env
	cp.Data = scrubNonFinite(env.Data)
	cp.Passthrough = scrubStringMap(env.Passthrough)
	return &cp
}

func scrubStringMap(m map[string]any) map[string]any {
	if m == nil {
		return nil
	}
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = scrubNonFinite(v)
	}
	return out
}

func scrubAnySlice(s []any) []any {
	out := make([]any, len(s))
	for i, elem := range s {
		out[i] = scrubNonFinite(elem)
	}
	return out
}

func scrubNonFinite(v any) any {
	switch val := v.(type) {
	case nil:
		return nil
	case map[string]any:
		return scrubStringMap(val)
	case []any:
		return scrubAnySlice(val)
	}
	return scrubValue(reflect.ValueOf(v))
}

func passthroughForMarshaler(rv reflect.Value) (any, bool) {
	if !rv.IsValid() {
		return nil, true
	}
	if rv.CanInterface() && isJSONMarshaler(rv.Type()) {
		return rv.Interface(), true
	}
	return nil, false
}

func scrubValue(rv reflect.Value) any {
	if pre, ok := passthroughForMarshaler(rv); ok {
		return pre
	}
	switch rv.Kind() {
	case reflect.Float32, reflect.Float64:
		return scrubFloat(rv)
	case reflect.Ptr, reflect.Interface:
		return scrubPointer(rv)
	case reflect.Slice, reflect.Array:
		return scrubSequence(rv)
	case reflect.Map:
		return scrubGenericMap(rv)
	case reflect.Struct:
		return scrubStruct(rv)
	}
	if rv.CanInterface() {
		return rv.Interface()
	}
	return nil
}

func scrubFloat(rv reflect.Value) any {
	f := rv.Float()
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return nil
	}
	return rv.Interface()
}

func scrubPointer(rv reflect.Value) any {
	if rv.IsNil() {
		return nil
	}
	return scrubNonFinite(rv.Elem().Interface())
}

func scrubSequence(rv reflect.Value) any {
	if rv.Kind() == reflect.Slice && rv.IsNil() {
		return nil
	}
	out := make([]any, rv.Len())
	for i := range out {
		out[i] = scrubNonFinite(rv.Index(i).Interface())
	}
	return out
}

func scrubGenericMap(rv reflect.Value) any {
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
		out[key] = scrubNonFinite(iter.Value().Interface())
	}
	return out
}

func scrubStruct(rv reflect.Value) any {
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
		out[name] = scrubNonFinite(rv.Field(i).Interface())
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
	return t.Implements(jsonMarshalerType) || reflect.PointerTo(t).Implements(jsonMarshalerType)
}
