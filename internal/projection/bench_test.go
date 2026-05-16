//go:build test

package projection_test

import (
	"strings"
	"testing"

	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/projection"
)

var benchDefaults = &projection.Defaults{
	StringLimit:        1000,
	DepthLimit:         3,
	ContentFields:      []string{"body"},
	AutoStripThreshold: 500,
}

func makeBenchPayload(items int, strLen int) map[string]any {
	arr := make([]any, items)
	for i := range arr {
		arr[i] = map[string]any{
			"id":    i,
			"title": strings.Repeat("t", 40),
			"body":  strings.Repeat("x", strLen),
		}
	}
	return map[string]any{"items": arr, "total": items}
}

func BenchmarkApply(b *testing.B) {
	cases := []struct {
		name   string
		items  int
		strLen int
	}{
		{"1KB", 5, 100},
		{"10KB", 20, 400},
		{"100KB", 50, 1800},
	}
	for _, tc := range cases {
		payload := makeBenchPayload(tc.items, tc.strLen)
		b.Run(tc.name, func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				projection.Apply(payload, nil, benchDefaults)
			}
		})
	}
}

func BenchmarkApply_stripContent(b *testing.B) {
	body := strings.Repeat("# Heading\n\nParagraph with **bold** and `code`.\n\n", 50)
	payload := map[string]any{"body": body, "title": "benchmark"}
	cfg := &config.ProjectionConfig{StripMarkup: true}

	for i := 0; i < b.N; i++ {
		projection.Apply(payload, cfg, benchDefaults)
	}
}
