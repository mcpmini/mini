package main

import (
	"fmt"
	"strings"
)

type scenarioSet struct {
	Name      string
	Mode      string
	Version   string
	Scenarios []string
}

var scenarioSets = []scenarioSet{
	{Name: "agent", Mode: "agent", Version: "2026-07-28", Scenarios: []string{
		"tools_call", "request-metadata", "http-standard-headers", "http-custom-headers",
		"http-invalid-tool-headers", "json-schema-ref-no-deref", "json-schema-2020-12-preservation",
		"sep-2322-client-request-state",
	}},
	{Name: "cli", Mode: "cli", Version: "2026-07-28", Scenarios: []string{
		"tools_call", "request-metadata", "http-standard-headers", "http-custom-headers",
		"http-invalid-tool-headers", "json-schema-ref-no-deref",
	}},
	{Name: "legacy", Mode: "agent", Version: "2025-11-25", Scenarios: []string{
		"initialize", "tools_call", "json-schema-2020-12-preservation",
	}},
	{Name: "auth", Mode: "cli", Version: "2026-07-28", Scenarios: []string{
		"auth/metadata-default", "auth/metadata-var1", "auth/metadata-var2", "auth/metadata-var3",
		"auth/basic-cimd", "auth/scope-from-www-authenticate", "auth/scope-from-scopes-supported",
		"auth/scope-omitted-when-undefined", "auth/scope-step-up", "auth/scope-retry-limit",
		"auth/token-endpoint-auth-basic", "auth/token-endpoint-auth-post", "auth/token-endpoint-auth-none",
		"auth/pre-registration", "auth/resource-mismatch", "auth/offline-access-scope",
		"auth/offline-access-not-supported", "auth/authorization-server-migration", "auth/iss-supported",
		"auth/iss-not-advertised", "auth/iss-supported-missing", "auth/iss-wrong-issuer",
		"auth/iss-unexpected", "auth/iss-normalized", "auth/metadata-issuer-mismatch",
	}},
}

// auth is opt-in until mini can log in to localhost servers, where every auth mock runs (#606).
var defaultSets = []string{"agent", "cli", "legacy"}

func selectSets(args []string) ([]scenarioSet, error) {
	names := defaultSets
	if len(args) > 1 {
		return nil, fmt.Errorf("usage: conformancerun [%s]", strings.Join(setNames(), "|"))
	}
	if len(args) == 1 {
		names = args
	}
	var selected []scenarioSet
	for _, name := range names {
		set, ok := findSet(name)
		if !ok {
			return nil, fmt.Errorf("unknown set %q; want one of %s", name, strings.Join(setNames(), ", "))
		}
		selected = append(selected, set)
	}
	return selected, nil
}

func findSet(name string) (scenarioSet, bool) {
	for _, set := range scenarioSets {
		if set.Name == name {
			return set, true
		}
	}
	return scenarioSet{}, false
}

func setNames() []string {
	names := make([]string, 0, len(scenarioSets))
	for _, set := range scenarioSets {
		names = append(names, set.Name)
	}
	return names
}
