package catalog

import (
	_ "embed"
	"fmt"
	"net/url"
	"strings"
	"unicode"

	"gopkg.in/yaml.v3"

	"github.com/mcpmini/mini/internal/config"
)

//go:embed catalog.yaml
var embedded []byte

// Same Pages origin and /mini/ project prefix as internal/auth.ClientMetadataURL —
// do not derive this path at runtime or move it to the site root.
const CatalogURL = "https://mcpmini.github.io/mini/catalog/v1.json"

type document struct {
	SchemaVersion int     `yaml:"schema_version"`
	Entries       []Entry `yaml:"entries"`
}

type Entry struct {
	Name        string `yaml:"name"        json:"name"`
	URL         string `yaml:"url"         json:"url"`
	Description string `yaml:"description" json:"description"`
	Category    string `yaml:"category"    json:"category"`
	Auth        string `yaml:"auth"        json:"auth"`
	SetupURL    string `yaml:"setup_url"   json:"setup_url,omitempty"`
}

const (
	AuthOAuth2    = "oauth2"
	AuthOAuth2App = "oauth2-app"
	AuthToken     = "token"
	AuthNone      = "none"
)

// NeedsManualSetup reports whether the entry requires the user to provide
// credentials (token or client_id) before the server can be used.
func (e Entry) NeedsManualSetup() bool {
	return e.Auth == AuthToken || e.Auth == AuthOAuth2App
}

// IsOAuth2 reports whether the entry uses managed OAuth2 (mini handles the
// full PKCE flow without requiring the user to register an app first).
func (e Entry) IsOAuth2() bool {
	return e.Auth == AuthOAuth2
}

func Load() ([]Entry, error) {
	return parse(embedded)
}

func parse(data []byte) ([]Entry, error) {
	var doc document
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("parse catalog: %w", err)
	}
	if doc.SchemaVersion != 1 {
		return nil, fmt.Errorf("catalog schema_version must be 1")
	}
	return validateEntries(doc.Entries)
}

func validateEntries(entries []Entry) ([]Entry, error) {
	if len(entries) == 0 {
		return nil, fmt.Errorf("catalog entries are required")
	}
	seen := make(map[string]bool)
	for i, entry := range entries {
		if err := validateEntry(entry); err != nil {
			return nil, fmt.Errorf("catalog entry %s: %w", entryLabel(entry, i), err)
		}
		if seen[entry.Name] {
			return nil, fmt.Errorf("catalog: duplicate entry name %q", entry.Name)
		}
		seen[entry.Name] = true
	}
	return entries, nil
}

var validAuthValues = map[string]bool{
	AuthOAuth2: true, AuthOAuth2App: true, AuthToken: true, AuthNone: true,
}

func validateEntry(entry Entry) error {
	if err := validateEntryBase(entry); err != nil {
		return err
	}
	return validateAuth(entry)
}

func validateEntryBase(entry Entry) error {
	if entry.Name == "" {
		return fmt.Errorf("name is required")
	}
	if !config.ValidServerName.MatchString(entry.Name) {
		return fmt.Errorf("invalid name %q", entry.Name)
	}
	if entry.URL == "" {
		return fmt.Errorf("url is required")
	}
	if strings.TrimSpace(entry.Description) == "" {
		return fmt.Errorf("description is required")
	}
	if strings.TrimSpace(entry.Category) == "" {
		return fmt.Errorf("category is required")
	}
	if err := rejectControlFields(entry); err != nil {
		return err
	}
	return validateHTTPSURL(entry.URL)
}

func rejectControlFields(entry Entry) error {
	if containsControlRune(entry.Description) {
		return fmt.Errorf("description contains invalid control characters")
	}
	if containsControlRune(entry.Category) {
		return fmt.Errorf("category contains invalid control characters")
	}
	if containsControlRune(entry.URL) {
		return fmt.Errorf("url contains invalid control characters")
	}
	return nil
}

func containsControlRune(s string) bool {
	for _, r := range s {
		if unicode.IsControl(r) {
			return true
		}
	}
	return false
}

func validateAuth(entry Entry) error {
	if entry.Auth == "" {
		return fmt.Errorf("auth is required")
	}
	if !validAuthValues[entry.Auth] {
		return fmt.Errorf("invalid auth %q", entry.Auth)
	}
	return validateSetupURL(entry.Auth, entry.SetupURL)
}

func validateSetupURL(auth, setupURL string) error {
	needs := auth == AuthToken || auth == AuthOAuth2App
	if needs && setupURL == "" {
		return fmt.Errorf("setup_url is required for auth %q", auth)
	}
	if !needs && setupURL != "" {
		return fmt.Errorf("setup_url not allowed for auth %q", auth)
	}
	if setupURL != "" {
		if containsControlRune(setupURL) {
			return fmt.Errorf("setup_url contains invalid control characters")
		}
		return validateHTTPSURL(setupURL)
	}
	return nil
}

func entryLabel(entry Entry, index int) string {
	if entry.Name != "" {
		return entry.Name
	}
	return fmt.Sprintf("%d", index+1)
}

func validateHTTPSURL(rawURL string) error {
	u, err := url.ParseRequestURI(rawURL)
	if err != nil || u.Scheme != "https" || u.Host == "" {
		return fmt.Errorf("url must be an https URL")
	}
	return nil
}
