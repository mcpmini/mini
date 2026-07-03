//go:build test

package forge_test

import (
	"context"
	"testing"

	"github.com/mcpmini/mini/internal/forge"
)

func TestExecute_sandboxDeniesHostAccess(t *testing.T) {
	requireDeno(t)
	cases := []struct {
		name string
		code string
	}{
		{"readFile", `async () => { await Deno.readTextFile("/etc/hosts"); }`},
		{"envGet", `async () => { Deno.env.get("HOME"); }`},
		{"fetch", `async () => { await fetch("http://localhost:1"); }`},
		{"subprocess", `async () => { await new Deno.Command("ls").output(); }`},
		{"ffi", `async () => { Deno.dlopen("/usr/lib/libc.dylib", {}); }`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := forge.Execute(context.Background(), forge.Params{Code: tc.code})
			fe := asForgeError(t, err)
			if fe.Kind != forge.KindRuntime {
				t.Errorf("Kind = %q, want %q", fe.Kind, forge.KindRuntime)
			}
			if fe.Message == "" {
				t.Error("Message is empty, want an informative denial message")
			}
		})
	}
}
