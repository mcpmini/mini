package forge

import (
	"fmt"
	"regexp"
)

const maxGrantEntries = 32

// Bare "*" is deliberately not matched here: unrestricted network access
// requires the explicit dangerous_allow_any_url escape hatch, not an
// allowlist entry, so it gets its own validation error (see below).
var netEntryPattern = regexp.MustCompile(`^(\*\.[a-zA-Z0-9]([a-zA-Z0-9.-]*[a-zA-Z0-9])?|[a-zA-Z0-9]([a-zA-Z0-9.-]*[a-zA-Z0-9])?)(:\d{1,5})?$`)

var envNamePattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// reservedEnvNames are set by the runner itself (see childEnv); a config
// grant must not clobber them.
var reservedEnvNames = map[string]bool{
	"PATH":                 true,
	"HOME":                 true,
	"DENO_DIR":             true,
	"DENO_NO_UPDATE_CHECK": true,
	"NO_COLOR":             true,
}

// Entries are never normalized or rewritten — an invalid entry fails the call
// loudly so the user fixes their config, rather than mini guessing intent.
func validateNetAllowList(entries []string) error {
	if len(entries) > maxGrantEntries {
		return &Error{Kind: KindRunner, Message: fmt.Sprintf(
			"too many code_mode.url_allow_list entries: %d (max %d)", len(entries), maxGrantEntries)}
	}
	for _, entry := range entries {
		if entry == "*" {
			return &Error{Kind: KindRunner, Message: fmt.Sprintf(
				"invalid code_mode.url_allow_list entry %q: unrestricted network access requires dangerous_allow_any_url: true", entry)}
		}
		if !netEntryPattern.MatchString(entry) {
			return &Error{Kind: KindRunner, Message: fmt.Sprintf(
				"invalid code_mode.url_allow_list entry %q: expected host or host:port, e.g. api.github.com", entry)}
		}
	}
	return nil
}

func validateEnvAllowList(names []string) error {
	if len(names) > maxGrantEntries {
		return &Error{Kind: KindRunner, Message: fmt.Sprintf(
			"too many code_mode.env_var_allow_list entries: %d (max %d)", len(names), maxGrantEntries)}
	}
	for _, name := range names {
		if err := validateEnvName(name); err != nil {
			return err
		}
	}
	return nil
}

func validateEnvName(name string) error {
	if reservedEnvNames[name] {
		return &Error{Kind: KindRunner, Message: fmt.Sprintf(
			"invalid code_mode.env_var_allow_list entry %q: reserved for the runner, choose another name", name)}
	}
	if !envNamePattern.MatchString(name) {
		return &Error{Kind: KindRunner, Message: fmt.Sprintf(
			"invalid code_mode.env_var_allow_list entry %q: expected a valid env var name, e.g. GITHUB_TOKEN", name)}
	}
	return nil
}
