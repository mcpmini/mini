//go:build integration

package main

import (
	"bytes"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mcpmini/mini/internal/testutil"
)

type fixtureChecker struct {
	binary, dir, tags string
}

func TestIntegrationStructlintCheckAndFix(t *testing.T) {
	binary := testutil.Binary(t, "STRUCTLINT_BIN")
	dir := fixtureModule(t, "package fixture\ntype Quad struct{A,B,C,D int}\n")
	checker := fixtureChecker{binary: binary, dir: dir, tags: "fixture"}
	tagged := "//go:build fixture\npackage fixture\nvar _ = Quad{1,2,3,4}\n"
	external := "package fixture_test\nimport f \"example.com/fixture\"\nvar _ = f.Quad{5,6,7,8}\n"
	testutil.WriteFile(t, filepath.Join(dir, "tagged_test.go"), tagged)
	testutil.WriteFile(t, filepath.Join(dir, "external_test.go"), external)
	output, err := checker.run(t, "./...")
	if err == nil || !strings.Contains(output, "use field names") {
		t.Fatalf("check = %v, %s; want an unkeyed-literal diagnostic", err, output)
	}
	if got := string(testutil.ReadFile(t, filepath.Join(dir, "tagged_test.go"))); got != tagged {
		t.Fatalf("check mode modified source: %s", got)
	}
	checker.requireSuccess(t, "-fix", "./...")
	fixed := testutil.ReadFile(t, filepath.Join(dir, "tagged_test.go"))
	if !bytes.Contains(fixed, []byte("A: 1")) || bytes.Contains(fixed, []byte("A: A:")) {
		t.Fatalf("test-package fix was missing or duplicated: %s", fixed)
	}
	checker.requireSuccess(t, "./...")
	checker.requireSuccess(t, "-fix", "./...")
	if again := testutil.ReadFile(t, filepath.Join(dir, "tagged_test.go")); !bytes.Equal(fixed, again) {
		t.Fatalf("second fix changed source: %s", again)
	}
	compileFixture(t, dir, "fixture")
}

func TestIntegrationStructlintUnfixableLiteralRemainsAnError(t *testing.T) {
	binary := testutil.Binary(t, "STRUCTLINT_BIN")
	source := "package fixture\nfunc effect() int { return 2 }\nvar _ = struct{A,_,C,D int}{1,effect(),3,4}\n"
	dir := fixtureModule(t, source)
	checker := fixtureChecker{binary: binary, dir: dir}
	checker.requireSuccess(t, "-fix", "./...")
	if got := string(testutil.ReadFile(t, filepath.Join(dir, "fixture.go"))); got != source {
		t.Fatalf("unfixable literal was modified: %s", got)
	}
	output, err := checker.run(t, "./...")
	if err == nil || !strings.Contains(output, "use field names") {
		t.Fatalf("unfixable literal passed check mode: %v, %s", err, output)
	}
}

func TestIntegrationStructlintFixPreservesNestedEvaluationOrder(t *testing.T) {
	binary := testutil.Binary(t, "STRUCTLINT_BIN")
	source := `package main
import "fmt"
type Quad struct{A,B,C,D int}
type Outer struct{First Quad; B,C,D int}
func effect(n int) int { fmt.Print(n); return n }
func main() { fmt.Print(Outer{Quad{effect(1),effect(2),effect(3),effect(4)},effect(5),effect(6),effect(7)}) }
`
	dir := fixtureModule(t, source)
	checker := fixtureChecker{binary: binary, dir: dir}
	before := runFixture(t, dir)
	checker.requireSuccess(t, "-fix", "./...")
	after := runFixture(t, dir)
	if before != "1234567{{1 2 3 4} 5 6 7}" || after != before {
		t.Fatalf("evaluation changed: before %q, after %q", before, after)
	}
}

func fixtureModule(t *testing.T, source string) string {
	t.Helper()
	dir := t.TempDir()
	testutil.WriteFile(t, filepath.Join(dir, "go.mod"), "module example.com/fixture\n\ngo 1.26.0\n")
	testutil.WriteFile(t, filepath.Join(dir, "fixture.go"), source)
	return dir
}

func (checker fixtureChecker) run(t *testing.T, args ...string) (string, error) {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), checker.binary, args...)
	cmd.Dir = checker.dir
	cmd.Env = append(cmd.Environ(), "GOWORK=off", "GOFLAGS=-tags="+checker.tags)
	output, err := cmd.CombinedOutput()
	return string(output), err
}

func (checker fixtureChecker) requireSuccess(t *testing.T, args ...string) {
	t.Helper()
	if output, err := checker.run(t, args...); err != nil {
		t.Fatalf("structlint %v: %v\n%s", args, err, output)
	}
}

func compileFixture(t *testing.T, dir, tags string) {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), "go", "test", "-vet=off", "-tags", tags, "./...")
	cmd.Dir = dir
	cmd.Env = append(cmd.Environ(), "GOWORK=off")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("fixed fixture does not compile: %v\n%s", err, output)
	}
}

func runFixture(t *testing.T, dir string) string {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), "go", "run", ".")
	cmd.Dir = dir
	cmd.Env = append(cmd.Environ(), "GOWORK=off")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("run fixture: %v\n%s", err, output)
	}
	return string(output)
}
