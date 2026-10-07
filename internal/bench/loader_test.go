package bench_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mcpmini/mini/internal/bench"
	"github.com/mcpmini/mini/internal/testutil"
)

func TestLoadFixtures_missingProjectionIsOptional(t *testing.T) {
	benchDir := loaderFixtureDir(t)

	cases, err := bench.LoadFixtures(benchDir)
	if err != nil {
		t.Fatalf("LoadFixtures: %v", err)
	}
	if len(cases) != 1 || cases[0].ProjConfig != nil {
		t.Fatalf("expected one fixture with no projection, got %+v", cases)
	}
}

func TestLoadFixtures_malformedProjectionReturnsError(t *testing.T) {
	benchDir := loaderFixtureDir(t)
	testutil.WriteFile(t, filepath.Join(benchDir, "projections", "sample.yaml"), "[invalid")

	_, err := bench.LoadFixtures(benchDir)
	if err == nil || !strings.Contains(err.Error(), "sample.yaml") {
		t.Fatalf("expected contextual projection parse error, got %v", err)
	}
}

func TestLoadFixtures_projectionDirectoryReturnsReadError(t *testing.T) {
	benchDir := loaderFixtureDir(t)
	projectionPath := filepath.Join(benchDir, "projections", "sample.yaml")
	if err := os.MkdirAll(filepath.Dir(projectionPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(projectionPath, 0o700); err != nil {
		t.Fatal(err)
	}

	_, err := bench.LoadFixtures(benchDir)
	if err == nil || !strings.Contains(err.Error(), projectionPath) {
		t.Fatalf("expected contextual projection read error for %s, got %v", projectionPath, err)
	}
}

func TestLoadFixtures_validWildcardProjectionApplies(t *testing.T) {
	benchDir := loaderFixtureDir(t)
	projectionYAML := `"*":
  include_only: [id]
  string_limits:
    title: 12
`
	testutil.WriteFile(t, filepath.Join(benchDir, "projections", "sample.yaml"), projectionYAML)

	cases, err := bench.LoadFixtures(benchDir)
	if err != nil {
		t.Fatalf("LoadFixtures: %v", err)
	}
	if len(cases) != 1 || cases[0].ProjConfig == nil {
		t.Fatalf("expected wildcard projection on fixture, got %+v", cases)
	}
	if len(cases[0].ProjConfig.IncludeOnly) != 1 || cases[0].ProjConfig.IncludeOnly[0] != "id" ||
		cases[0].ProjConfig.StringLimits["title"] != 12 {
		t.Fatalf("unexpected wildcard projection: %+v", cases[0].ProjConfig)
	}
}

func loaderFixtureDir(t *testing.T) string {
	t.Helper()
	benchDir := t.TempDir()
	testutil.WriteFile(t, filepath.Join(benchDir, "fixtures", "sample", "tool.json"), `{"id":1,"title":"sample"}`)
	return benchDir
}
