package config

import (
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/mcpmini/mini/internal/testutil"
)

func TestServerFile_aSavedProjectionKeepsTheLayoutMiniCreatedTheFileIn(t *testing.T) {
	dir := t.TempDir()
	sc := ServerConfig{Name: "svc", Command: "echo", Args: []string{"hi"}, Env: []string{"TOKEN=${TOKEN}"}}
	path, err := CreateServerFile(dir, sc)
	if err != nil {
		t.Fatal(err)
	}

	if _, saveErr := SaveServerProjection(
		ServerProjectionParams{
			ConfigDir:  dir,
			ServerName: "svc",
			Tool:       "list",
			Projection: &ProjectionConfig{Exclude: []string{"secret"}},
		},
	); saveErr != nil {
		t.Fatal(saveErr)
	}

	sc.Projections = map[string]*ProjectionConfig{"list": {Exclude: []string{"secret"}}}
	want, err := EncodeServerFile(sc)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(testutil.ReadFile(t, path)); got != string(want) {
		t.Errorf("saved file:\n%s\nwant the layout mini creates:\n%s", got, want)
	}
}

func TestReplaceServerKey(t *testing.T) {
	const file = "command: echo # kept\nprojections:\n    old: {}\nenv:\n    A: b\n"
	value := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "new"}
	cases := []struct {
		name, key string
		value     *yaml.Node
		want      string
	}{
		{
			name:  "replaces a key in place",
			key:   "projections",
			value: value,
			want:  "command: echo # kept\nprojections: new\nenv:\n    A: b\n",
		},
		{name: "adds a missing key at the end", key: "url", value: value, want: file + "url: new\n"},
		{name: "removes a key when value is nil", key: "projections", want: "command: echo # kept\nenv:\n    A: b\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ReplaceServerKey([]byte(file), tc.key, tc.value)
			if err != nil || string(got) != tc.want {
				t.Fatalf("ReplaceServerKey = %q, %v; want %q", got, err, tc.want)
			}
		})
	}
	t.Run("refuses a file that isn't one YAML mapping", func(t *testing.T) {
		for _, data := range []string{"- a\n", "command: echo\n---\ncommand: other\n"} {
			if _, err := ReplaceServerKey([]byte(data), "url", value); err == nil {
				t.Errorf("ReplaceServerKey(%q) succeeded, want an error", data)
			}
		}
	})
}
