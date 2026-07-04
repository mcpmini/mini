package forge

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
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
	"TMPDIR":               true,
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

// Entries are never normalized — an invalid entry fails the call loudly so the
// user fixes their config rather than mini guessing intent.
func validateFileAllowList(entries []string, listName string) error {
	if len(entries) > maxGrantEntries {
		return &Error{Kind: KindRunner, Message: fmt.Sprintf(
			"too many %s entries: %d (max %d)", listName, len(entries), maxGrantEntries)}
	}
	if len(entries) == 0 {
		return nil
	}
	roots, err := allowedGrantRoots()
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if err := validateFileEntry(entry, listName, roots); err != nil {
			return err
		}
	}
	return nil
}

func validateFileEntry(entry, listName string, roots []string) error {
	if !filepath.IsAbs(entry) {
		return &Error{Kind: KindRunner, Message: fmt.Sprintf(
			"invalid %s entry %q: expected an absolute path, e.g. /Users/me/data", listName, entry)}
	}
	// A comma is a legal filename byte but Deno's --allow-read/--allow-write
	// parser splits its value on it, so one comma-bearing entry would silently
	// expand into multiple grants and slip a system path past the home boundary.
	if strings.ContainsRune(entry, ',') {
		return &Error{Kind: KindRunner, Message: fmt.Sprintf(
			"invalid %s entry %q: a comma is not allowed in a grant path", listName, entry)}
	}
	if clean := filepath.Clean(entry); entry != clean {
		return &Error{Kind: KindRunner, Message: fmt.Sprintf(
			"invalid %s entry %q: path is not clean, write it as %q", listName, entry, clean)}
	}
	if !withinAnyRoot(entry, roots) {
		return &Error{Kind: KindRunner, Message: fmt.Sprintf(
			"invalid %s entry %q: paths must be within your home directory or the system temp directory — code mode does not grant access to system paths", listName, entry)}
	}
	return nil
}

func allowedGrantRoots() ([]string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, &Error{Kind: KindRunner, Message: fmt.Sprintf("resolving home directory for file grant validation: %v", err)}
	}
	roots := []string{filepath.Clean(home), filepath.Clean(os.TempDir())}
	if resolved, err := filepath.EvalSymlinks(os.TempDir()); err == nil {
		if clean := filepath.Clean(resolved); clean != roots[1] {
			roots = append(roots, clean)
		}
	}
	return roots, nil
}

func withinAnyRoot(path string, roots []string) bool {
	for _, root := range roots {
		if path == root || strings.HasPrefix(path, root+string(filepath.Separator)) {
			return true
		}
	}
	return false
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
