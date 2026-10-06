package main

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/mcpmini/mini/internal/testutil"
)

const (
	integrationHeader = "//go:build integration\n\npackage p\n\nimport \"testing\"\n\n"
	plainHeader       = "package p\n\nimport \"testing\"\n\n"
)

func TestCheckSource(t *testing.T) {
	tests := []struct {
		name string
		path string
		src  string
		want string
	}{
		{
			name: "tagged file with wrong name is flagged",
			path: "a_test.go",
			src:  integrationHeader + "func TestIntegrationA(t *testing.T) {}\n",
			want: "a_test.go:1: file has the integration build constraint",
		},
		{
			name: "named file without constraint is flagged",
			path: "a_integration_test.go",
			src:  plainHeader + "func TestIntegrationA(t *testing.T) {}\n",
			want: "a_integration_test.go:1: file is named",
		},
		{
			name: "unprefixed test in integration file is flagged with its line",
			path: "a_integration_test.go",
			src:  integrationHeader + "func TestCLI_version(t *testing.T) {}\n",
			want: "a_integration_test.go:7: TestCLI_version in an integration file",
		},
		{
			name: "TestIntegration in plain file is flagged",
			path: "a_test.go",
			src:  plainHeader + "func TestIntegrationA(t *testing.T) {}\n",
			want: "a_test.go:5: TestIntegrationA uses the TestIntegration prefix",
		},
		{
			name: "constraint combined with other tags counts",
			path: "a_test.go",
			src:  "//go:build integration && test\n\npackage p\n",
			want: "a_test.go:1: file has the integration build constraint",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := checkSource(tc.path, []byte(tc.src))
			if len(got) != 1 || !strings.HasPrefix(got[0], tc.want) {
				t.Fatalf("want one violation starting %q, got %v", tc.want, got)
			}
		})
	}
}

func TestCheckSource_clean(t *testing.T) {
	tests := []struct {
		name string
		path string
		src  string
	}{
		{
			name: "integration file with prefixed tests, TestMain, and helpers",
			path: "a_integration_test.go",
			src: integrationHeader + "func TestMain(m *testing.M) {}\n" +
				"func TestIntegrationA(t *testing.T) {}\n" +
				"func TestHelper() {}\n" +
				"func TestBuild(t *testing.T, n int) {}\n",
		},
		{
			name: "plain file with unit tests",
			path: "a_test.go",
			src:  plainHeader + "func TestA(t *testing.T) {}\n",
		},
		{
			name: "negated constraint is not integration",
			path: "a_test.go",
			src:  "//go:build !integration\n\npackage p\n",
		},
		{
			name: "constraint after the package clause is ignored",
			path: "a_test.go",
			src:  "package p\n\n// //go:build integration\n",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := checkSource(tc.path, []byte(tc.src)); len(got) != 0 {
				t.Fatalf("want no violations, got %v", got)
			}
		})
	}
}

func TestCheckTree(t *testing.T) {
	root := t.TempDir()
	testutil.WriteFile(
		t,
		filepath.Join(root, "ok", "a_integration_test.go"),
		integrationHeader+"func TestIntegrationA(t *testing.T) {}\n",
	)
	testutil.WriteFile(
		t,
		filepath.Join(root, "bad", "b_test.go"),
		plainHeader+"func TestIntegrationB(t *testing.T) {}\n",
	)
	testutil.WriteFile(
		t,
		filepath.Join(root, "testdata", "c_test.go"),
		plainHeader+"func TestIntegrationC(t *testing.T) {}\n",
	)
	testutil.WriteFile(
		t,
		filepath.Join(root, ".claude", "d_test.go"),
		plainHeader+"func TestIntegrationD(t *testing.T) {}\n",
	)

	got, err := checkTree(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || !strings.Contains(got[0], filepath.Join("bad", "b_test.go")+":5:") {
		t.Fatalf("want only the bad/b_test.go violation, got %v", got)
	}
}
