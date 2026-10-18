package config_test

import (
	"slices"
	"testing"

	"github.com/mcpmini/mini/internal/config"
)

func TestParseExcludePathAcceptsFieldsAndArraySteps(t *testing.T) {
	field := func(name string) config.ExcludeStep { return config.ExcludeStep{Field: name} }
	item := config.ExcludeStep{Array: true}
	cases := map[string][]config.ExcludeStep{
		"secret":                        {field("secret")},
		"author.avatar_url":             {field("author"), field("avatar_url")},
		"[].author.avatar_url":          {item, field("author"), field("avatar_url")},
		"items[].author.avatar_url":     {field("items"), item, field("author"), field("avatar_url")},
		"groups[].members[].avatar_url": {field("groups"), item, field("members"), item, field("avatar_url")},
		"matrix[][].secret":             {field("matrix"), item, item, field("secret")},
		"[][].secret":                   {item, item, field("secret")},
		"0.name":                        {field("0"), field("name")},
		"_meta.field-1":                 {field("_meta"), field("field-1")},
	}
	for rule, want := range cases {
		t.Run(rule, func(t *testing.T) {
			got, err := config.ParseExcludePath(rule)
			if err != nil || !slices.Equal(got, want) {
				t.Fatalf("ParseExcludePath(%q) = %v, %v; want %v", rule, got, err, want)
			}
		})
	}
}

func TestParseExcludePathExplainsEachRejection(t *testing.T) {
	const (
		index   = "numeric indexes are unsupported; use [] for every item, for example items[].secret"
		empty   = "empty field name; separate fields with single dots, for example items[].author.login"
		badChar = "fields may contain only ASCII letters, digits, underscores and hyphens; wildcards and jq expressions are unsupported"
		bracket = "brackets must be exactly [] after a field or another []"
		bareArr = "a path must end in a field name; name the field to remove inside each item, for example [].secret"
	)
	cases := map[string]string{
		"items[0].secret":    index,
		"[0].secret":         index,
		"items[]":            "a path must end in a field name; to drop the whole array use items",
		"groups[].members[]": "a path must end in a field name; to drop the whole array use groups[].members",
		"[]":                 bareArr,
		"[][]":               bareArr,
		"":                   empty,
		".items":             empty,
		"a..b":               empty,
		"a.":                 empty,
		"..x":                empty,
		"items.[].x":         empty,
		"a b":                badChar,
		"items.*":            badChar,
		"*":                  badChar,
		"head | .repo":       badChar,
		"head/description":   badChar,
		"café":               badChar,
		"items[]x.y":         badChar,
		"items[":             bracket,
		"items]":             bracket,
		"items[x]":           bracket,
		"items[*].desc":      bracket,
	}
	for rule, reason := range cases {
		t.Run(rule, func(t *testing.T) {
			steps, err := config.ParseExcludePath(rule)
			want := `invalid exclude path "` + rule + `": ` + reason
			if err == nil || err.Error() != want {
				t.Fatalf("ParseExcludePath(%q) = %v, %v; want error %q", rule, steps, err, want)
			}
		})
	}
}
