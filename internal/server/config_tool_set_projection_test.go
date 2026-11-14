//go:build test

package server_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/server"
	"github.com/mcpmini/mini/internal/testutil"
)

func TestConfigureSetProjection_aFailedSaveKeepsTheLiveRuleAndSessionOnlyStillApplies(t *testing.T) {
	const broken = "command: echo\nprojections: {getData: {include_only: 5}}\n"
	cases := map[string]func(t *testing.T, path string){
		"broken server file": func(t *testing.T, path string) { testutil.WriteFile(t, path, broken) },
		"missing server file": func(t *testing.T, path string) {
			if err := os.Remove(path); err != nil {
				t.Fatal(err)
			}
		},
		"unwritable servers directory": func(t *testing.T, path string) {
			dir := filepath.Dir(path)
			if err := os.Chmod(dir, 0o500); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(
				func() { os.Chmod(dir, 0o700) },
			) //nolint:errcheck // TempDir cleanup reports a directory it can't remove
		},
	}
	for name, breakFile := range cases {
		t.Run(name, func(t *testing.T) {
			rules := rulesByScope{"getData": {IncludeOnly: []string{"a"}}}
			srv := newProjectionServer(t, config.ServerConfig{Name: "svc", Projections: rules})
			path := config.ServerPath(srv.ConfigDir(), "svc")
			breakFile(t, path)
			before := serverFileText(t, path)
			s := openSession(t, srv)

			saved := s.configure(includeOnlyB(false))
			assertIsErrorResult(t, saved)
			if text := toolResultText(t, saved); !strings.Contains(text, "session_only") {
				t.Errorf("failed save = %q, want session_only advice", text)
			}
			s.expectGetDataFields([]string{"a"}, []string{"b"})

			if text := toolResultText(t, s.configure(includeOnlyB(true))); strings.Contains(text, `"error"`) {
				t.Fatalf("session_only set_projection = %s", text)
			}
			s.expectGetDataFields([]string{"b"}, []string{"a"})
			if after := serverFileText(t, path); after != before {
				t.Errorf("server file = %q, want it untouched (%q)", after, before)
			}
		})
	}
}

func TestConfigureSetProjectionRequiresTool(t *testing.T) {
	srv := newTestServer(t, server.Params{})
	resp := serve(t, srv, callTool("config", map[string]any{
		"action": "set_projection",
		"server": "svc",
	}))
	assertIsErrorResult(t, resp)
}

func TestConfigureSetProjection_rejectsMiniFormat(t *testing.T) {
	t.Run("server scope", func(t *testing.T) {
		srv := newTestServer(t, server.Params{})
		resp := serve(t, srv, callTool("config", map[string]any{
			"action": "set_projection", "server": "svc", "tool": "tool1",
			"projection": map[string]any{"format": "mini"},
		}))
		assertIsErrorResult(t, resp)
		if text := toolResultText(t, resp); !strings.Contains(text, "toon") {
			t.Errorf("expected error naming toon as the replacement, got: %s", text)
		}
	})
	t.Run("session scope", func(t *testing.T) {
		srv := newTestServer(t, server.Params{})
		resp := serve(t, srv, callTool("config", map[string]any{
			"action": "set_projection", "server": "svc", "tool": "tool1", "session_only": true,
			"projection": map[string]any{"format": "mini"},
		}))
		assertIsErrorResult(t, resp)
		if text := toolResultText(t, resp); !strings.Contains(text, "toon") {
			t.Errorf("expected error naming toon as the replacement, got: %s", text)
		}
	})
}

func TestConfigureSetProjection_rejectsUnknownOption(t *testing.T) {
	srv := newTestServer(t, server.Params{})
	resp := serve(t, srv, callTool("config", map[string]any{
		"action": "set_projection", "server": "svc", "tool": "tool1",
		"projection": map[string]any{"exlude": []string{"secret"}},
	}))
	assertIsErrorResult(t, resp)
	if text := toolResultText(t, resp); !strings.Contains(text, `"exlude"`) || !strings.Contains(text, "exclude") {
		t.Errorf("expected an error naming the unknown option and the valid ones, got: %s", text)
	}
}

func TestConfigureProjectionRejectsUnsupportedExcludePathsWithoutPublishing(t *testing.T) {
	for _, scope := range []string{"server", "session"} {
		t.Run(scope, func(t *testing.T) {
			previous := &config.ProjectionConfig{Exclude: []string{"head.repo.description", "[].author.avatar_url"}}
			s := newProjectionSession(t, rulesByScope{"getData": previous})
			args := map[string]any{
				"action": "set_projection", "server": "svc", "tool": "getData", "session_only": scope == "session",
				"projection": previous,
			}
			if text := toolResultText(t, s.configure(args)); !strings.Contains(text, `"ok":true`) {
				t.Fatalf("valid projection was rejected: %s", text)
			}
			want := s.projection("getData")
			file := config.ServerPath(s.h.srv.ConfigDir(), "svc")
			before := string(testutil.ReadFile(t, file))
			args["projection"] = &config.ProjectionConfig{Exclude: []string{"number", "items[0].desc"}}
			resp := s.configure(args)
			assertIsErrorResult(t, resp)
			text := toolResultText(t, resp)
			if !strings.Contains(text, `invalid exclude path "items[0].desc"`) ||
				!strings.Contains(text, "for example items[].secret") {
				t.Errorf("rejected path needs actionable error: %s", text)
			}
			assertReport(t, s.projection("getData"), want)
			if after := string(testutil.ReadFile(t, file)); after != before {
				t.Errorf("invalid projection changed persisted rule: %s", after)
			}
		})
	}
}

func serverFileText(t *testing.T, path string) string {
	t.Helper()
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return "<missing>"
	}
	return string(testutil.ReadFile(t, path))
}
