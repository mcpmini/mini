package config

import (
	"errors"
	"fmt"
	"strings"
)

// ExcludeStep is one step of a root-relative exclude path: an object field, or every item of an array.
type ExcludeStep struct {
	Field string
	Array bool
}

var (
	errExcludeEmptyField = errors.New(
		"empty field name; separate fields with single dots, for example items[].author.login",
	)
	errExcludeBadChar = errors.New(
		"fields may contain only ASCII letters, digits, underscores and hyphens; wildcards and jq expressions are unsupported",
	)
	errExcludeBracket = errors.New("brackets must be exactly [] after a field or another []")
	errExcludeIndex   = errors.New(
		"numeric indexes are unsupported; use [] for every item, for example items[].secret",
	)
)

// ParseExcludePath parses dot-separated object fields where [] steps into every array item, e.g. items[].author.login.
func ParseExcludePath(rule string) ([]ExcludeStep, error) {
	var steps []ExcludeStep
	for i, segment := range strings.Split(rule, ".") {
		parsed, err := parseExcludeSegment(segment, i == 0)
		if err != nil {
			return nil, fmt.Errorf("invalid exclude path %q: %w", rule, err)
		}
		steps = append(steps, parsed...)
	}
	if steps[len(steps)-1].Array {
		return nil, fmt.Errorf("invalid exclude path %q: %s", rule, arrayEndHint(rule))
	}
	return steps, nil
}

func parseExcludeSegment(segment string, first bool) ([]ExcludeStep, error) {
	end := 0
	for end < len(segment) && isExcludeFieldByte(segment[end]) {
		end++
	}
	name := segment[:end]
	items, err := countArraySteps(segment[end:])
	if err != nil {
		return nil, err
	}
	if name == "" && (!first || items == 0) {
		return nil, errExcludeEmptyField
	}
	var steps []ExcludeStep
	if name != "" {
		steps = append(steps, ExcludeStep{Field: name})
	}
	for range items {
		steps = append(steps, ExcludeStep{Array: true})
	}
	return steps, nil
}

func countArraySteps(s string) (int, error) {
	count := 0
	for s != "" {
		if s[0] != '[' && s[0] != ']' {
			return 0, errExcludeBadChar
		}
		closing := strings.IndexByte(s, ']')
		if s[0] != '[' || closing < 0 {
			return 0, errExcludeBracket
		}
		switch inner := s[1:closing]; {
		case inner == "":
			count++
		case strings.Trim(inner, "0123456789") == "":
			return 0, errExcludeIndex
		default:
			return 0, errExcludeBracket
		}
		s = s[closing+1:]
	}
	return count, nil
}

func arrayEndHint(rule string) string {
	whole := rule
	for strings.HasSuffix(whole, "[]") {
		whole = strings.TrimSuffix(whole, "[]")
	}
	if whole == "" {
		return "a path must end in a field name; name the field to remove inside each item, for example [].secret"
	}
	return "a path must end in a field name; to drop the whole array use " + whole
}

func isExcludeFieldByte(b byte) bool {
	return b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b >= '0' && b <= '9' || b == '_' || b == '-'
}

// ValidateProjection checks projection rules before they are persisted or published.
func ValidateProjection(p *ProjectionConfig) error {
	if p == nil {
		return nil
	}
	if err := ValidResponseFormat(p.Format); err != nil {
		return fmt.Errorf("format: %w", err)
	}
	for _, path := range p.Exclude {
		if _, err := ParseExcludePath(path); err != nil {
			return err
		}
	}
	return nil
}
