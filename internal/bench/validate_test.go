//go:build test

package bench_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/mcpmini/mini/internal/bench"
	"github.com/mcpmini/mini/internal/config"
	minidefaults "github.com/mcpmini/mini/internal/defaults"
	"github.com/mcpmini/mini/internal/projection"
	"github.com/mcpmini/mini/internal/response"
)

// validateCase defines what to assert after projecting a fixture.
//
// Fixtures MUST be captured from real MCP tool responses — not raw REST API
// calls. The MCP server transforms data before returning it, and the shapes can
// differ significantly. See benchmarks/README.md for capture instructions.
type validateCase struct {
	projection      string   // bundled projection key; defaults to server dir name
	projTool        string   // projection config tool key; defaults to fixture filename
	requiredKeys    []string // must appear in projected output (top-level, or first array item)
	minReductionPct float64  // minimum % token reduction; 0 = no minimum enforced
}

// fixtureValidations maps "server/tool" → assertions.
// tool = fixture filename without extension (.live suffix also stripped).
// If projTool differs from the fixture filename, the named config won't match —
// only the "*" wildcard will apply. The test logs this as a warning.
var fixtureValidations = map[string]validateCase{
	"github/list_pull_requests":  {requiredKeys: []string{"number", "title", "state"}, minReductionPct: 15},
	"github/list_issues":         {requiredKeys: []string{"issues", "totalCount"}, minReductionPct: 10},
	"github/get_commit":          {requiredKeys: []string{"sha", "commit"}, minReductionPct: 5},
	"github/list_commits":        {requiredKeys: []string{"sha", "commit"}, minReductionPct: 5},
	"github/search_repositories": {requiredKeys: []string{"total_count", "items"}, minReductionPct: 5},
	"github/search_code":         {requiredKeys: []string{"total_count", "items"}, minReductionPct: 20},
	"github/get_file_contents":   {requiredKeys: []string{"type", "content"}},
	"github/create_pull_request": {requiredKeys: []string{"number", "html_url"}},
	"github/issue_read":          {requiredKeys: []string{"number", "title", "body"}},
	"github/pull_request_read":   {requiredKeys: []string{"number", "title", "body"}},
	// jira/search_issues uses old tool naming ("search_issues"); our atlassian
	// projection config uses "jira_search". Only wildcard "*" applies (auto_strip_threshold).
	// 30% reflects what markup stripping alone achieves on a 35K-token Jira response.
	// Replace with "atlassian/jira_search" once real mcp-atlassian fixtures exist.
	"jira/search_issues": {projection: "atlassian", projTool: "search_issues", requiredKeys: []string{"issues", "total"}, minReductionPct: 25},
	"linear/list_issues":          {requiredKeys: []string{"nodes"}, minReductionPct: 5},
	"sentry/list_issues":          {requiredKeys: []string{"id", "title"}, minReductionPct: 20},
	"slack/conversations_history": {requiredKeys: []string{"messages"}, minReductionPct: 5},
}

