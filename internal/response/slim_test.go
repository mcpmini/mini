package response_test

import (
	"strings"
	"testing"

	"github.com/mcpmini/mini/internal/response"
)

var bareArrayInput = []any{
	map[string]any{"name": "main", "protected": true, "sha": "abc123"},
	map[string]any{"name": "dev", "protected": false, "sha": "def456"},
}

func assertBranchItem(t *testing.T, item map[string]any) {
	t.Helper()
	if _, ok := item["sha"]; ok {
		t.Error("sha should be stripped as noise")
	}
	if item["name"] != "main" {
		t.Errorf("name = %v, want main", item["name"])
	}
	if _, ok := item["protected"]; !ok {
		t.Error("protected:true should be kept")
	}
}

func slimItems(t *testing.T, input any) []any {
	t.Helper()
	items, ok := response.Slimify(input)["items"].([]any)
	if !ok {
		t.Fatal("expected slimified items")
	}
	return items
}

func slimItem(t *testing.T, input any) map[string]any {
	t.Helper()
	return slimItems(t, input)[0].(map[string]any)
}

func assertHasKey(t *testing.T, item map[string]any, key string) {
	t.Helper()
	if _, ok := item[key]; !ok {
		t.Errorf("expected %s to be kept", key)
	}
}

func assertMissingKey(t *testing.T, item map[string]any, key string) {
	t.Helper()
	if _, ok := item[key]; ok {
		t.Errorf("expected %s to be dropped", key)
	}
}

func TestSlimifyBareArray(t *testing.T) {
	s := response.Slimify(bareArrayInput)
	meta := mustMeta(t, s)
	if meta["shape"] != "array" {
		t.Errorf("shape = %v, want array", meta["shape"])
	}
	if meta["count"].(int) != 2 {
		t.Errorf("count = %v, want 2", meta["count"])
	}
	items := s["items"].([]any)
	if len(items) != 2 {
		t.Fatalf("items len = %d, want 2", len(items))
	}
	assertBranchItem(t, items[0].(map[string]any))
}

var keepsFalseAndZeroInput = []any{
	map[string]any{"number": float64(1), "draft": false, "merged": false, "comments": float64(0), "title": "fix bug"},
	map[string]any{"number": float64(2), "draft": true, "merged": false, "comments": float64(3), "title": "add feature"},
}

func TestSlimifyKeepsFalseAndZero(t *testing.T) {
	items := slimItems(t, keepsFalseAndZeroInput)
	item0 := items[0].(map[string]any)
	assertHasKey(t, item0, "draft")
	assertHasKey(t, item0, "merged")
	assertHasKey(t, item0, "comments")
	item1 := items[1].(map[string]any)
	if item1["draft"] != true {
		t.Errorf("draft:true should be kept, got %v", item1["draft"])
	}
	if item1["comments"] != float64(3) {
		t.Errorf("comments = %v, want 3", item1["comments"])
	}
}

var wrappedPaginatedInput = map[string]any{
	"issues": []any{
		map[string]any{"number": float64(78158), "title": "gopls issue", "state": "OPEN", "user": map[string]any{"login": "ericchiang"}},
		map[string]any{"number": float64(78156), "title": "sync issue", "state": "OPEN", "user": map[string]any{"login": "MarcoPolo"}},
	},
	"pageInfo": map[string]any{
		"endCursor": "Y3Vyc29yOnYy==", "hasNextPage": true, "hasPreviousPage": false,
	},
	"totalCount": float64(9469),
}

func TestSlimifyWrappedArrayWithPagination(t *testing.T) {
	s := response.Slimify(wrappedPaginatedInput)
	meta := mustMeta(t, s)

	if meta["shape"] != "array" {
		t.Errorf("shape = %v, want array", meta["shape"])
	}
	if meta["total"] != float64(9469) {
		t.Errorf("total = %v, want 9469", meta["total"])
	}
	if meta["next_cursor"] != "Y3Vyc29yOnYy==" {
		t.Errorf("next_cursor = %v, want Y3Vyc29yOnYy==", meta["next_cursor"])
	}
	if meta["has_more"] != true {
		t.Errorf("has_more = %v, want true", meta["has_more"])
	}
	item := s["items"].([]any)[0].(map[string]any)
	if item["user_login"] != "ericchiang" {
		t.Errorf("user_login = %v, want ericchiang", item["user_login"])
	}
	if _, ok := item["user"]; ok {
		t.Error("user object should be flattened, not kept as nested")
	}
}

