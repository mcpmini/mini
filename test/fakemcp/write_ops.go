//go:build integration

package main

import (
	"fmt"
	"strings"
	"time"
)

// writeOpMappings defines the synthetic response template for each write operation.
//
// Leaf string values:
//   - "$.field"   → copies args["field"] (any type)
//   - "${field}"  → string interpolation of args["field"]
//   - "$now"      → RFC3339 timestamp string
//   - "$ts"       → Unix float timestamp string (e.g. Slack "1710403200.000100")
//   - "$id"       → synthetic integer ID
//   - "$sha"      → fake 40-char hex git SHA
//   - "$uuid"     → fake UUID string
//
// Nested map[string]any values are resolved recursively.
// Tools shared across services use a superset of fields.
var writeOpMappings = map[string]map[string]any{
	// ── GitHub ──────────────────────────────────────────────────────────────────
	"create_pull_request": {
		"id": "$id", "number": 101, "state": "open", "mergeable": true,
		"title": "$.title", "body": "$.body", "draft": "$.draft",
		"head":       map[string]any{"ref": "$.head", "sha": "$sha"},
		"base":       map[string]any{"ref": "$.base", "sha": "$sha"},
		"html_url":   "https://github.com/${owner}/${repo}/pull/101",
		"user":       map[string]any{"login": "agent", "id": 99999},
		"created_at": "$now", "updated_at": "$now",
	},
	"update_pull_request": {
		"id": "$id", "number": "$.pull_number", "state": "open", "mergeable": true,
		"title": "$.title", "body": "$.body",
		"html_url": "https://github.com/${owner}/${repo}/pull/101", "updated_at": "$now",
	},
	"merge_pull_request": {
		"sha":     "$sha",
		"merged":  true,
		"message": "$.commit_title",
	},
	"add_issue_comment": {
		"id": "$id", "body": "$.body",
		"html_url":   "https://github.com/${owner}/${repo}/issues/${issue_number}#issuecomment-10000002",
		"user":       map[string]any{"login": "agent"},
		"created_at": "$now", "updated_at": "$now",
	},
	"add_reply_to_pull_request_comment": {
		"id": "$id", "body": "$.body",
		"html_url": "https://github.com/${owner}/${repo}/pull/${pull_number}#issuecomment-10000003",
		"user":     map[string]any{"login": "agent"}, "created_at": "$now",
	},
	"create_branch": {
		"ref": "refs/heads/${branch}",
		"object": map[string]any{
			"sha":  "$sha",
			"type": "commit",
			"url":  "https://api.github.com/repos/${owner}/${repo}/git/commits/$sha",
		},
	},
	"create_or_update_file": {
		"content": map[string]any{
			"name": "$.path", "path": "$.path", "sha": "$sha",
			"html_url": "https://github.com/${owner}/${repo}/blob/${branch}/${path}",
		},
		"commit": map[string]any{
			"sha": "$sha", "message": "$.message",
			"html_url": "https://github.com/${owner}/${repo}/commit/a1b2c3d4e5f6",
		},
	},
	"delete_file": {
		"content": nil,
		"commit": map[string]any{
			"sha": "$sha", "message": "$.message",
			"html_url": "https://github.com/${owner}/${repo}/commit/a1b2c3d4e5f6",
		},
	},
	"push_files": {
		"ref":    "refs/heads/${branch}",
		"object": map[string]any{"sha": "$sha", "type": "commit"},
	},
	"create_repository": {
		"id": "$id", "name": "$.name", "full_name": "agent/${name}",
		"description": "$.description", "private": "$.private",
		"html_url":   "https://github.com/agent/${name}",
		"clone_url":  "https://github.com/agent/${name}.git",
		"ssh_url":    "git@github.com:agent/${name}.git",
		"created_at": "$now", "updated_at": "$now",
	},
	"fork_repository": {
		"id": "$id", "name": "$.repo",
		"full_name": "${organization}/${repo}",
		"html_url":  "https://github.com/${organization}/${repo}",
		"clone_url": "https://github.com/${organization}/${repo}.git",
		"fork":      true, "created_at": "$now",
	},

	// ── Jira + Linear + Sentry (shared tool names, superset response) ──────────
	"create_issue": {
		// Jira fields
		"id":   "$id",
		"key":  "PROJ-123",
		"self": "https://acme.atlassian.net/rest/api/3/issue/10001",
		// summary echoes Jira arg; title echoes Linear arg
		"summary": "$.summary",
		// Linear fields
		"identifier":  "ENG-123",
		"title":       "$.title",
		"description": "$.description",
		"priority":    "$.priority",
		"state":       map[string]any{"name": "Todo", "type": "unstarted"},
		"team":        map[string]any{"id": "team-1", "name": "Engineering", "key": "ENG"},
		"url":         "https://linear.app/company/issue/ENG-123",
		// Common
		"created_at": "$now", "createdAt": "$now",
	},
	"update_issue": {
		// id comes from whichever arg is present (issue_key for Jira, issueId for Linear, issue_id for Sentry)
		"id":      "$id",
		"key":     "$.issue_key",
		"updated": true,       // Jira
		"success": true,       // Linear
		"status":  "$.status", // Sentry/Jira
	},
	"add_comment": {
		"id": "$id",
		// Jira uses "comment" arg; Linear uses "body" arg — include both
		"body":    "$.body",
		"comment": "$.comment",
		"self":    "https://acme.atlassian.net/rest/api/3/issue/PROJ-123/comment/10001",
		"created": "$now", "updated": "$now",
		"createdAt": "$now",
		"issue":     map[string]any{"id": "$.issueId"},
		"author":    map[string]any{"displayName": "Agent", "emailAddress": "agent@example.com"},
	},

	// ── Jira-only ────────────────────────────────────────────────────────────────
	"assign_issue": {"id": "$.issue_key", "updated": true},
	"add_label":    {"id": "$.issue_key", "updated": true},

	// ── Slack ─────────────────────────────────────────────────────────────────
	"post_message": {
		"ok": true, "ts": "$ts",
		"channel": "$.channel",
		"message": map[string]any{"type": "message", "ts": "$ts", "text": "$.text"},
	},
	"send_message": {
		"ok": true, "ts": "$ts",
		"channel": "$.channel_id",
		"message": map[string]any{"type": "message", "ts": "$ts", "text": "$.text"},
	},
	"add_reaction": {"ok": true},

	// ── Notion ────────────────────────────────────────────────────────────────
	"create_page": {
		"id": "$uuid", "object": "page",
		"url":              "https://notion.so/$uuid",
		"created_time":     "$now",
		"last_edited_time": "$now",
		"parent":           "$.parent",
		"properties":       "$.properties",
	},
	"update_page": {
		"id": "$.page_id", "object": "page",
		"url":              "https://notion.so/${page_id}",
		"last_edited_time": "$now",
		"archived":         "$.archived",
		"properties":       "$.properties",
	},
	"create_database": {
		"id": "$uuid", "object": "database",
		"url":          "https://notion.so/$uuid",
		"title":        "$.title",
		"parent":       "$.parent",
		"created_time": "$now",
	},

	// ── Sentry ────────────────────────────────────────────────────────────────
	"create_note": {
		"id":          "$uuid",
		"text":        "$.text",
		"dateCreated": "$now",
		"user":        map[string]any{"name": "Agent", "email": "agent@example.com"},
	},
}

