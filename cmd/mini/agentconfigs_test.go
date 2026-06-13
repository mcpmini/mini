package main

import (
	"encoding/json"
	"testing"
)

func TestRenderMinimcpInstallJSON_usesConnectArg(t *testing.T) {
	var parsed struct {
		MCPServers struct {
			Mini struct {
				Command string   `json:"command"`
				Args    []string `json:"args"`
			} `json:"mini"`
		} `json:"mcpServers"`
	}
	if err := json.Unmarshal([]byte(renderMinimcpInstallJSON("/usr/local/bin/mini")), &parsed); err != nil {
		t.Fatalf("unmarshal install JSON: %v", err)
	}
	if got := parsed.MCPServers.Mini.Args; len(got) != 1 || got[0] != "connect" {
		t.Errorf("mini args = %v, want [connect]", got)
	}
}
