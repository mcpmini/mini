//go:build test

package main

import (
	"reflect"
	"testing"
)

func TestParseToolTable(t *testing.T) {
	output := "TOOL DESCRIPTION\nalpha first tool\nsection:\nbeta second tool\n"
	want := []tool{{Name: "alpha"}, {Name: "beta"}}
	if got := parseToolTable(output); !reflect.DeepEqual(got, want) {
		t.Fatalf("parseToolTable() = %#v, want %#v", got, want)
	}
}
