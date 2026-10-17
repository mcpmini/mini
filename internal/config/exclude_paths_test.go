package config_test

import (
	"strings"
	"testing"

	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/config/configtest"
)

func TestLoadServerRejectsUnsupportedExcludePaths(t *testing.T) {
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
	for _, path := range paths {
		t.Run(path, func(t *testing.T) {
			dir := t.TempDir()
			configtest.WriteServer(t, dir, config.ServerConfig{
				Name: "svc", Command: "echo", Projections: map[string]*config.ProjectionConfig{
					"tool": {Exclude: []string{"valid", path}}, "other": {Exclude: []string{"secret"}},
				},
			})
			sc, err := config.LoadServer(dir, "svc")
			if err != nil || sc.Command != "echo" {
				t.Fatalf("server must remain loadable: %+v, %v", sc, err)
			}
			if sc.ProjectionsErr == nil || !strings.Contains(sc.ProjectionsErr.Err.Error(), "invalid exclude path") {
				t.Fatalf("projection error = %v, want actionable exclude path error", sc.ProjectionsErr)
			}
			if sc.Projections != nil {
				t.Errorf("bad projections must be discarded together: %v", sc.Projections)
			}
		})
	}
}

func TestLoadServerAcceptsRootRelativeExcludePaths(t *testing.T) {
	dir := t.TempDir()
	configtest.WriteServer(t, dir, config.ServerConfig{
		Name: "svc", Command: "echo", Projections: map[string]*config.ProjectionConfig{
			"nil":   nil,
			"empty": {},
			"paths": {
				Exclude: []string{
					"description",
					"head.repo.description",
					"items",
					"data.items",
					"0.name",
					"_meta.field-1",
				},
			},
		},
	})
	sc, err := config.LoadServer(dir, "svc")
	if err != nil || sc.ProjectionsErr != nil || len(sc.Projections) != 3 {
		t.Fatalf("LoadServer = %+v, %v; want all supported projections", sc, err)
	}
}
