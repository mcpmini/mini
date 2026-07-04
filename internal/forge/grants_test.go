//go:build test

package forge_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mcpmini/mini/internal/forge"
)

func TestExecute_netAllowListValidationRejectsWithoutSpawningDeno(t *testing.T) {
	cases := []struct {
		name string
		net  []string
		want []string
	}{
		{"schemeURL", []string{"https://api.github.com/whatever"}, []string{`"https://api.github.com/whatever"`, "expected host"}},
		{"commaInjected", []string{"a.com,b.com"}, []string{`"a.com,b.com"`, "expected host"}},
		{"whitespace", []string{"a.com "}, []string{`"a.com "`, "expected host"}},
		{"bareWildcard", []string{"*"}, []string{`"*"`, "dangerous_allow_any_url"}},
		{"tooMany", manyHosts(maxAllowListEntries + 1), []string{"too many"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := forge.Execute(context.Background(), forge.Params{Code: "async () => 1", Net: tc.net})
			assertGrantValidationError(t, err, tc.want)
		})
	}
}

func TestExecute_netAllowListScopedWildcardIsValid(t *testing.T) {
	requireDeno(t)
	_, err := forge.Execute(context.Background(), forge.Params{
		Code: "async () => 1",
		Net:  []string{"*.github.com"},
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
}

func TestExecute_envAllowListValidationRejectsWithoutSpawningDeno(t *testing.T) {
	cases := []struct {
		name string
		env  []string
		want []string
	}{
		{"badName", []string{"1BAD"}, []string{`"1BAD"`, "expected a valid env var name"}},
		{"reservedName", []string{"PATH"}, []string{`"PATH"`, "reserved"}},
		{"tooMany", manyEnvNames(maxAllowListEntries + 1), []string{"too many"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := forge.Execute(context.Background(), forge.Params{Code: "async () => 1", Env: tc.env})
			assertGrantValidationError(t, err, tc.want)
		})
	}
}

const maxAllowListEntries = 32

func assertGrantValidationError(t *testing.T, err error, want []string) {
	t.Helper()
	fe := asForgeError(t, err)
	if fe.Kind != forge.KindRunner {
		t.Errorf("Kind = %q, want %q", fe.Kind, forge.KindRunner)
	}
	for _, sub := range want {
		if !strings.Contains(fe.Message, sub) {
			t.Errorf("Message = %q, want it to contain %q", fe.Message, sub)
		}
	}
}

func TestExecute_fileReadAllowListValidation(t *testing.T) {
	cases := []struct {
		name  string
		paths []string
		want  []string
	}{
		{"relative", []string{"relative/path"}, []string{"expected an absolute path"}},
		{"fsRoot", []string{"/"}, []string{"within your home directory"}},
		{"systemPath", []string{"/etc/passwd"}, []string{`"/etc/passwd"`, "within your home directory"}},
		{"commaSmuggledSystemPath", []string{"/Users/me/ok,/etc"}, []string{`"/Users/me/ok,/etc"`, "comma is not allowed"}},
		{"uncleanTrailingSlash", []string{"/a/"}, []string{"path is not clean", `"/a"`, `"/a/"`}},
		{"uncleanDotDot", []string{"/a/../b"}, []string{"path is not clean"}},
		{"uncleanDoubleSlash", []string{"//x"}, []string{"path is not clean"}},
		{"tooMany", manyPaths(maxAllowListEntries + 1), []string{"too many"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := forge.Execute(context.Background(), forge.Params{Code: "async () => 1", ReadPaths: tc.paths})
			assertGrantValidationError(t, err, tc.want)
		})
	}
}

func TestExecute_fileWriteAllowListValidation(t *testing.T) {
	_, err := forge.Execute(context.Background(), forge.Params{Code: "async () => 1", WritePaths: []string{"relative/path"}})
	assertGrantValidationError(t, err, []string{"expected an absolute path", "code_mode.file_write_allow_list"})
}

func TestExecute_fileAllowListEmptyIsAccepted(t *testing.T) {
	requireDeno(t)
	_, err := forge.Execute(context.Background(), forge.Params{Code: "async () => 1"})
	if err != nil {
		t.Fatalf("empty file allowlists should be accepted: %v", err)
	}
}

func TestExecute_fileAllowListAcceptsHomeAndTempPaths(t *testing.T) {
	requireDeno(t)
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("UserHomeDir: %v", err)
	}
	cases := []struct {
		name string
		path string
	}{
		{"underHome", filepath.Join(home, "forge-grant-test-data")},
		{"underTemp", filepath.Join(filepath.Clean(os.TempDir()), "forge-grant-test-data")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := forge.Execute(context.Background(), forge.Params{Code: "async () => 1", ReadPaths: []string{tc.path}})
			if err != nil {
				t.Fatalf("Execute with grant %q: %v", tc.path, err)
			}
		})
	}
}

func manyPaths(n int) []string {
	paths := make([]string, n)
	for i := range paths {
		paths[i] = fmt.Sprintf("/data/dir%d", i)
	}
	return paths
}

func manyHosts(n int) []string {
	hosts := make([]string, n)
	for i := range hosts {
		hosts[i] = fmt.Sprintf("host%d.example.com", i)
	}
	return hosts
}

func manyEnvNames(n int) []string {
	names := make([]string, n)
	for i := range names {
		names[i] = fmt.Sprintf("VAR_%d", i)
	}
	return names
}