var nestedFlatteningInput = []any{
	map[string]any{
		"number": float64(78151), "title": "fix bug", "state": "open", "draft": false, "merged": false,
		"base": map[string]any{"ref": "master", "sha": "abc123abc123abc123abc123abc123abc123abc1", "repo": map[string]any{"full_name": "golang/go", "description": "Go"}},
		"head": map[string]any{"ref": "fix/debug-macho", "sha": "def456def456def456def456def456def456def4"},
		"user": map[string]any{"login": "zheliu2", "avatar_url": "https://avatars.githubusercontent.com/u/1?v=4", "id": float64(15888718)},
	},
}

func TestSlimifyNestedObjectFlattening(t *testing.T) {
	item := slimItem(t, nestedFlatteningInput)

	if item["base_ref"] != "master" {
		t.Errorf("base_ref = %v, want master", item["base_ref"])
	}
	if item["head_ref"] != "fix/debug-macho" {
		t.Errorf("head_ref = %v, want fix/debug-macho", item["head_ref"])
	}
	if item["user_login"] != "zheliu2" {
		t.Errorf("user_login = %v, want zheliu2", item["user_login"])
	}
	assertMissingKey(t, item, "user_avatar_url")
	assertMissingKey(t, item, "base_sha")
	assertMissingKey(t, item, "base_repo")
}

var urlNoiseInput = []any{
	map[string]any{
		"number": float64(1), "title": "issue",
		"html_url":       "https://github.com/golang/go/issues/1",
		"comments_url":   "https://api.github.com/repos/golang/go/issues/1/comments",
		"events_url":     "https://api.github.com/repos/golang/go/issues/1/events",
		"labels_url":     "https://api.github.com/repos/golang/go/issues/1/labels{/name}",
		"repository_url": "https://api.github.com/repos/golang/go",
		"url":            "https://api.github.com/repos/golang/go/issues/1",
		"node_id":        "MDU6SXNzdWU0MDcx",
	},
}

func TestSlimifyURLNoise(t *testing.T) {
	item := slimItem(t, urlNoiseInput)
	assertHasKey(t, item, "html_url")
	assertHasKey(t, item, "url")
	for _, noisy := range []string{"comments_url", "events_url", "labels_url", "repository_url", "node_id"} {
		assertMissingKey(t, item, noisy)
	}
}

func TestSlimifyLabelsAsStringArray(t *testing.T) {
	// list_issues already projects labels to []string
	input := []any{
		map[string]any{
			"number": float64(78158),
			"labels": []any{"FeatureRequest", "gopls", "Tools"},
		},
	}
	s := response.Slimify(input)
	items := s["items"].([]any)
	item := items[0].(map[string]any)
	labels, ok := item["labels"].([]any)
	if !ok || len(labels) != 3 {
		t.Errorf("labels = %v, want 3-element string slice", item["labels"])
	}
}

func TestSlimifyLabelObjectsDropped(t *testing.T) {
	// search_issues returns full label objects — nested arrays of maps should be dropped
	input := []any{
		map[string]any{
			"number": float64(1),
			"labels": []any{
				map[string]any{"name": "bug", "color": "d73a4a", "id": float64(1)},
			},
		},
	}
	assertMissingKey(t, slimItem(t, input), "labels")
}

var singleObjectInput = map[string]any{
	"number": float64(78151), "title": "fix bug", "state": "open",
	"additions": float64(47), "deletions": float64(2), "changed_files": float64(2),
	"draft": false, "merged": false,
	"html_url":        "https://github.com/golang/go/pull/78151",
	"body":            "When macho.NewFile encounters io.EOF...",
	"user":            map[string]any{"login": "zheliu2", "id": float64(15888718), "avatar_url": "https://..."},
	"mergeable_state": "blocked",
}

func TestSlimifySingleObject(t *testing.T) {
	s := response.Slimify(singleObjectInput)
	meta := mustMeta(t, s)

	if meta["shape"] != "object" {
		t.Errorf("shape = %v, want object", meta["shape"])
	}
	if len(meta["fields"].([]string)) == 0 {
		t.Error("fields should not be empty")
	}
	if s["number"] != float64(78151) {
		t.Errorf("number = %v, want 78151", s["number"])
	}
	assertHasKey(t, s, "draft")
	if s["user_login"] != "zheliu2" {
		t.Errorf("user_login = %v, want zheliu2", s["user_login"])
	}
	assertMissingKey(t, s, "user_avatar_url")
}

func TestSlimifyDocument(t *testing.T) {
	input := "This is the file content.\nLine two.\nLine three."
	s := response.Slimify(input)
	meta := mustMeta(t, s)

	if meta["shape"] != "document" {
		t.Errorf("shape = %v, want document", meta["shape"])
	}
	if meta["chars"].(int) == 0 {
		t.Error("chars should be non-zero")
	}
	if _, ok := s["preview"]; !ok {
		t.Error("preview should be present")
	}
}

