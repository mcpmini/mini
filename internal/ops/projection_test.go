package ops_test

import (
	"reflect"
	"testing"

	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/ops"
)

func TestAddServerStoresBundledProjectionsInline(t *testing.T) {
	dir := tempDir(t)
	added, err := ops.AddServer(dir, config.ServerConfig{Name: "my-github", URL: "https://api.github.com/mcp"})
	if err != nil {
		t.Fatal(err)
	}
	if !added.DefaultProjections {
		t.Fatal("default projections were not reported")
	}
	loaded, err := config.LoadServer(dir, "my-github")
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.Projections) == 0 {
		t.Fatal("server file has no bundled projections")
	}
}

func TestAddServerKeepsExplicitProjectionMaps(t *testing.T) {
	for name, projections := range map[string]map[string]*config.ProjectionConfig{
		"explicit":       {"custom": {Exclude: []string{"secret"}}},
		"explicit empty": {},
	} {
		t.Run(name, func(t *testing.T) {
			dir := tempDir(t)
			want := projections
			added, err := ops.AddServer(dir, config.ServerConfig{Name: "my-github", URL: "https://api.github.com/mcp", Projections: want})
			if err != nil {
				t.Fatal(err)
			}
			if added.DefaultProjections {
				t.Fatal("explicit projection map was replaced by bundled defaults")
			}
			loaded, err := config.LoadServer(dir, "my-github")
			if err != nil {
				t.Fatal(err)
			}
			if len(loaded.Projections)+len(want) > 0 && !reflect.DeepEqual(loaded.Projections, want) {
				t.Fatalf("loaded projections = %#v, want %#v", loaded.Projections, want)
			}
		})
	}
}
