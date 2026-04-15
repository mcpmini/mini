//go:build evals

package evals

import "path/filepath"

var writeOps = map[string][]string{
	"github": {
		"create_pull_request", "add_issue_comment", "add_reply_to_pull_request_comment",
		"create_branch", "merge_pull_request", "update_pull_request",
		"create_or_update_file", "delete_file", "push_files",
		"create_repository", "fork_repository",
	},
	"jira":   {"create_issue", "update_issue", "add_comment", "assign_issue", "add_label"},
	"slack":  {"post_message", "send_message", "add_reaction"},
	"linear": {"create_issue", "update_issue", "add_comment"},
	"notion": {"create_page", "update_page", "create_database"},
	"sentry": {"update_issue", "create_note"},
}

// DefaultServers creates fixture dirs for the named servers.
// Returns a map of server name → fixture dir path.
func DefaultServers(r *Runner, env *Env, names ...string) (map[string]string, error) {
	out := make(map[string]string, len(names))
	for _, name := range names {
		dir, err := DefaultServer(r, env, name)
		if err != nil {
			return nil, err
		}
		out[name] = dir
	}
	return out, nil
}

func DefaultServer(r *Runner, env *Env, name string) (string, error) {
	m := NewMCPMapping().FromFixtureDir(filepath.Join(r.FixturesDir, name))
	for _, op := range writeOps[name] {
		m.WriteOp(op)
	}
	return m.Dir(env)
}
