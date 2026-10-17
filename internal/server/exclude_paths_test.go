//go:build test

package server_test

import (
	"strings"
	"testing"

	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/testutil"
)

func TestConfigureProjectionRejectsUnsupportedExcludePathsWithoutPublishing(t *testing.T) {
	paths := []string{
		"",
		"items[*].desc",
		"items[].desc",
		"items[0].desc",
		".description",
		"head.",
		"head..description",
		"..description",
		"*",
		"head.*",
		"head | .repo",
		"head/description",
		"head description",
		"café",
	}
	for _, scope := range []string{"server", "session"} {
		for _, path := range paths {
			t.Run(scope+"/"+path, func(t *testing.T) {
				assertRejectedExcludePreservesRule(t, scope, path)
			})
		}
	}
}

func assertRejectedExcludePreservesRule(t *testing.T, scope, path string) {
	t.Helper()
	const sessionID = "cccccccc-cccc-cccc-cccc-000000000051"
	previous := &config.ProjectionConfig{Exclude: []string{"head.repo.description"}}
	srv := newProjectionServer(t, sessionID, map[string]*config.ProjectionConfig{"getData": previous})
	args := map[string]any{
		"action": "set_projection", "server": "svc", "tool": "getData", "session_only": scope == "session",
		"projection": previous,
	}
	resp := postMCP(t, srv, sessionID, configCall(11, args))
	if text := toolResultText(t, resp); !strings.Contains(text, `"ok":true`) {
		t.Fatalf("valid projection was rejected: %s", text)
	}
	want := getProjection(t, srv, sessionID, "getData")
	file := config.ServerPath(srv.ConfigDir(), "svc")
	before := string(testutil.ReadFile(t, file))
	args["projection"] = &config.ProjectionConfig{Exclude: []string{"number", path}}
	resp = postMCP(t, srv, sessionID, configCall(12, args))
	assertIsErrorResult(t, resp)
	if text := toolResultText(
		t,
		resp,
	); !strings.Contains(text, "invalid exclude path") ||
		!strings.Contains(text, "dot-separated object fields") {
		t.Errorf("rejected path needs actionable error: %s", text)
	}
	assertReport(t, getProjection(t, srv, sessionID, "getData"), want)
	if after := string(testutil.ReadFile(t, file)); after != before {
		t.Errorf("invalid projection changed persisted rule: %s", after)
	}
}
