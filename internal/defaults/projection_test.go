package defaults_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/mcpmini/mini/internal/config"
	minidefaults "github.com/mcpmini/mini/internal/defaults"
	"github.com/mcpmini/mini/internal/projection"
	"github.com/mcpmini/mini/internal/testutil"
)

func TestBundledProjectionsPreserveFixtureShape(t *testing.T) {
	for _, tc := range projectionFixtureCases {
		t.Run(tc.server+"/"+tc.tool, func(t *testing.T) {
			assertFixtureShape(t, tc)
		})
	}
}

type projectionFixtureCase struct {
	server, tool string
	requiredKeys []string
}

var projectionFixtureCases = []projectionFixtureCase{
	{server: "github", tool: "list_pull_requests", requiredKeys: []string{"number", "title", "state"}},
	{server: "github", tool: "list_issues", requiredKeys: []string{"issues", "totalCount"}},
	{server: "github", tool: "get_commit", requiredKeys: []string{"sha", "commit"}},
	{server: "github", tool: "list_commits", requiredKeys: []string{"sha", "commit"}},
	{server: "github", tool: "search_repositories", requiredKeys: []string{"total_count", "items"}},
	{server: "github", tool: "search_code", requiredKeys: []string{"total_count", "items"}},
	{server: "github", tool: "issue_read", requiredKeys: []string{"number", "title", "body"}},
	{server: "github", tool: "pull_request_read", requiredKeys: []string{"number", "title", "body"}},
}

func assertFixtureShape(t *testing.T, tc projectionFixtureCase) {
	t.Helper()
	fixture := findProjectionFixture(t, tc.server, tc.tool)
	raw := testutil.ReadFile(t, fixture)
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		t.Fatalf("parse fixture: %v", err)
	}
	result := projection.Apply(value, loadBundledProjection(t, tc), defaultProjectionDefaults())
	fields := firstObject(t, result.Summary)
	for _, key := range tc.requiredKeys {
		if _, ok := fields[key]; !ok {
			t.Errorf("projected output is missing required field %q", key)
		}
	}
}

func findProjectionFixture(t *testing.T, server, tool string) string {
	t.Helper()
	_, source, _, _ := runtime.Caller(0)
	base := filepath.Join(filepath.Dir(source), "..", "..", "benchmarks", "fixtures", server)
	for _, suffix := range []string{".json", ".live.json"} {
		path := filepath.Join(base, tool+suffix)
		if _, err := os.Stat(path); err == nil {
			return path
		}
	}
	t.Fatalf("missing fixture for %s/%s", server, tool)
	return ""
}

func loadBundledProjection(t *testing.T, tc projectionFixtureCase) *config.ProjectionConfig {
	t.Helper()
	data := minidefaults.ProjectionFor(tc.server)
	if data == nil {
		t.Fatalf("missing bundled projection for %q", tc.server)
	}
	var projections map[string]*config.ProjectionConfig
	if err := yaml.Unmarshal(data, &projections); err != nil {
		t.Fatalf("parse bundled projection for %q: %v", tc.server, err)
	}
	if rule := projections[tc.tool]; rule != nil {
		return rule
	}
	t.Fatalf("missing bundled projection rule for %s/%s", tc.server, tc.tool)
	return nil
}

func defaultProjectionDefaults() *projection.Defaults {
	cfg := config.DefaultConfig()
	return &projection.Defaults{
		StringLimit:        cfg.DefaultStringLimit,
		DepthLimit:         cfg.DefaultDepthLimit,
		ContentFields:      cfg.ContentFields,
		AutoStripThreshold: cfg.AutoStripThreshold,
	}
}

func firstObject(t *testing.T, value any) map[string]any {
	t.Helper()
	switch typed := value.(type) {
	case map[string]any:
		if len(typed) == 0 {
			t.Fatal("projected output is empty")
		}
		return typed
	case []any:
		if len(typed) == 0 {
			t.Fatal("projected output is empty")
		}
		object, ok := typed[0].(map[string]any)
		if !ok || len(object) == 0 {
			t.Fatal("first projected item is not a nonempty object")
		}
		return object
	default:
		t.Fatalf("unexpected projected output type %T", value)
	}
	return nil
}

func TestPullRequestProjectionPreservesForkContext(t *testing.T) {
	var value any
	err := json.Unmarshal([]byte(`{
		"number": 42, "body": "Review the fork's implementation",
		"user": {"login": "contributor", "id": 7, "profile_url": "https://github.com/contributor", "avatar_url": "https://example.test/avatar"},
		"head": {"ref": "feature", "sha": "abc123", "repo": {"full_name": "contributor/project", "description": "Experimental fork"}},
		"base": {"ref": "main", "sha": "def456", "repo": {"full_name": "upstream/project", "description": "Upstream project"}}
	}`), &value)
	if err != nil {
		t.Fatal(err)
	}
	rule := loadBundledProjection(t, projectionFixtureCase{server: "github", tool: "pull_request_read"})
	result := projection.Apply(value, rule, defaultProjectionDefaults())
	want := firstObject(t, value)
	delete(want["user"].(map[string]any), "avatar_url")
	if !reflect.DeepEqual(result.Summary, want) {
		t.Fatalf("fork context changed: got %#v, want %#v", result.Summary, want)
	}
	if !reflect.DeepEqual(result.ExcludedKeys, []string{".user.avatar_url"}) {
		t.Fatalf("excluded paths = %v", result.ExcludedKeys)
	}
}
