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
		"auth/metadata-default", "auth/metadata-var1", "auth/basic-cimd",
		"auth/scope-from-scopes-supported", "auth/scope-omitted-when-undefined", "auth/scope-retry-limit",
		"auth/token-endpoint-auth-basic", "auth/token-endpoint-auth-post", "auth/token-endpoint-auth-none",
		"auth/pre-registration",
		"auth/offline-access-scope", "auth/offline-access-not-supported", "auth/authorization-server-migration",
		"auth/iss-supported", "auth/iss-not-advertised", "auth/iss-supported-missing",
		"auth/iss-wrong-issuer", "auth/iss-unexpected", "auth/iss-normalized", "auth/metadata-issuer-mismatch",
	}},
	{Name: "auth-pending", Mode: "cli", Version: "2026-07-28", Scenarios: []string{
		"auth/metadata-var2", "auth/metadata-var3", "auth/scope-from-www-authenticate",
		"auth/scope-step-up", "auth/resource-mismatch",
	}},
}

// requiredChecks must succeed in these scenarios, which otherwise pass with the checks they exist for skipped.
var requiredChecks = map[string][]check{
	"http-standard-headers": {
		{ID: "sep-2243-client-includes-standard-headers", Name: "ClientMcpMethodHeader_tools_call"},
		{ID: "sep-2243-client-includes-standard-headers", Name: "ClientMcpNameHeader_tools_call"},
	},
	"request-metadata": {
		{ID: "sep-2575-http-client-sends-version-header"},
		{ID: "sep-2575-client-populates-meta"},
		{ID: "sep-2575-client-sends-client-info"},
		{ID: "sep-2575-http-version-header-matches-meta"},
		{ID: "sep-2575-client-retry-supported-version"},
	},
}

var defaultSets = []string{"agent", "cli", "legacy", "auth"}

func selectSets(args []string) ([]scenarioSet, error) {
	names := defaultSets
	if len(args) > 1 {
		return nil, fmt.Errorf("usage: go run ./test/conformance/runner [%s]", strings.Join(setNames(), "|"))
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
