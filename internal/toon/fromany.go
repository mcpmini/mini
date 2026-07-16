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

// normalizeNonFinite never mutates rv. Types implementing json.Marshaler are
// returned unwalked so the retry marshal invokes them directly. For unexported
// embedded struct values (CanInterface=false) the Marshaler path is skipped;
// those values are walked as plain structs so their fields can be normalized.
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

// jsonField carries everything needed to emit one struct field into the output
// map. orig holds the original interface value when accessible; when orig is
// nil (unexported embedded) the normalized path is used unconditionally.
type jsonField struct {
	key      string
	index    []int            // sf.Index — used for depth-based precedence
	isTagged bool             // sf has a non-empty json struct tag
	orig     any              // original value via Interface(); nil if !CanInterface
	origTag  reflect.StructTag // explicit-name tag, options intact — for orig path
	norm     any              // value after normalizeNonFinite
	normTag  reflect.StructTag // explicit-name tag, opts stripped for non-finite
}

func normalizeStruct(rv reflect.Value) any {
	winners := resolveFieldWinners(visibleJSONFields(rv))
	out := make(map[string]any, len(winners))
	for _, jf := range winners {
		emitViaSynth(jf, out)
	}
	return out
}

func visibleJSONFields(rv reflect.Value) []jsonField {
	taggedAnon := taggedAnonIndices(rv.Type())
	var fields []jsonField
	for _, sf := range reflect.VisibleFields(rv.Type()) {
		if shouldSkipField(sf, taggedAnon) {
			continue
		}
		fv, err := rv.FieldByIndexErr(sf.Index)
		if err != nil {
			continue
		}
		fields = append(fields, makeJSONField(sf, fv))
	}
	return fields
}

func makeJSONField(sf reflect.StructField, fv reflect.Value) jsonField {
	nonFinite := isNonFiniteField(fv)
	jf := jsonField{
		key:      jsonNameFromField(sf),
		index:    sf.Index,
		isTagged: sf.Tag.Get("json") != "",
		norm:     normalizeNonFinite(fv),
		normTag:  synthFieldTag(sf, nonFinite),
		origTag:  synthFieldTag(sf, false),
	}
	if fv.CanInterface() {
		jf.orig = fv.Interface()
	}
	return jf
}

// resolveFieldWinners applies encoding/json's field precedence rules: shallower
// depth wins; among ties, exactly one tagged field wins; otherwise the key is
// dropped. Insertion order from fields is preserved for the winners.
func resolveFieldWinners(fields []jsonField) []jsonField {
	byKey := make(map[string][]jsonField, len(fields))
	for _, f := range fields {
		byKey[f.key] = append(byKey[f.key], f)
	}
	seen := make(map[string]bool, len(byKey))
	var winners []jsonField
	for _, f := range fields {
		if seen[f.key] {
			continue
		}
		seen[f.key] = true
		if w := pickWinner(byKey[f.key]); w != nil {
			winners = append(winners, *w)
		}
	}
	return winners
}

func pickWinner(candidates []jsonField) *jsonField {
	if len(candidates) == 1 {
		return &candidates[0]
	}
	atMin := shallowestAmong(candidates)
	if len(atMin) == 1 {
		return &atMin[0]
	}
	var tagged []jsonField
	for _, c := range atMin {
		if c.isTagged {
			tagged = append(tagged, c)
		}
	}
	if len(tagged) == 1 {
		return &tagged[0]
	}
	return nil
}

func shallowestAmong(candidates []jsonField) []jsonField {
	min := len(candidates[0].index)
	for _, c := range candidates[1:] {
		if len(c.index) < min {
			min = len(c.index)
		}
	}
	var out []jsonField
	for _, c := range candidates {
		if len(c.index) == min {
			out = append(out, c)
		}
	}
	return out
}

// taggedAnonIndices returns the Index paths of all anonymous struct fields
// (exported or unexported) that carry a non-skip json tag. encoding/json
// treats both as named objects; their promoted descendants must not be emitted
// independently.
func taggedAnonIndices(t reflect.Type) [][]int {
	var result [][]int
	for _, sf := range reflect.VisibleFields(t) {
		if !sf.Anonymous {
			continue
		}
		tag, hasTag := sf.Tag.Lookup("json")
		if hasTag && tag != "-" {
			result = append(result, sf.Index)
		}
	}
	return result
}

