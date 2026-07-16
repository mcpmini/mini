package catalog

import (
	_ "embed"
	"fmt"
	"net/url"
	"strings"

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
	for i, entry := range entries {
		if err := validateEntry(entry); err != nil {
			return nil, fmt.Errorf("catalog entry %s: %w", entryLabel(entry, i), err)
		}
	}
	return entries, nil
}

var validAuthValues = map[string]bool{
	"oauth2": true, "oauth2-app": true, "token": true, "none": true,
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
	return validateHTTPSURL(entry.URL)
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
	needs := auth == "token" || auth == "oauth2-app"
	if needs && setupURL == "" {
		return fmt.Errorf("setup_url is required for auth %q", auth)
	}
	if !needs && setupURL != "" {
		return fmt.Errorf("setup_url not allowed for auth %q", auth)
	}
	if setupURL != "" {
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
