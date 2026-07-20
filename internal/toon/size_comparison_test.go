package toon

import (
	"bytes"
	"encoding/json"
	"testing"
)

// TestToonSizeComparison compares byte sizes (not tokens; synthetic fixtures,
// indicative only). Only the uniform tabular fixture asserts TOON ≤ JSON —
// the shared header row eliminates per-item key repetition.
func TestToonSizeComparison(t *testing.T) {
	fixtures := []struct {
		name         string
		json         string
		assertToonLE bool
	}{
		{
			name:         "uniform tabular issue list",
			assertToonLE: true,
			json: `{"data":[
				{"number":101,"title":"panic on empty config file","state":"open","author":"testuser1","comments":4},
				{"number":102,"title":"add retry flag to sync command","state":"open","author":"testuser2","comments":0},
				{"number":103,"title":"docs: fix broken quickstart link","state":"closed","author":"testuser3","comments":2},
				{"number":104,"title":"flaky TestWatcherReload on linux","state":"open","author":"testuser1","comments":11},
				{"number":105,"title":"support YAML anchors in config","state":"closed","author":"testuser4","comments":7},
				{"number":106,"title":"reduce allocation in hot path","state":"open","author":"testuser5","comments":3},
				{"number":107,"title":"CLI exits 0 on invalid subcommand","state":"open","author":"testuser2","comments":1},
				{"number":108,"title":"expose health endpoint","state":"closed","author":"testuser6","comments":5}
			]}`,
		},
		{
			name: "nested detail object",
			json: `{"data":{
				"number":4321,
				"title":"add pagination to list endpoint",
				"state":"open",
				"body":"Adds cursor-based pagination.\n\nThe previous offset approach degraded on large tables; this switches to keyset pagination with an opaque cursor.",
				"author":{"login":"testuser7","id":90001},
				"head":{"ref":"feature/pagination","sha":"0123456789abcdef0123456789abcdef01234567"},
				"base":{"ref":"main","sha":"fedcba9876543210fedcba9876543210fedcba98"},
				"labels":["enhancement","api"],
				"created_at":"2026-01-15T10:30:00Z"
			}}`,
		},
		{
			name: "wrapped map with non-uniform items",
			json: `{"data":{
				"total":3,
				"has_more":false,
				"items":[
					{"id":1,"kind":"error","message":"connection refused","tags":["backend","db"]},
					{"id":2,"kind":"warning","message":"slow query: 2.4s"},
					{"id":3,"kind":"error","message":"timeout after 30s","tags":["backend"],"resolved":true}
				]
			}}`,
		},
	}

	for _, tc := range fixtures {
		t.Run(tc.name, func(t *testing.T) {
			jsonBytes, toonBytes := encodedSizes(t, tc.json)
			delta := 100 * float64(jsonBytes-toonBytes) / float64(jsonBytes)
			t.Logf("compact JSON %d bytes, TOON %d bytes, %.1f%% smaller (bytes, not tokens)", jsonBytes, toonBytes, delta)
			if tc.assertToonLE && toonBytes > jsonBytes {
				t.Errorf("TOON (%d bytes) must not exceed compact JSON (%d bytes) for a uniform tabular fixture", toonBytes, jsonBytes)
			}
		})
	}
}

func encodedSizes(t *testing.T, fixture string) (jsonBytes, toonBytes int) {
	t.Helper()
	var compact bytes.Buffer
	if err := json.Compact(&compact, []byte(fixture)); err != nil {
		t.Fatalf("compact fixture JSON: %v", err)
	}
	v, err := FromJSON(json.RawMessage(fixture))
	if err != nil {
		t.Fatalf("FromJSON: %v", err)
	}
	encoded, err := Encode(v)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	return compact.Len(), len(encoded)
}
