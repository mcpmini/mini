package server

import "encoding/json"

func miniConfigSchema() map[string]any {
	return map[string]any{
		"name":        "config",
		"description": "Runtime admin for mini. Actions: status (server health, servers still starting or unavailable and why, response store stats); get_projection (a tool's server rule and this session's override); set_projection (replace a tool's projection in its server YAML); reload (re-read projections from server YAML files); add_server (connect a new upstream MCP and save it to config); remove_server (disconnect an upstream and delete it from config); start_auth (begin OAuth2 PKCE flow); auth_status (check OAuth token status).",
		"inputSchema": schema(map[string]any{
			"action": prop(
				"string",
				"status | get_projection | set_projection | reload | add_server | remove_server | start_auth | auth_status",
			),
			"server": prop(
				"string",
				"Server name (for get_projection, set_projection, add_server, remove_server)",
			),
			"tool": prop("string", "Tool name (for get_projection, set_projection)"),
			"projection": map[string]any{
				"type":        "object",
				"description": "ProjectionConfig: {include_only, exclude, string_limits, array_limits, depth_limit, strip_markup, format (json|toon), alias}",
			},
			"session_only": prop(
				"boolean",
				"If true, projection applies only to this session (not persisted). Default: false.",
			),
			"config": map[string]any{"type": "object", "description": "ServerConfig for add_server"},
		}),
	}
}

func miniReadSchema() map[string]any {
	return map[string]any{
		"name":        "read",
		"description": "Read a raw upstream response file written by mini. Pass the bare key from __mini.file (e.g. 1750830563123). Use filter to run a jq expression and extract specific fields instead of reading the full file.",
		"inputSchema": schema(map[string]any{
			"file":   prop("string", "Bare key (e.g. 1750830563123) from __mini.file"),
			"filter": prop("string", "Optional jq filter (e.g. .title, .[0].body, .items[0].body, .items[] | .title)"),
		}),
	}
}

func schema(properties map[string]any) json.RawMessage {
	s := map[string]any{
		"type":       "object",
		"properties": properties,
	}
	b, _ := json.Marshal(s) //nolint:errcheck // All callers pass fixed JSON-compatible schemas.
	return b
}

func prop(typ, desc string) map[string]any {
	return map[string]any{"type": typ, "description": desc}
}