func TestProjectionValidation(t *testing.T) {
	_, thisFile, _, _ := runtime.Caller(0)
	root := filepath.Join(filepath.Dir(thisFile), "..", "..")
	fixturesBase := filepath.Join(root, "benchmarks", "fixtures")
	projDefaults := bench.DefaultProjectionDefaults()

	for key, vc := range fixtureValidations {
		key, vc := key, vc
		t.Run(key, func(t *testing.T) {
			serverDir, toolName := splitValidateKey(key)
			fixturePath := findFixturePath(fixturesBase, serverDir, toolName)
			if fixturePath == "" {
				t.Skipf("fixture not found: benchmarks/fixtures/%s/%s.json", serverDir, toolName)
			}

			raw, err := os.ReadFile(fixturePath)
			if err != nil {
				t.Fatalf("read fixture: %v", err)
			}

			projServer := vc.projection
			if projServer == "" {
				projServer = serverDir
			}
			projMap := loadBundledProjectionMap(t, projServer)

			lookupTool := vc.projTool
			if lookupTool == "" {
				lookupTool = toolName
			}
			toolCfg, namedMatch := resolveToolProjection(projMap, lookupTool)
			if !namedMatch {
				t.Logf("WARNING: no named projection entry for tool %q — only wildcard \"*\" applies", lookupTool)
			}

			var value any
			if err := json.Unmarshal(raw, &value); err != nil {
				t.Fatalf("parse fixture: %v", err)
			}

			result := projection.Apply(value, toolCfg, projDefaults)
			projectedJSON, _ := json.Marshal(result.Summary)

			rawTokens := response.EstimateTokensRaw(raw)
			projTokens := response.EstimateTokensRaw(projectedJSON)

			if rawTokens == 0 {
				t.Fatal("fixture produced zero raw tokens")
			}

			reductionPct := float64(rawTokens-projTokens) / float64(rawTokens) * 100
			t.Logf("tokens: raw=%d projected=%d reduction=%.1f%%", rawTokens, projTokens, reductionPct)

			if vc.minReductionPct > 0 && reductionPct < vc.minReductionPct {
				t.Errorf("token reduction %.1f%% below required %.1f%%", reductionPct, vc.minReductionPct)
			}

			checkRequiredKeys(t, result.Summary, vc.requiredKeys)
			checkNotEmpty(t, result.Summary)
		})
	}
}

// checkRequiredKeys verifies that each key exists in the projected summary.
// For flat-array responses, checks keys on the first item.
func checkRequiredKeys(t *testing.T, projected any, keys []string) {
	t.Helper()
	if len(keys) == 0 {
		return
	}
	m, ok := topLevelMap(projected)
	if !ok {
		t.Logf("projected output is not a map — skipping key checks")
		return
	}
	for _, k := range keys {
		if _, ok := m[k]; !ok {
			t.Errorf("required key %q missing from projected output", k)
		}
	}
}

// topLevelMap returns the map to check required keys against.
// For dicts it's the dict itself; for arrays it's the first element.
func topLevelMap(v any) (map[string]any, bool) {
	switch typed := v.(type) {
	case map[string]any:
		return typed, true
	case []any:
		if len(typed) == 0 {
			return nil, false
		}
		m, ok := typed[0].(map[string]any)
		return m, ok
	}
	return nil, false
}

// checkNotEmpty fails if the projected summary is a bare empty map or nil.
func checkNotEmpty(t *testing.T, projected any) {
	t.Helper()
	switch v := projected.(type) {
	case nil:
		t.Error("projected output is nil")
	case map[string]any:
		if len(v) == 0 {
			t.Error("projected output is an empty map {} — include filter likely drops all keys")
		}
	case string:
		if strings.Contains(v, "[depth limit reached]") {
			t.Errorf("projected output hit depth limit at top level: %s", v)
		}
	case []any:
		if len(v) == 0 {
			t.Error("projected output is an empty array")
		}
	}
}

func splitValidateKey(key string) (server, tool string) {
	if i := strings.IndexByte(key, '/'); i >= 0 {
		return key[:i], key[i+1:]
	}
	return key, ""
}

func findFixturePath(base, server, tool string) string {
	for _, suffix := range []string{".json", ".live.json"} {
		p := filepath.Join(base, server, tool+suffix)
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return ""
}

func loadBundledProjectionMap(t *testing.T, server string) map[string]*config.ProjectionConfig {
	t.Helper()
	data := minidefaults.ProjectionFor(server)
	if data == nil {
		t.Fatalf("no bundled projection for server %q", server)
	}
	var m map[string]*config.ProjectionConfig
	if err := yaml.Unmarshal(data, &m); err != nil {
		t.Fatalf("parse projection for %q: %v", server, err)
	}
	return m
}

// resolveToolProjection returns the config for tool, plus whether it was a named match.
func resolveToolProjection(projMap map[string]*config.ProjectionConfig, tool string) (*config.ProjectionConfig, bool) {
	if c, ok := projMap[tool]; ok {
		return c, true
	}
	return projMap["*"], false
}

