package jqfilter

import (
	"context"
	"strings"
	"testing"
)

func TestRun(t *testing.T) {
	data := map[string]any{
		"foo": "bar",
		"items": []any{
			map[string]any{"name": "a"},
			map[string]any{"name": "b"},
		},
	}

	tests := []struct {
		name string
		expr string
		want string
	}{
		{"field access", ".foo", `"bar"`},
		{"multiple results", ".items[].name", "\"a\"\n\"b\""},
		{"length", ".items | length", "2"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Run(context.Background(), data, tt.expr)
			if err != nil {
				t.Fatalf("Run(%q) returned error: %v", tt.expr, err)
			}
			if got != tt.want {
				t.Errorf("Run(%q) = %q, want %q", tt.expr, got, tt.want)
			}
		})
	}
}

func TestRunZeroResults(t *testing.T) {
	got, err := Run(context.Background(), map[string]any{"foo": "bar"}, ".missing | empty")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "" {
		t.Errorf("got %q, want empty string", got)
	}
}

func TestRunInvalidFilterSyntax(t *testing.T) {
	_, err := Run(context.Background(), map[string]any{}, ".foo[")
	if err == nil {
		t.Fatal("expected error for invalid filter syntax")
	}
}

func TestRunRuntimeError(t *testing.T) {
	_, err := Run(context.Background(), map[string]any{"foo": 1}, ".foo.bar")
	if err == nil {
		t.Fatal("expected runtime error for indexing a number")
	}
}

func TestRunTimeout(t *testing.T) {
	_, err := Run(context.Background(), nil, "repeat(1)")
	if err == nil {
		t.Fatal("expected timeout error for an infinite filter")
	}
	if !strings.Contains(err.Error(), "timed out") {
		t.Errorf("expected timeout error, got: %v", err)
	}
}

func TestRunHaltError(t *testing.T) {
	_, err := Run(context.Background(), map[string]any{}, `"boom" | halt_error`)
	if err == nil {
		t.Fatal("expected halt error")
	}
	if !strings.Contains(err.Error(), "boom") {
		t.Errorf("expected halt error to mention message, got: %v", err)
	}
}
