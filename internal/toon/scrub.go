package toon

import (
	"math"
	"reflect"
)

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
	return scrubValue(rv.Elem())
}

func scrubSequence(rv reflect.Value) any {
	if rv.Kind() == reflect.Slice && rv.IsNil() {
		return nil
	}
	out := make([]any, rv.Len())
	for i := range out {
		out[i] = scrubValue(rv.Index(i))
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
		k, ok := jsonMapKey(iter.Key())
		if !ok {
			return rv.Interface()
		}
		out[k] = scrubValue(iter.Value())
	}
	return out
}

func scrubStruct(rv reflect.Value) any {
	fields := scrubFields(rv.Type())
	out := make(map[string]any, len(fields))
	for _, f := range fields {
		fv, ok := fieldByIndex(rv, f.index)
		if !ok || f.omitEmpty && isJSONEmptyValue(fv) {
			continue
		}
		out[f.name] = scrubValue(fv)
	}
	return out
}

func isJSONMarshaler(t reflect.Type) bool {
	return t.Implements(jsonMarshalerType) || reflect.PointerTo(t).Implements(jsonMarshalerType)
}
