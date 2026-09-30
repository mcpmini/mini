package catalog

import (
	"cmp"
	"encoding/json"
	"fmt"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	catalogdata "github.com/mcpmini/mini/catalog"
	"github.com/mcpmini/mini/internal/config"
)

type document struct {
	SchemaVersion int     `json:"schema_version"`
	Entries       []Entry `json:"entries"`
}

type Entry struct {
	Name        string `json:"name"`
	Title       string `json:"title"`
	URL         string `json:"url"`
	Description string `json:"description"`
	Category    string `json:"category"`
	Auth        string `json:"auth"`
	SetupURL    string `json:"setup_url,omitempty"`
}

const (
	AuthOAuth2    = "oauth2"
	AuthOAuth2App = "oauth2-app"
	AuthToken     = "token"
	AuthNone      = "none"
)

const (
	maxTextRunes  = 120
	maxTitleRunes = 40
)

var knownAuthValues = []string{AuthOAuth2, AuthOAuth2App, AuthToken, AuthNone}

// NeedsSetup reports whether the user must create credentials at SetupURL (an access
// token or their own OAuth app) before mini can connect.
func (e Entry) NeedsSetup() bool {
	return e.Auth == AuthToken || e.Auth == AuthOAuth2App
}

func Load() ([]Entry, error) {
	return parse(catalogdata.V1())
}

func parse(data []byte) ([]Entry, error) {
	doc, err := decode(data)
	if err != nil {
		return nil, err
	}
	return validateEntries(doc.Entries)
}

func decode(data []byte) (document, error) {
	var doc document
	if err := json.Unmarshal(data, &doc); err != nil {
		return document{}, fmt.Errorf("parse catalog: %w", err)
	}
	if doc.SchemaVersion != 1 {
		return document{}, fmt.Errorf("catalog schema_version must be 1")
	}
	return doc, nil
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
			return nil, fmt.Errorf("catalog entry %q: duplicate name", entry.Name)
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
	if err := cmp.Or(validateTitle(entry.Title), validateText("description", entry.Description), validateText("category", entry.Category)); err != nil {
		return err
	}
	if !slices.Contains(knownAuthValues, entry.Auth) {
		return fmt.Errorf("invalid auth %q", entry.Auth)
	}
	if err := validateHTTPSURL(entry.URL); err != nil {
		return err
	}
	return validateSetupURL(entry)
}

func validateSetupURL(entry Entry) error {
	if !entry.NeedsSetup() {
		if entry.SetupURL != "" {
			return fmt.Errorf("setup_url is only for %s and %s entries", AuthToken, AuthOAuth2App)
		}
		return nil
	}
	if err := validateHTTPSURL(entry.SetupURL); err != nil {
		return fmt.Errorf("setup_url: %w", err)
	}
	return nil
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

// Fetched catalog text reaches the terminal, so it must not hide, reorder, or push aside the host shown beside it.
func validateText(field, value string) error {
	if strings.TrimSpace(value) == "" {
		return fmt.Errorf("%s is required", field)
	}
	if utf8.RuneCountInString(value) > maxTextRunes {
		return fmt.Errorf("%s is longer than %d characters", field, maxTextRunes)
	}
	if hasIrregularSpacing(value) {
		return fmt.Errorf("%s has irregular spacing", field)
	}
	if strings.ContainsFunc(value, isHiddenOrControl) {
		return fmt.Errorf("%s contains control characters", field)
	}
	return nil
}

// The title is printed just before the entry's host; brackets could fake a host in front
// of the real one, and a long title could push it out of view.
func validateTitle(title string) error {
	if err := validateText("title", title); err != nil {
		return err
	}
	if utf8.RuneCountInString(title) > maxTitleRunes || strings.ContainsAny(title, "[]") {
		return fmt.Errorf("title must be at most %d characters without brackets", maxTitleRunes)
	}
	return nil
}

func hasIrregularSpacing(value string) bool {
	return strings.HasPrefix(value, " ") || strings.HasSuffix(value, " ") || strings.Contains(value, "  ") ||
		strings.ContainsFunc(value, func(r rune) bool { return unicode.IsSpace(r) && r != ' ' })
}

func entryLabel(entry Entry, index int) string {
	if entry.Name != "" {
		return strconv.Quote(entry.Name)
	}
	return fmt.Sprintf("%d", index+1)
}

func isHiddenOrControl(r rune) bool {
	return unicode.In(r, unicode.Cc, unicode.Cf)
}

func validateHTTPSURL(rawURL string) error {
	// Setup URLs are printed in credential instructions; hidden formatting could disguise where they lead.
	if strings.ContainsFunc(rawURL, func(r rune) bool { return r < 0x21 || r > 0x7e }) {
		return fmt.Errorf("url must be printable ASCII")
	}
	u, err := url.ParseRequestURI(rawURL)
	if err != nil || u.Scheme != "https" || u.Host == "" {
		return fmt.Errorf("url must be an https URL")
	}
	if strings.ContainsFunc(u.Host, func(r rune) bool { return r > unicode.MaxASCII }) {
		return fmt.Errorf("url host must be ASCII")
	}
	return nil
}
