package toon

import (
	"math"
	"reflect"
	"strings"
)

// scrubNonFinite is the lossy tier of FromAny's three-tier chain. Structs are
// rebuilt as generic maps (embedded-field flattening is lost, see scrubStruct)
// and json.Marshaler values pass through untouched. FromAny invokes this only
// after the surgical rescue (normalizeNonFinite) failed.
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
		k, ok := jsonMapKey(iter.Key())
		if !ok {
			return rv.Interface()
		}
		out[k] = scrubNonFinite(iter.Value().Interface())
	}
	return out
}

// scrubStruct honors the json tag's name and omitempty option per field, but
// does not promote embedded struct fields the way encoding/json does — an
// embedded field is emitted under its own field name rather than flattened.
func scrubStruct(rv reflect.Value) any {
	t := rv.Type()
	out := make(map[string]any, t.NumField())
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if !f.IsExported() {
			continue
		}
		name, omit := jsonTagNameOpts(f)
		if name == "-" {
			continue
		}
		val := scrubNonFinite(rv.Field(i).Interface())
		if omit && isJSONZero(val) {
			continue
		}
		out[name] = val
	}
	return out
}

func isJSONZero(v any) bool {
	if v == nil {
		return true
	}
	return reflect.ValueOf(v).IsZero()
}

func jsonTagNameOpts(f reflect.StructField) (string, bool) {
	tag := f.Tag.Get("json")
	if tag == "" {
		return f.Name, false
	}
	name, opts := tag, ""
	if idx := strings.Index(tag, ","); idx >= 0 {
		name, opts = tag[:idx], tag[idx+1:]
	}
	if name == "" {
		name = f.Name
	}
	return name, strings.Contains(opts, "omitempty")
}

func isJSONMarshaler(t reflect.Type) bool {
	return t.Implements(jsonMarshalerType) || reflect.PointerTo(t).Implements(jsonMarshalerType)
}