func TestSlimifyDocumentTruncatesAt500(t *testing.T) {
	input := strings.Repeat("x", 1000)
	s := response.Slimify(input)
	preview, _ := s["preview"].(string)
	// preview is JSON-encoded string, so will have quotes
	if len(preview) > 510 {
		t.Errorf("preview length %d should be ~500", len(preview))
	}
}

func TestSlimifyBuildsCategoricalIndex(t *testing.T) {
	input := []any{
		map[string]any{"number": float64(1), "state": "open", "base_ref": "master"},
		map[string]any{"number": float64(2), "state": "open", "base_ref": "dev"},
		map[string]any{"number": float64(3), "state": "closed", "base_ref": "master"},
	}
	s := response.Slimify(input)
	meta := mustMeta(t, s)
	idx, ok := meta["index"].(map[string]any)
	if !ok {
		t.Fatal("index missing")
	}
	stateCounts, ok := idx["state"].(map[string]int)
	if !ok {
		t.Fatalf("state index = %T, want map[string]int", idx["state"])
	}
	if stateCounts["open"] != 2 {
		t.Errorf("open count = %d, want 2", stateCounts["open"])
	}
	if stateCounts["closed"] != 1 {
		t.Errorf("closed count = %d, want 1", stateCounts["closed"])
	}
}

func TestSlimifyNoIndexForHighCardinality(t *testing.T) {
	// 25 distinct string values — should not be indexed (>20 limit)
	items := make([]any, 25)
	for i := range items {
		items[i] = map[string]any{"id": float64(i), "title": strings.Repeat("x", i+1)}
	}
	s := response.Slimify(items)
	meta := mustMeta(t, s)
	idx, _ := meta["index"].(map[string]any)
	if _, ok := idx["title"]; ok {
		t.Error("high-cardinality field should not be indexed")
	}
}

var searchReposInput = map[string]any{
	"incomplete_results": false,
	"total_count":        float64(417),
	"items": []any{
		map[string]any{
			"full_name": "metoro-io/mcp-golang", "name": "mcp-golang", "language": "Go",
			"stargazers_count": float64(1206), "forks_count": float64(119), "open_issues_count": float64(44),
			"description": "Write MCP servers in Go", "html_url": "https://github.com/metoro-io/mcp-golang",
			"topics": []any{"ai", "golang", "mcp"}, "archived": false, "fork": false, "private": false,
		},
	},
}

func TestSlimifySearchRepositories(t *testing.T) {
	s := response.Slimify(searchReposInput)
	meta := mustMeta(t, s)

	if meta["total"] != float64(417) {
		t.Errorf("total = %v, want 417", meta["total"])
	}
	item := slimItem(t, searchReposInput)
	if item["stargazers_count"] != float64(1206) {
		t.Errorf("stargazers_count = %v, want 1206", item["stargazers_count"])
	}
	assertHasKey(t, item, "topics")
	for _, f := range []string{"archived", "fork", "private"} {
		assertHasKey(t, item, f)
	}
}

var getCommitInput = map[string]any{
	"sha": "1f9de17ca8ed2612a682bf2731b7c6b2e80fb96a", "html_url": "https://github.com/golang/go/commit/1f9de17",
	"author": map[string]any{"login": "matloob", "id": float64(16470053), "avatar_url": "https://..."},
	"commit": map[string]any{
		"message": "cmd/go: split mod_get_pseudo_hg test",
		"author":  map[string]any{"name": "matloob", "email": "matloob@golang.org", "date": "2026-03-11T16:38:32Z"},
	},
	"stats": map[string]any{"additions": float64(249), "deletions": float64(81), "total": float64(330)},
	"files": []any{map[string]any{"filename": "src/cmd/go/testdata/script/mod_get_pseudo_hg.txt", "status": "removed", "changes": float64(81)}},
}

func TestSlimifyGetCommit(t *testing.T) {
	s := response.Slimify(getCommitInput)
	meta := mustMeta(t, s)
	if meta["shape"] != "object" {
		t.Errorf("shape = %v, want object", meta["shape"])
	}
	if _, ok := s["sha"]; ok {
		t.Error("sha should be dropped as noise")
	}
	if s["commit_message"] == nil {
		t.Error("commit_message should be present after flattening")
	}
	if s["stats_additions"] != float64(249) {
		t.Errorf("stats_additions = %v, want 249", s["stats_additions"])
	}
	if s["author_login"] != "matloob" {
		t.Errorf("author_login = %v, want matloob", s["author_login"])
	}
}

func mustMeta(t *testing.T, s map[string]any) map[string]any {
	t.Helper()
	meta, ok := s["_meta"].(map[string]any)
	if !ok {
		t.Fatalf("_meta missing or wrong type, got %T", s["_meta"])
	}
	return meta
}
