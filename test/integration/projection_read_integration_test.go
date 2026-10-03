//go:build integration

package integration_test

import (
	"strings"
	"testing"

	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/config/configtest"
)

func TestIntegrationProjection_readRecoversProjectedData(t *testing.T) {
	cases := []struct {
		name       string
		fixture    string
		projection map[string]*config.ProjectionConfig
		wantByPath map[string]string // path as reported in __mini → expected read() result
	}{
		{
			name:       "excluded field",
			fixture:    `{"id":1,"secret":"hidden"}`,
			projection: map[string]*config.ProjectionConfig{"get_item": {Exclude: []string{"secret"}}},
			wantByPath: map[string]string{
				".secret": `"hidden"`,
			},
		},
		{
			name:       "truncated string",
			fixture:    `{"id":1,"body":"` + strings.Repeat("x", 100) + `"}`,
			projection: map[string]*config.ProjectionConfig{"get_item": {StringLimits: map[string]int{"body": 20}}},
			wantByPath: map[string]string{
				".body": `"` + strings.Repeat("x", 100) + `"`,
			},
		},
		{
			name:       "truncated array",
			fixture:    `{"id":1,"items":[{"n":1},{"n":2},{"n":3}]}`,
			projection: map[string]*config.ProjectionConfig{"get_item": {ArrayLimits: map[string]int{"items": 1}}},
			wantByPath: map[string]string{
				".items": `[{"n":1},{"n":2},{"n":3}]`,
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := t.TempDir()
			writeFakeServer(t, cfg, fakeServerParams{
				ServerName: "svc",
				Fixtures:   mockFixtureDir(t, map[string]string{"get_item": tc.fixture}),
			})
			writeConfig(t, cfg, "response_dir: "+t.TempDir()+"\n")
			configtest.WriteProjections(t, cfg, configtest.ProjectionFile{ServerName: "svc", Tools: tc.projection})
			client := startProxyServer(t, cfg)

			raw := client.mustCall("tools/call", map[string]any{
				"name":      "svc__get_item",
				"arguments": map[string]any{},
			})
			text, _ := parseToolCallResult(raw)
			env := parseMiniEnv(t, text)

			reportedPaths := env.Excluded
			for _, tr := range env.Truncated {
				reportedPaths = append(reportedPaths, tr.Path)
			}

			for _, path := range reportedPaths {
				want, ok := tc.wantByPath[path]
				if !ok {
					continue
				}
				t.Run(path, func(t *testing.T) {
					if got := client.callRead(env.File, path); got != want {
						t.Errorf("got %q, want %q", got, want)
					}
				})
			}

			for path := range tc.wantByPath {
				found := false
				for _, p := range reportedPaths {
					if p == path {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("path %q not reported in envelope: excluded=%v truncated=%v",
						path, env.Excluded, env.Truncated)
				}
			}
		})
	}
}
