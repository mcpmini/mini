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

const maxTextRunes = 120

var knownAuthValues = []string{AuthOAuth2, AuthOAuth2App, AuthToken, AuthNone}

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
	if err := cmp.Or(validateText("description", entry.Description), validateText("category", entry.Category)); err != nil {
		return err
	}
	if !slices.Contains(knownAuthValues, entry.Auth) {
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

// Catalog text is printed straight to the user's terminal; a fetched catalog must not
// be able to smuggle in escape sequences (Cc) or reorder or hide text (Cf: bidi
// overrides, zero-width characters) around the host shown for each entry. Length and
// spacing limits keep free text from pushing that host out of view.
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

func hasIrregularSpacing(value string) bool {
	return strings.HasPrefix(value, " ") || strings.HasSuffix(value, " ") || strings.Contains(value, "  ") ||
		strings.ContainsFunc(value, func(r rune) bool { return unicode.IsSpace(r) && r != ' ' })
}

// Quoted: errors reach the terminal, and an invalid name may hold escape sequences.
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
	u, err := url.ParseRequestURI(rawURL)
	if err != nil || u.Scheme != "https" || u.Host == "" {
		return fmt.Errorf("url must be an https URL")
	}
	// The listing shows each entry's host as the user's check on where it points, and a
	// non-ASCII host can pass for a familiar one (a Cyrillic і in github.com).
	if strings.ContainsFunc(u.Host, func(r rune) bool { return r > unicode.MaxASCII }) {
		return fmt.Errorf("url host must be ASCII")
	}
	return nil
}
