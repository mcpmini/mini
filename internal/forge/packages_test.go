//go:build test

package forge_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/mcpmini/mini/internal/forge"
)

func TestExecute_importsCachedJSRPackage(t *testing.T) {
	requireCached(t, "jsr:@std/csv@1")
	code := `async () => {
		const { parse } = await import("jsr:@std/csv@1");
		return parse("a,b\n1,2", { skipFirstRow: true });
	}`
	got, err := forge.Execute(context.Background(), forge.Params{
		Code:     code,
		Packages: []string{"jsr:@std/csv@1"},
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	assertJSONEqual(t, got, `[{"a":"1","b":"2"}]`)
}

func TestExecute_importsCachedNPMPackage(t *testing.T) {
	requireCached(t, "npm:zod@3")
	code := `async () => {
		const { z } = await import("npm:zod@3");
		const schema = z.object({ n: z.number() });
		return schema.safeParse({ n: 5 }).success;
	}`
	got, err := forge.Execute(context.Background(), forge.Params{
		Code:     code,
		Packages: []string{"npm:zod@3"},
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	assertJSONEqual(t, got, "true")
}

func TestExecute_packageValidationRejectsWithoutSpawningDeno(t *testing.T) {
	cases := []struct {
		name     string
		packages []string
	}{
		{"https", []string{"https://example.com/mod.ts"}},
		{"file", []string{"file:///etc/passwd"}},
		{"node", []string{"node:fs"}},
		{"shellMetacharacters", []string{"npm:zod; rm -rf /"}},
		{"tooMany", manyPackages(9)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := forge.Execute(context.Background(), forge.Params{
				Code:     "async () => 1",
				Packages: tc.packages,
			})
			fe := asForgeError(t, err)
			if fe.Kind != forge.KindRunner {
				t.Errorf("Kind = %q, want %q", fe.Kind, forge.KindRunner)
			}
		})
	}
}

func manyPackages(n int) []string {
	pkgs := make([]string, n)
	for i := range pkgs {
		pkgs[i] = fmt.Sprintf("npm:pkg%d", i)
	}
	return pkgs
}

func TestExecute_unresolvablePackageFailsAsDependencyError(t *testing.T) {
	requireDeno(t)
	denoDir := t.TempDir()
	_, err := forge.ExecuteWithEnv(context.Background(), forge.Params{
		Code:     "async () => 1",
		Packages: []string{"npm:@mini-forge-test/does-not-exist-xyz"},
	}, []string{"DENO_DIR=" + denoDir})
	fe := asForgeError(t, err)
	if fe.Kind != forge.KindDependency {
		t.Errorf("Kind = %q, want %q", fe.Kind, forge.KindDependency)
	}
}

func TestExecute_npmImportWithoutDeclaredPackagesFailsWithoutNetworkAccess(t *testing.T) {
	requireDeno(t)
	_, err := forge.Execute(context.Background(), forge.Params{
		Code: `async () => { await import("npm:is-even@1"); return null; }`,
	})
	fe := asForgeError(t, err)
	if fe.Kind != forge.KindRuntime {
		t.Fatalf("Kind = %q, want %q", fe.Kind, forge.KindRuntime)
	}
	if !containsAny(fe.Message, "packages parameter") {
		t.Errorf("Message = %q, want a hint about declaring packages", fe.Message)
	}
}

func TestExecute_unlistedPackageInvisibleEvenWhenCachedElsewhere(t *testing.T) {
	requireDeno(t)
	_, err := forge.Execute(context.Background(), forge.Params{
		Code:     `async () => { await import("npm:zod@3"); return null; }`,
		Packages: []string{"jsr:@std/csv@1"},
	})
	fe := asForgeError(t, err)
	if fe.Kind == forge.KindDependency {
		t.Skip("offline: could not resolve jsr:@std/csv@1 to set up the isolated cache")
	}
	if fe.Kind != forge.KindRuntime {
		t.Fatalf("Kind = %q, want %q", fe.Kind, forge.KindRuntime)
	}
	if !containsAny(fe.Message, "packages parameter", "--cached-only") {
		t.Errorf("Message = %q, want a hint about listing packages", fe.Message)
	}
}