func shouldSkipField(sf reflect.StructField, taggedAnon [][]int) bool {
	tag, hasTag := sf.Tag.Lookup("json")
	if hasTag && tag == "-" {
		return true
	}
	if sf.Anonymous {
		// Untagged anonymous: skip the container; promoted fields appear separately.
		// Tagged anonymous (exported or unexported): include as named field.
		return !hasTag
	}
	if !sf.IsExported() {
		return true
	}
	// Descendants of a tagged anonymous field are not independently promoted;
	// encoding/json nests them under the named embedded field.
	for _, idx := range taggedAnon {
		if isIndexDescendant(sf.Index, idx) {
			return true
		}
	}
	return false
}

func isIndexDescendant(child, parent []int) bool {
	if len(child) <= len(parent) {
		return false
	}
	for i, p := range parent {
		if child[i] != p {
			return false
		}
	}
	return true
}

func jsonNameFromField(sf reflect.StructField) string {
	tag, ok := sf.Tag.Lookup("json")
	if !ok {
		return sf.Name
	}
	name, _, _ := strings.Cut(tag, ",")
	if name == "" {
		return sf.Name
	}
	return name
}

// isNonFiniteField follows pointer/interface indirection: a non-nil pointer or
// interface is never "empty" for encoding/json's omitempty, so a NaN behind one
// needs the stripped-options tag to emit an explicit null instead of being
// omitted once normalization turns it into nil.
func isNonFiniteField(fv reflect.Value) bool {
	for fv.Kind() == reflect.Ptr || fv.Kind() == reflect.Interface {
		if fv.IsNil() {
			return false
		}
		fv = fv.Elem()
	}
	k := fv.Kind()
	if k != reflect.Float32 && k != reflect.Float64 {
		return false
	}
	f := fv.Float()
	return math.IsNaN(f) || math.IsInf(f, 0)
}

func synthFieldTag(sf reflect.StructField, nonFinite bool) reflect.StructTag {
	name, opts := sf.Name, ""
	if tag, ok := sf.Tag.Lookup("json"); ok {
		n, o, _ := strings.Cut(tag, ",")
		if n != "" {
			name = n
		}
		opts = o
	}
	if nonFinite {
		// normalizeNonFinite maps NaN/Inf to nil (untyped any), which is the
		// zero value for any encoding/json omitempty check. Strip options so
		// encoding/json emits explicit null rather than omitting the field.
		opts = ""
	}
	if opts != "" {
		return reflect.StructTag(fmt.Sprintf(`json:"%s,%s"`, name, opts))
	}
	return reflect.StructTag(fmt.Sprintf(`json:"%s"`, name))
}

// emitViaSynth tries encoding/json with the original value first so omitempty
// and all other options are applied with perfect fidelity. Only when the
// original marshal fails with a non-finite-float error does it fall back to
// the normalized-value path, where omitempty is stripped because NaN/Inf
// normalized to nil must still emit as explicit null, not be omitted.
func emitViaSynth(jf jsonField, out map[string]any) {
	if jf.orig != nil {
		data, err := marshalSynth(jf.orig, jf.origTag)
		if err == nil {
			mergeJSON(data, out)
			return
		}
		if !isNonFiniteFloatError(err) {
			// Unsupported type (chan, func, …): keep the raw value so the outer
			// json.Marshal in FromAny surfaces the error.
			name, _, _ := strings.Cut(jf.origTag.Get("json"), ",")
			out[name] = jf.orig
			return
		}
	}
	mergeNormalized(jf, out)
}

func mergeNormalized(jf jsonField, out map[string]any) {
	data, err := marshalSynth(jf.norm, jf.normTag)
	if err != nil {
		name, _, _ := strings.Cut(jf.normTag.Get("json"), ",")
		out[name] = jf.norm
		return
	}
	mergeJSON(data, out)
}

func mergeJSON(data []byte, out map[string]any) {
	var partial map[string]any
	if json.Unmarshal(data, &partial) == nil {
		for k, v := range partial {
			out[k] = v
		}
	}
}

func marshalSynth(normalized any, tag reflect.StructTag) ([]byte, error) {
	ft := reflect.TypeOf((*struct{})(nil))
	if normalized != nil {
		ft = reflect.TypeOf(normalized)
	}
	sv := reflect.New(reflect.StructOf([]reflect.StructField{
		{Name: "F", Type: ft, Tag: tag},
	})).Elem()
	if normalized != nil {
		sv.Field(0).Set(reflect.ValueOf(normalized))
	}
	return json.Marshal(sv.Interface())
}
