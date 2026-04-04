//go:build evals

package evals_test

import (
	"path/filepath"
	"testing"
)

// writeOps lists write operations per service. fakemcp generates synthetic
// responses from request args for these tools instead of serving fixture files.
var writeOps = map[string][]string{
	"github": {
		"create_pull_request",
		"add_issue_comment",
		"add_reply_to_pull_request_comment",
		"create_branch",
		"merge_pull_request",
		"update_pull_request",
		"create_or_update_file",
		"delete_file",
		"push_files",
		"create_repository",
		"fork_repository",
	},
	"jira": {
		"create_issue",
		"update_issue",
		"add_comment",
		"assign_issue",
		"add_label",
	},
	"slack": {
		"post_message",
		"send_message",
		"add_reaction",
	},
	"linear": {
		"create_issue",
		"update_issue",
		"add_comment",
	},
	"notion": {
		"create_page",
		"update_page",
		"create_database",
	},
	"sentry": {
		"update_issue",
		"create_note",
	},
}

func defaultServers(t *testing.T, names ...string) map[string]string {
	t.Helper()
	out := make(map[string]string, len(names))
	for _, name := range names {
		out[name] = defaultServer(t, name)
	}
	return out
}

func defaultServer(t *testing.T, name string) string {
	t.Helper()
	m := NewMCPMapping().FromFixtureDir(filepath.Join(fixturesDir, name))
	for _, op := range writeOps[name] {
		m.WriteOp(op)
	}
	return m.Dir(t)
}
