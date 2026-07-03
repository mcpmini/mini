//go:build test

package forge_test

import (
	"context"
	"strings"
	"testing"

	"github.com/mcpmini/mini/internal/forge"
)

var networkBoundaryCases = []struct {
	name    string
	code    string
	errWant string
}{
	{"fetchHTTPS", `async () => { await fetch("https://api.github.com/zen"); }`, "net access"},
	{"fetchHTTP", `async () => { await fetch("http://example.com/"); }`, "net access"},
	{"tcpConnect", `async () => { await Deno.connect({ hostname: "1.1.1.1", port: 443 }); }`, "net access"},
	{"inboundListen", `async () => { Deno.listen({ port: 0 }); }`, "net access"},
	{"dnsResolve", `async () => { await Deno.resolveDns("example.com", "A"); }`, "net access"},
	{"webSocket", `async () => { new WebSocket("wss://example.com"); }`, "net access"},
	// node-compat DNS denial surfaces as EPERM instead of NotCapable.
	{"nodeHTTPRequest", `async () => {
		const http = await import("node:http");
		await new Promise((_, rej) => { const r = http.request("http://example.com/", () => {}); r.on("error", rej); r.end(); });
	}`, "EPERM"},
	{"nodeNetConnect", `async () => {
		const net = await import("node:net");
		await new Promise((_, rej) => net.connect(443, "1.1.1.1").on("error", rej));
	}`, "net access"},
}

func assertNetworkDenied(t *testing.T, params forge.Params, errWant string) {
	t.Helper()
	_, err := forge.Execute(context.Background(), params)
	fe := asForgeError(t, err)
	if fe.Kind != forge.KindRuntime {
		t.Errorf("Kind = %q, want %q", fe.Kind, forge.KindRuntime)
	}
	if !strings.Contains(fe.Message, errWant) {
		t.Errorf("Message = %q, want it to contain %q", fe.Message, errWant)
	}
}

func TestExecute_networkBoundariesHardFail(t *testing.T) {
	requireDeno(t)
	for _, tc := range networkBoundaryCases {
		t.Run(tc.name, func(t *testing.T) {
			assertNetworkDenied(t, forge.Params{Code: tc.code}, tc.errWant)
		})
	}
}

// Non-empty Packages switches the run flags from --no-remote to --cached-only;
// the network denial must hold on that flag path too, not just the stage-1 one.
func TestExecute_networkBoundariesHardFailOnPackagePath(t *testing.T) {
	requireCached(t, "jsr:@std/csv@1")
	for _, tc := range networkBoundaryCases {
		t.Run(tc.name, func(t *testing.T) {
			assertNetworkDenied(t, forge.Params{Code: tc.code, Packages: []string{"jsr:@std/csv@1"}}, tc.errWant)
		})
	}
}

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
