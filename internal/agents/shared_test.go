package agents

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/testutil"
)

func tempDir(t *testing.T) string {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("EvalSymlinks: %v", err)
	}
	return dir
}

func writeClientConfig(t *testing.T, name, content string) string {
	t.Helper()
	path := filepath.Join(tempDir(t), name)
	testutil.WriteFile(t, path, content)
	return path
}

type readerFunc func(string) (map[string]Server, error)

type readerCase struct {
	name   string
	file   string
	config string
	want   Server
}

func runReaderCases(t *testing.T, read readerFunc, cases []readerCase) {
	t.Helper()
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			got, err := read(writeClientConfig(t, tt.file, tt.config))
			if err != nil {
				t.Fatalf("read: %v", err)
			}
			if want := map[string]Server{"s": tt.want}; !reflect.DeepEqual(got, want) {
				t.Errorf("got  %#v\nwant %#v", got, want)
			}
		})
	}
}

func stdio(name, command string, args ...string) config.ServerConfig {
	return config.ServerConfig{Name: name, Command: command, Args: args}
}

func remote(name, url string, headers map[string]string) config.ServerConfig {
	return config.ServerConfig{Name: name, Transport: "http", URL: url, Headers: headers}
}

func TestServerCandidate(t *testing.T) {
	for _, tt := range []struct {
		name   string
		server Server
		want   bool
	}{
		{"plain", Server{}, true},
		{"switched off is still a candidate", Server{Disabled: true}, true},
		{"ignored settings are still a candidate", Server{IgnoredRunSettings: []string{"cwd"}}, true},
		{"unsupported reference", Server{UnexpandableRefs: []string{"an environment variable in url"}}, false},
	} {
		if got := tt.server.Candidate(); got != tt.want {
			t.Errorf("%s: Candidate() = %v, want %v", tt.name, got, tt.want)
		}
	}
}

func TestReadConfigFile(t *testing.T) {
	t.Run("file not found returns error", func(t *testing.T) {
		_, err := ReadConfigFile("/nonexistent/path/file.json")
		if err == nil {
			t.Fatal("expected error for missing file")
		}
	})

	t.Run("happy path returns contents", func(t *testing.T) {
		dir := tempDir(t)
		f := filepath.Join(dir, "test.json")
		want := []byte(`{"hello":"world"}`)
		testutil.WriteFileBytes(t, f, want)

		got, err := ReadConfigFile(f)
		if err != nil {
			t.Fatalf("ReadConfigFile: %v", err)
		}
		if string(got) != string(want) {
			t.Errorf("got %q, want %q", got, want)
		}
	})

	t.Run("file too large returns error", func(t *testing.T) {
		dir := tempDir(t)
		f := filepath.Join(dir, "big.json")
		big := make([]byte, maxImportConfigBytes+1)
		testutil.WriteFileBytes(t, f, big)

		_, err := ReadConfigFile(f)
		if err == nil {
			t.Fatal("expected error for oversized file")
		}
		if !strings.Contains(err.Error(), "too large") {
			t.Errorf("error = %q, want 'too large'", err.Error())
		}
	})

	t.Run("file at the limit is returned whole", func(t *testing.T) {
		f := filepath.Join(tempDir(t), "limit.json")
		testutil.WriteFileBytes(t, f, make([]byte, maxImportConfigBytes))

		got, err := ReadConfigFile(f)
		if err != nil {
			t.Fatalf("ReadConfigFile: %v", err)
		}
		if len(got) != maxImportConfigBytes {
			t.Errorf("read %d bytes, want %d", len(got), maxImportConfigBytes)
		}
	})
}
