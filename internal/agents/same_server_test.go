package agents

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/mcpmini/mini/internal/config"
)

func TestIsMiniEntry(t *testing.T) {
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(tempDir(t), "mini-link")
	if err := os.Symlink(self, link); err != nil {
		t.Fatalf("symlink: %v", err)
	}
	for _, tt := range []struct {
		name  string
		entry config.ServerConfig
		want  bool
	}{
		{"the running binary", config.ServerConfig{Command: self}, true},
		{"a symlink to the running binary", config.ServerConfig{Command: link}, true},
		{"another mini binary running connect", config.ServerConfig{Command: "/opt/mini/bin/mini", Args: []string{"--config", "/x", "connect"}}, true},
		{"another mini binary running serve", config.ServerConfig{Command: "mini.exe", Args: []string{"serve"}}, true},
		{"a mini binary running something else", config.ServerConfig{Command: "/opt/mini/bin/mini", Args: []string{"ls"}}, false},
		{"a similar name", config.ServerConfig{Command: "minify", Args: []string{"connect"}}, false},
		{"an unrelated binary", config.ServerConfig{Command: "/usr/bin/env"}, false},
		{"an http entry", config.ServerConfig{Transport: "http", URL: "https://example.com/mcp"}, false},
	} {
		if got := IsMiniEntry(tt.entry, self); got != tt.want {
			t.Errorf("%s: IsMiniEntry = %v, want %v", tt.name, got, tt.want)
		}
	}
}

func TestConnectionDifferences(t *testing.T) {
	base := config.ServerConfig{
		Name: "a", Transport: "http", URL: "https://example.com/mcp",
		Env: []string{"A=1", "B=2"}, Headers: map[string]string{"Authorization": "Bearer ${A}"},
	}
	t.Run("name and order of env don't matter", func(t *testing.T) {
		other := base
		other.Name, other.Env = "b", []string{"B=2", "A=1"}
		if got := ConnectionDifferences(base, other); got != nil {
			t.Errorf("differences = %v, want none", got)
		}
	})
	t.Run("an empty transport is stdio", func(t *testing.T) {
		if got := ConnectionDifferences(config.ServerConfig{Command: "run"}, config.ServerConfig{Transport: "stdio", Command: "run"}); got != nil {
			t.Errorf("differences = %v, want none", got)
		}
	})
	t.Run("same url with other credentials is another server", func(t *testing.T) {
		other := base
		other.Headers = map[string]string{"Authorization": "Bearer ${B}"}
		if got := ConnectionDifferences(base, other); !reflect.DeepEqual(got, []string{"headers"}) {
			t.Errorf("differences = %v, want [headers]", got)
		}
	})
}