func applyMapping(template map[string]any, args map[string]any) map[string]any {
	out := make(map[string]any, len(template))
	for k, v := range template {
		out[k] = resolveValue(v, args)
	}
	return out
}

func resolveValue(v any, args map[string]any) any {
	switch val := v.(type) {
	case string:
		return resolveString(val, args)
	case map[string]any:
		return applyMapping(val, args)
	default:
		return v
	}
}

func resolveString(s string, args map[string]any) any {
	if value, ok := fixedPlaceholderValue(s); ok {
		return value
	}
	if strings.HasPrefix(s, "$.") {
		return args[s[2:]]
	}
	if strings.Contains(s, "${") {
		return interpolate(s, args)
	}
	return s
}

func fixedPlaceholderValue(s string) (any, bool) {
	switch s {
	case "$now":
		return time.Now().UTC().Format(time.RFC3339), true //nolint:clocklint
	case "$ts":
		return fmt.Sprintf("%.6f", float64(time.Now().UnixMilli())/1000.0), true //nolint:clocklint
	case "$id":
		return 10000001, true
	case "$sha":
		return "a1b2c3d4e5f6a7b8c9d0e1f2a3b4c5d6e7f8a9b0", true
	case "$uuid":
		return "4a8d9c2e-1b3f-4e5a-8c7d-2b4f6e8a9c1d", true
	default:
		return nil, false
	}
}

func interpolate(s string, args map[string]any) string {
	for k, v := range args {
		if str, ok := v.(string); ok {
			s = strings.ReplaceAll(s, "${"+k+"}", str)
		}
	}
	return s
}
