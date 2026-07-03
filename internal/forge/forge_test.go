//go:build test

package forge_test

import (
	"context"
	"encoding/json"
	"strconv"
	"testing"

	"github.com/mcpmini/mini/internal/forge"
)

func TestExecute_pureComputation(t *testing.T) {
	requireDeno(t)
	cases := []struct {
		name  string
		code  string
		input string
		want  string
	}{
		{"object", "async (input) => ({sum: input.a + input.b})", `{"a":2,"b":3}`, `{"sum":5}`},
		{"array", "async (input) => input.filter((x) => x % 2 === 0)", `[1,2,3,4,5,6]`, `[2,4,6]`},
		{"string", "async (input) => input.name.toUpperCase()", `{"name":"abc"}`, `"ABC"`},
		{"number", "async (input) => input.n * 2", `{"n":21}`, `42`},
		{"boolean", "async (input) => input.n > 0", `{"n":5}`, `true`},
		{"null", "async (input) => null", `{"n":5}`, `null`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := forge.Execute(context.Background(), forge.Params{
				Code:  tc.code,
				Input: json.RawMessage(tc.input),
			})
			if err != nil {
				t.Fatalf("Execute: %v", err)
			}
			assertJSONEqual(t, got, tc.want)
		})
	}
}

func TestExecute_asyncComputation(t *testing.T) {
	requireDeno(t)
	code := `async (input) => {
		const wait = (ms, val) => new Promise((resolve) => setTimeout(() => resolve(val), ms));
		const [a, b] = await Promise.all([wait(5, input.a), wait(10, input.b)]);
		return a + b;
	}`
	got, err := forge.Execute(context.Background(), forge.Params{
		Code:  code,
		Input: json.RawMessage(`{"a":3,"b":4}`),
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	assertJSONEqual(t, got, "7")
}

func TestExecute_missingReturnValueIsNull(t *testing.T) {
	requireDeno(t)
	got, err := forge.Execute(context.Background(), forge.Params{Code: "async () => {}"})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	assertJSONEqual(t, got, "null")
}

func TestExecute_syntaxError(t *testing.T) {
	requireDeno(t)
	_, err := forge.Execute(context.Background(), forge.Params{
		Code: "async (input) => { this is not valid js !!!",
	})
	fe := asForgeError(t, err)
	if fe.Kind != forge.KindSyntax {
		t.Errorf("Kind = %q, want %q", fe.Kind, forge.KindSyntax)
	}
	if !containsAny(fe.Message, "SyntaxError", "Expected") {
		t.Errorf("Message = %q, want a useful syntax diagnostic", fe.Message)
	}
}

func TestExecute_runtimeThrow(t *testing.T) {
	requireDeno(t)
	_, err := forge.Execute(context.Background(), forge.Params{
		Code: `async () => { throw new Error("boom"); }`,
	})
	fe := asForgeError(t, err)
	if fe.Kind != forge.KindRuntime {
		t.Errorf("Kind = %q, want %q", fe.Kind, forge.KindRuntime)
	}
	if !containsAny(fe.Message, "boom") {
		t.Errorf("Message = %q, want it to include the thrown message", fe.Message)
	}
	if !containsAny(fe.Message, "code:1:") {
		t.Errorf("Message = %q, want a stack location matching the user's source line numbers", fe.Message)
	}
}

func TestExecute_notSerializable(t *testing.T) {
	requireDeno(t)
	cases := []struct {
		name string
		code string
	}{
		{"cyclicObject", `async () => { const o = {}; o.self = o; return o; }`},
		{"function", `async () => { return () => 1; }`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := forge.Execute(context.Background(), forge.Params{Code: tc.code})
			fe := asForgeError(t, err)
			if fe.Kind != forge.KindNotSerializable {
				t.Errorf("Kind = %q, want %q", fe.Kind, forge.KindNotSerializable)
			}
		})
	}
}

func TestExecute_consoleNoiseDoesNotCorruptResultAndSurfacesInErrorConsole(t *testing.T) {
	requireDeno(t)
	code := `async () => {
		console.log("diagnostic line 1");
		console.log("diagnostic line 2");
		throw new Error("boom");
	}`
	_, err := forge.Execute(context.Background(), forge.Params{Code: code})
	fe := asForgeError(t, err)
	if fe.Kind != forge.KindRuntime {
		t.Fatalf("Kind = %q, want %q", fe.Kind, forge.KindRuntime)
	}
	if !containsAny(fe.Console, "diagnostic line 1", "diagnostic line 2") {
		t.Errorf("Console = %q, want it to include the console.log noise", fe.Console)
	}
}

func TestExecute_fakeMarkerInOutputCannotSpoofResult(t *testing.T) {
	requireDeno(t)
	code := `async (input) => { console.log(input.fake); return 99; }`
	input := json.RawMessage(`{"fake":"\nffffffffffffffff{\"ok\":\"hacked\"}"}`)
	got, err := forge.Execute(context.Background(), forge.Params{Code: code, Input: input})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	assertJSONEqual(t, got, "99")
}

func TestExecute_invalidInputJSONFailsWithoutSpawning(t *testing.T) {
	_, err := forge.Execute(context.Background(), forge.Params{
		Code:  "async (input) => input",
		Input: json.RawMessage(`{not valid json`),
	})
	fe := asForgeError(t, err)
	if fe.Kind != forge.KindRunner {
		t.Errorf("Kind = %q, want %q", fe.Kind, forge.KindRunner)
	}
}

func TestExecute_isolationBetweenRuns(t *testing.T) {
	requireDeno(t)
	_, err := forge.Execute(context.Background(), forge.Params{
		Code: `async () => { (globalThis).leak = 1; return "ok"; }`,
	})
	if err != nil {
		t.Fatalf("first Execute: %v", err)
	}
	got, err := forge.Execute(context.Background(), forge.Params{
		Code: `async () => (globalThis).leak ?? null`,
	})
	if err != nil {
		t.Fatalf("second Execute: %v", err)
	}
	assertJSONEqual(t, got, "null")
}

func TestExecute_concurrentCallsGetDistinctResults(t *testing.T) {
	requireDeno(t)
	const n = 8
	results := make([]json.RawMessage, n)
	errs := make([]error, n)
	done := make(chan int, n)
	for i := 0; i < n; i++ {
		go func(i int) {
			results[i], errs[i] = forge.Execute(context.Background(), forge.Params{
				Code:  "async (input) => input.n * input.n",
				Input: json.RawMessage(`{"n":` + strconv.Itoa(i) + `}`),
			})
			done <- i
		}(i)
	}
	for i := 0; i < n; i++ {
		<-done
	}
	for i := 0; i < n; i++ {
		if errs[i] != nil {
			t.Fatalf("Execute[%d]: %v", i, errs[i])
		}
		assertJSONEqual(t, results[i], strconv.Itoa(i*i))
	}
}
