package configtest

import (
	"reflect"
	"testing"

	"github.com/mcpmini/mini/internal/config"
)

func TestWriteProjectionsRoundTripsThroughLoader(t *testing.T) {
	dir := t.TempDir()
	WriteServer(t, dir, config.ServerConfig{Name: "svc", Command: "echo"})
	want := map[string]*config.ProjectionConfig{
		"*":    {Exclude: []string{"secret"}},
		"list": {IncludeOnly: []string{"title"}, StringLimits: map[string]int{"body": 0}},
	}
	WriteProjections(t, dir, ProjectionFile{ServerName: "svc", Tools: want})
	if got := loadedProjections(t, dir); !reflect.DeepEqual(got, want) {
		t.Fatalf("loaded projections = %#v, want %#v", got, want)
	}
}

func TestWriteProjectionsReplacesExistingIncludingEmptyMap(t *testing.T) {
	dir := t.TempDir()
	WriteServer(t, dir, config.ServerConfig{Name: "svc", Command: "echo"})
	WriteProjections(t, dir, ProjectionFile{
		ServerName: "svc",
		Tools: map[string]*config.ProjectionConfig{"old": {
			Exclude: []string{"secret"},
		}},
	})
	want := map[string]*config.ProjectionConfig{"new": {IncludeOnly: []string{"id"}}}
	WriteProjections(t, dir, ProjectionFile{ServerName: "svc", Tools: want})
	if got := loadedProjections(t, dir); !reflect.DeepEqual(got, want) {
		t.Fatalf("replacement projections = %#v, want %#v", got, want)
	}
	WriteProjections(t, dir, ProjectionFile{ServerName: "svc", Tools: map[string]*config.ProjectionConfig{}})
	if got := loadedProjections(t, dir); len(got) != 0 {
		t.Fatalf("empty replacement kept rules: %#v", got)
	}
}

func loadedProjections(t *testing.T, dir string) map[string]*config.ProjectionConfig {
	t.Helper()
	server, err := config.LoadServer(dir, "svc")
	if err != nil {
		t.Fatalf("LoadServer: %v", err)
	}
	return server.Projections
}
