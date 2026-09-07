package toon

import (
	"math"
	"reflect"
	"testing"
)

type scrubCycleNode struct {
	Value float64         `json:"value"`
	Next  *scrubCycleNode `json:"next"`
}

type scrubSharedNode struct {
	Value float64 `json:"value"`
}

func scrubCycleErrors() map[string]any {
	ptr := &scrubCycleNode{Value: math.NaN()}
	ptr.Next = ptr
	mapCycle := map[string]any{"a": math.NaN()}
	mapCycle["z"] = mapCycle
	sliceCycle := []any{math.NaN(), nil}
	sliceCycle[1] = sliceCycle
	return map[string]any{"pointer": ptr, "map": mapCycle, "slice": sliceCycle}
}

func TestFromAnyNonFiniteCyclesReturnErrors(t *testing.T) {
	for name, value := range scrubCycleErrors() {
		t.Run(name, func(t *testing.T) {
			if _, err := FromAny(value); err == nil {
				t.Fatal("FromAny returned nil error for cyclic value")
			}
		})
	}
}

func scrubSharedCases() map[string]struct {
	value any
	want  string
} {
	sharedMap := map[string]any{"value": math.NaN()}
	sharedSlice := []any{math.NaN()}
	sharedPointer := &scrubSharedNode{Value: math.NaN()}
	return map[string]struct {
		value any
		want  string
	}{
		"map":     {map[string]any{"left": sharedMap, "right": sharedMap}, `{"left":{"value":null},"right":{"value":null}}`},
		"slice":   {[]any{sharedSlice, sharedSlice}, `[[null],[null]]`},
		"pointer": {[]*scrubSharedNode{sharedPointer, sharedPointer}, `[{"value":null},{"value":null}]`},
	}
}

func assertSharedValue(t *testing.T, value any, wantRaw string) {
	t.Helper()
	got, err := FromAny(value)
	if err != nil {
		t.Fatalf("FromAny returned error for shared acyclic value: %v", err)
	}
	want, err := FromJSON([]byte(wantRaw))
	if err != nil {
		t.Fatalf("FromJSON expected value: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("FromAny = %#v, want %#v", got, want)
	}
}

func TestFromAnySharedAcyclicNonFiniteReferencesSucceed(t *testing.T) {
	for name, tc := range scrubSharedCases() {
		t.Run(name, func(t *testing.T) {
			assertSharedValue(t, tc.value, tc.want)
		})
	}
}
