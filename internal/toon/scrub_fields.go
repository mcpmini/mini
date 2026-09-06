package toon

import (
	"reflect"
	"sort"
	"strings"
	"unicode"
)

type scrubField struct {
	name      string
	index     []int
	omitEmpty bool
	omitZero  bool
	quoted    bool
	tagged    bool
}

func scrubFields(t reflect.Type) []scrubField {
	current := []structFieldType{{typ: t}}
	var fields []scrubField
	visited := make(map[reflect.Type]bool)
	for len(current) > 0 {
		next := make([]structFieldType, 0)
		counts := countFieldTypes(current)
		for _, f := range current {
			if visited[f.typ] {
				continue
			}
			visited[f.typ] = true
			exploreScrubFields(f, counts, &next, &fields)
		}
		current = next
	}
	return resolveScrubFields(fields)
}

type structFieldType struct {
	typ   reflect.Type
	index []int
}

func countFieldTypes(fields []structFieldType) map[reflect.Type]int {
	counts := make(map[reflect.Type]int)
	for _, f := range fields {
		counts[f.typ]++
	}
	return counts
}

func exploreScrubFields(parent structFieldType, counts map[reflect.Type]int, next *[]structFieldType, fields *[]scrubField) {
	for i := 0; i < parent.typ.NumField(); i++ {
		sf := parent.typ.Field(i)
		if !includeScrubField(sf) {
			continue
		}
		name, opts := scrubTag(sf)
		index := append(append([]int(nil), parent.index...), i)
		ft := scrubFieldType(sf.Type)
		if name != "" || !sf.Anonymous || ft.Kind() != reflect.Struct {
			if name == "" {
				name = sf.Name
			}
			field := scrubField{
				name: name, index: index, omitEmpty: opts.omitEmpty,
				omitZero: opts.omitZero, quoted: opts.quoted, tagged: opts.tagged,
			}
			*fields = appendScrubField(*fields, field, counts[parent.typ] > 1)
			continue
		}
		*next = append(*next, structFieldType{typ: ft, index: index})
	}
}

func scrubFieldType(t reflect.Type) reflect.Type {
	if t.Name() == "" && t.Kind() == reflect.Pointer {
		return t.Elem()
	}
	return t
}

func appendScrubField(fields []scrubField, field scrubField, duplicate bool) []scrubField {
	fields = append(fields, field)
	if duplicate {
		fields = append(fields, field)
	}
	return fields
}

type scrubTagOptions struct {
	omitEmpty bool
	omitZero  bool
	quoted    bool
	tagged    bool
}

func includeScrubField(sf reflect.StructField) bool {
	if sf.Anonymous {
		ft := sf.Type
		if ft.Kind() == reflect.Pointer {
			ft = ft.Elem()
		}
		if !sf.IsExported() && ft.Kind() != reflect.Struct {
			return false
		}
	} else if !sf.IsExported() {
		return false
	}
	return sf.Tag.Get("json") != "-"
}

func scrubTag(sf reflect.StructField) (string, scrubTagOptions) {
	tag := sf.Tag.Get("json")
	name, opts := tag, ""
	if idx := strings.IndexByte(tag, ','); idx >= 0 {
		name, opts = tag[:idx], tag[idx+1:]
	}
	if !validJSONTag(name) {
		name = ""
	}
	return name, scrubTagOptions{
		omitEmpty: hasJSONOption(opts, "omitempty"),
		omitZero:  hasJSONOption(opts, "omitzero"),
		quoted:    quotedJSONField(sf.Type, opts),
		tagged:    name != "",
	}
}

func quotedJSONField(t reflect.Type, options string) bool {
	if !hasJSONOption(options, "string") {
		return false
	}
	if t.Name() == "" && t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	switch t.Kind() {
	case reflect.Bool, reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr,
		reflect.Float32, reflect.Float64, reflect.String:
		return true
	default:
		return false
	}
}

func hasJSONOption(options, want string) bool {
	for _, option := range strings.Split(options, ",") {
		if option == want {
			return true
		}
	}
	return false
}

func validJSONTag(name string) bool {
	if name == "" {
		return false
	}
	for _, r := range name {
		if strings.ContainsRune("!#$%&()*+-./:;<=>?@[]^_{|}~ ", r) {
			continue
		}
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) {
			return false
		}
	}
	return true
}

func resolveScrubFields(fields []scrubField) []scrubField {
	sort.SliceStable(fields, func(i, j int) bool {
		if fields[i].name != fields[j].name {
			return fields[i].name < fields[j].name
		}
		if len(fields[i].index) != len(fields[j].index) {
			return len(fields[i].index) < len(fields[j].index)
		}
		if fields[i].tagged != fields[j].tagged {
			return fields[i].tagged
		}
		return compareIndices(fields[i].index, fields[j].index) < 0
	})
	out := fields[:0]
	for i := 0; i < len(fields); {
		j := i + 1
		for j < len(fields) && fields[j].name == fields[i].name {
			j++
		}
		if j-i == 1 || dominantScrubField(fields[i:j]) {
			out = append(out, fields[i])
		}
		i = j
	}
	sort.SliceStable(out, func(i, j int) bool {
		return compareIndices(out[i].index, out[j].index) < 0
	})
	return out
}

func dominantScrubField(fields []scrubField) bool {
	return len(fields) < 2 || len(fields[0].index) != len(fields[1].index) || fields[0].tagged != fields[1].tagged
}

func compareIndices(a, b []int) int {
	for i := 0; i < len(a) && i < len(b); i++ {
		if a[i] != b[i] {
			if a[i] < b[i] {
				return -1
			}
			return 1
		}
	}
	if len(a) < len(b) {
		return -1
	}
	if len(a) > len(b) {
		return 1
	}
	return 0
}

func fieldByIndex(rv reflect.Value, index []int) (reflect.Value, bool) {
	for _, i := range index {
		for rv.Kind() == reflect.Pointer {
			if rv.IsNil() {
				return reflect.Value{}, false
			}
			rv = rv.Elem()
		}
		if rv.Kind() != reflect.Struct {
			return reflect.Value{}, false
		}
		rv = rv.Field(i)
	}
	return rv, rv.IsValid()
}

func isJSONEmptyValue(rv reflect.Value) bool {
	switch rv.Kind() {
	case reflect.Array, reflect.Map, reflect.Slice, reflect.String:
		return rv.Len() == 0
	case reflect.Bool, reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr,
		reflect.Float32, reflect.Float64, reflect.Interface, reflect.Pointer:
		return rv.IsZero()
	default:
		return false
	}
}

type jsonZeroer interface {
	IsZero() bool
}

var jsonZeroerType = reflect.TypeFor[jsonZeroer]()

func isJSONZeroValue(rv reflect.Value) bool {
	if !rv.IsValid() {
		return true
	}
	t := rv.Type()
	switch {
	case t.Kind() == reflect.Interface && t.Implements(jsonZeroerType):
		return rv.IsNil() || callJSONZero(rv)
	case t.Kind() == reflect.Pointer && t.Implements(jsonZeroerType):
		return rv.IsNil() || callJSONZero(rv)
	case t.Implements(jsonZeroerType):
		return callJSONZero(rv)
	case reflect.PointerTo(t).Implements(jsonZeroerType):
		if !rv.CanAddr() {
			copy := reflect.New(t).Elem()
			copy.Set(rv)
			rv = copy
		}
		return callJSONZero(rv.Addr())
	default:
		return rv.IsZero()
	}
}

func callJSONZero(rv reflect.Value) bool {
	zeroer, ok := rv.Interface().(jsonZeroer)
	return ok && zeroer.IsZero()
}
