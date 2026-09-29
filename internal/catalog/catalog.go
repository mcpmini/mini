package catalog

import (
	"encoding/json"
	"fmt"
	"net/url"
	"slices"
	"strings"

	catalogdata "github.com/mcpmini/mini/catalog"
	"github.com/mcpmini/mini/internal/config"
)

type document struct {
	SchemaVersion int     `json:"schema_version"`
	Entries       []Entry `json:"entries"`
}

type Entry struct {
	Name        string `json:"name"`
	URL         string `json:"url"`
	Description string `json:"description"`
	Category    string `json:"category"`
	Auth        string `json:"auth"`
}

const (
	AuthOAuth2    = "oauth2"
	AuthOAuth2App = "oauth2-app"
	AuthToken     = "token"
	AuthNone      = "none"
)

func Load() ([]Entry, error) {
	return parse(catalogdata.V1())
}

func parse(data []byte) ([]Entry, error) {
	var doc document
	if err := json.Unmarshal(data, &doc); err != nil {
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
	seen := make(map[string]bool, len(entries))
	for i, entry := range entries {
		if err := validateEntry(entry); err != nil {
			return nil, fmt.Errorf("catalog entry %s: %w", entryLabel(entry, i), err)
		}
		if seen[entry.Name] {
			return nil, fmt.Errorf("catalog entry %s: duplicate name", entry.Name)
		}
		seen[entry.Name] = true
	}
	return entries, nil
}

func validateEntry(entry Entry) error {
	if err := validateName(entry.Name); err != nil {
		return err
	}
	if entry.URL == "" {
		return fmt.Errorf("url is required")
	}
	if strings.TrimSpace(entry.Description) == "" || strings.TrimSpace(entry.Category) == "" {
		return fmt.Errorf("description and category are required")
	}
	if !slices.Contains([]string{AuthOAuth2, AuthOAuth2App, AuthToken, AuthNone}, entry.Auth) {
		return fmt.Errorf("invalid auth %q", entry.Auth)
	}
	return validateHTTPSURL(entry.URL)
}

func validateName(name string) error {
	if name == "" {
		return fmt.Errorf("name is required")
	}
	if !config.ValidServerName.MatchString(name) {
		return fmt.Errorf("invalid name %q", name)
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
