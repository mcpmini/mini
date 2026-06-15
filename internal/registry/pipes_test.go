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

func TestAddPipes_StepWrappingHiddenToolIsProtected(t *testing.T) {
	r := registry.New()
	perm := &config.PermissionsConfig{Hidden: []string{"admin_reset"}}
	r.AddServer("gh", []transport.ToolDefinition{
		{Name: "admin_reset"},
	}, perm)
	r.AddPipes([]config.PipeConfig{
		{
			Name:  "wraps_hidden",
			Steps: []config.StepConfig{{ID: "step1", Server: "gh", Tool: "admin_reset"}},
		},
	}, r.PermLookup)

	e, err := r.Lookup("user.wraps_hidden")
	if err != nil {
		t.Fatal(err)
	}
	if e.Permission != config.PermProtected {
		t.Errorf("pipe wrapping a hidden tool: permission = %q, want protected", e.Permission)
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

func TestAddPipes_ExplicitProtectedPermission(t *testing.T) {
	r := registry.New()
	r.AddPipes([]config.PipeConfig{
		{
			Name:       "protected_pipe",
			Permission: "protected",
			Steps:      []config.StepConfig{{ID: "s", Server: "gh", Tool: "t"}},
		},
	}, noopLookup)

	e, err := r.Lookup("user.protected_pipe")
	if err != nil {
		t.Fatal(err)
	}
	if e.Permission != config.PermProtected {
		t.Errorf("expected protected, got %s", e.Permission)
	}
}

func TestAddPipes_ExplicitOpenCannotDowngradeProtectedStep(t *testing.T) {
	r := registry.New()
	perm := &config.PermissionsConfig{Protected: []string{"delete_branch"}}
	r.AddServer("gh", []transport.ToolDefinition{
		{Name: "delete_branch"},
	}, perm)
	r.AddPipes([]config.PipeConfig{
		{
			Name:       "open_pipe",
			Permission: "open",
			Steps:      []config.StepConfig{{ID: "s", Server: "gh", Tool: "delete_branch"}},
		},
	}, r.PermLookup)

	e, err := r.Lookup("user.open_pipe")
	if err != nil {
		t.Fatal(err)
	}
	if e.Permission != config.PermProtected {
		t.Errorf("explicit open with protected step: permission = %q, want protected", e.Permission)
	}
}

func TestAddPipes_Idempotent_Replace(t *testing.T) {
	r := registry.New()
	r.AddPipes([]config.PipeConfig{
		{Name: "my_pipe", Description: "v1", Steps: []config.StepConfig{{ID: "s", Server: "gh", Tool: "t"}}},
	}, noopLookup)
	r.AddPipes([]config.PipeConfig{
		{Name: "my_pipe", Description: "v2", Steps: []config.StepConfig{{ID: "s", Server: "gh", Tool: "t"}}},
	}, noopLookup)

	e, err := r.Lookup("user.my_pipe")
	if err != nil {
		t.Fatal(err)
	}
	if e.Description != "v2" {
		t.Errorf("expected description v2, got %q", e.Description)
	}
}

func TestAddPipes_HiddenPermission(t *testing.T) {
	r := registry.New()
	r.AddPipes([]config.PipeConfig{
		{
			Name:       "hidden_pipe",
			Permission: "hidden",
			Steps:      []config.StepConfig{{ID: "s", Server: "gh", Tool: "t"}},
		},
	}, noopLookup)

	if _, err := r.Lookup("user.hidden_pipe"); err == nil {
		t.Error("expected hidden pipe to not be lookupable")
	}
	all := r.All()
	for _, ce := range all {
		if ce.Name == "user.hidden_pipe" {
			t.Error("hidden pipe should not appear in All()")
		}
	}
}

func TestAddPipes_InputSchemaBuilt(t *testing.T) {
	r := registry.New()
	r.AddPipes([]config.PipeConfig{
		{
			Name: "input_pipe",
			Inputs: map[string]config.InputSchema{
				"title": {Type: "string", Required: true},
			},
			Steps: []config.StepConfig{{ID: "s", Server: "gh", Tool: "t"}},
		},
	}, noopLookup)

	e, err := r.Lookup("user.input_pipe")
	if err != nil {
		t.Fatal(err)
	}
	if len(e.InputSchema) == 0 {
		t.Error("expected non-empty InputSchema for pipe with inputs")
	}
}

func TestAddPipes_AppearsInAll(t *testing.T) {
	r := registry.New()
	r.AddPipes([]config.PipeConfig{
		{Name: "my_pipe", Description: "a pipe", Steps: []config.StepConfig{{ID: "s", Server: "gh", Tool: "t"}}},
	}, noopLookup)

	e, err := r.Lookup("user.my_pipe")
	if err != nil {
		t.Fatalf("pipe not found via Lookup: %v", err)
	}
	if e.Pipe == nil {
		t.Error("pipe entry should have non-nil Pipe field")
	}

	found := false
	for _, ce := range r.All() {
		if ce.Name == "user.my_pipe" {
			found = true
		}
	}
	if !found {
		t.Error("pipe did not appear in All()")
	}
}
