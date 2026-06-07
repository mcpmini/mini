//go:build test

package registry_test

import (
	"testing"

	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/registry"
	"github.com/mcpmini/mini/internal/transport"
)

func noopLookup(_, _ string) (config.PermissionLevel, bool) {
	return config.PermOpen, true
}

func TestAddPipes_RegistersUnderUserServer(t *testing.T) {
	r := registry.New()
	pipes := []config.PipeConfig{
		{Name: "my_pipe", Description: "test pipe", Steps: []config.StepConfig{
			{ID: "step1", Server: "gh", Tool: "create_pr"},
		}},
	}
	r.AddPipes(pipes, noopLookup)
	e, err := r.Lookup("user.my_pipe")
	if err != nil {
		t.Fatalf("expected pipe to be in registry: %v", err)
	}
	if e.Server != config.UserServerName {
		t.Errorf("server = %q, want %q", e.Server, config.UserServerName)
	}
	if e.Pipe == nil {
		t.Error("expected Pipe field to be set")
	}
}

func TestAddPipes_PermissionInheritsFromSteps(t *testing.T) {
	r := registry.New()
	perm := &config.PermissionsConfig{Protected: []string{"delete_branch"}}
	r.AddServer("gh", []transport.ToolDefinition{
		{Name: "create_pr"},
		{Name: "delete_branch"},
	}, perm)
	pipes := []config.PipeConfig{
		{
			Name: "safe_pipe",
			Steps: []config.StepConfig{
				{ID: "step1", Server: "gh", Tool: "create_pr"},
			},
		},
		{
			Name: "danger_pipe",
			Steps: []config.StepConfig{
				{ID: "step1", Server: "gh", Tool: "delete_branch"},
			},
		},
	}
	r.AddPipes(pipes, r.PermLookup)
	safe, _ := r.Lookup("user.safe_pipe")
	if safe.Permission != config.PermOpen {
		t.Errorf("safe_pipe permission = %q, want open", safe.Permission)
	}
	danger, _ := r.Lookup("user.danger_pipe")
	if danger.Permission != config.PermProtected {
		t.Errorf("danger_pipe permission = %q, want protected", danger.Permission)
	}
}

func TestAddPipes_UnknownServerDefaultsToProtected(t *testing.T) {
	r := registry.New()
	pipes := []config.PipeConfig{
		{
			Name: "unknown_pipe",
			Steps: []config.StepConfig{
				{ID: "step1", Server: "not_connected", Tool: "some_tool"},
			},
		},
	}
	r.AddPipes(pipes, r.PermLookup)
	e, _ := r.Lookup("user.unknown_pipe")
	if e.Permission != config.PermProtected {
		t.Errorf("unknown server pipe permission = %q, want protected", e.Permission)
	}
}

func TestAddServer_RejectsReservedName(t *testing.T) {
	r := registry.New()
	r.AddServer(config.UserServerName, []transport.ToolDefinition{
		{Name: "some_tool"},
	}, nil)
	all := r.All()
	for _, e := range all {
		if e.Server == config.UserServerName && e.Name == "user.some_tool" {
			t.Error("reserved server name 'user' should not be added via AddServer")
		}
	}
	_, err := r.Lookup("user.some_tool")
	if err == nil {
		t.Error("tool under reserved server name should not be discoverable")
	}
}
