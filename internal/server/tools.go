package server

import (
	"encoding/json"

	"github.com/mcpmini/mini/internal/config"
)

func toolSchemasFor(cfg *config.Config) []map[string]any {
	schemas := compactToolSchemas()
	if cfg.ExperimentalCodeMode {
		schemas = append(schemas, executeCodeSchema())
	}
	return schemas
}

func compactToolSchemas() []map[string]any {
	return []map[string]any{
		listSchema(),
		callSchema(),
		permCallSchema(),
		configureSchema(),
	}
}

func listSchema() map[string]any {
	return map[string]any{
		"name":        "list",
		"description": "List or search tools across all connected MCP servers. No params: compact index. query: keyword search. tool+detail: full schema for one tool. hidden:true: include hidden tools (if permitted).",
		"inputSchema": schema(map[string]any{
			"query":  prop("string", "Keyword search query"),
			"tool":   prop("string", "Full tool name (server.tool) for detail lookup"),
			"detail": prop("boolean", "Return full schema for the specified tool"),
			"hidden": prop("boolean", "Include hidden tools in results (admin/audit use)"),
		}),
	}
}

func callSchema() map[string]any {
	return map[string]any{
		"name":        "call",
		"description": "Call an open-permission tool on an upstream MCP server. Protected tools must use perm_call.",
		"inputSchema": schema(map[string]any{
			"server": prop("string", "Server name"),
			"tool":   prop("string", "Tool name"),
			"params": map[string]any{"type": "object", "description": "Tool arguments"},
		}),
	}
}

func permCallSchema() map[string]any {
	return map[string]any{
		"name":        "perm_call",
		"description": "Call a protected tool. Tell the user what you are about to do and why before calling this.",
		"inputSchema": schema(map[string]any{
			"server": prop("string", "Server name"),
			"tool":   prop("string", "Tool name"),
			"params": map[string]any{"type": "object", "description": "Tool arguments"},
		}),
	}
}

func configureSchema() map[string]any {
	return map[string]any{
		"name":        "config",
		"description": configureDescription(),
		"inputSchema": schema(map[string]any{
			"action":       prop("string", "status | set_projection | reload (re-reads projections from disk, replacing all runtime-set projections) | add_server | remove_server"),
			"server":       prop("string", "Server name (for set_projection, add_server, remove_server)"),
			"tool":         prop("string", "Tool name (for set_projection)"),
			"projection":   map[string]any{"type": "object", "description": "ProjectionConfig: {mode, include_only, exclude, string_limits, array_limits, strip_markup}"},
			"session_only": prop("boolean", "If true, projection applies only to this session (not persisted). Default: false."),
			"config":       map[string]any{"type": "object", "description": "ServerConfig for add_server"},
		}),
	}
}

func configureDescription() string {
	return "Runtime admin for mini. Actions: " +
		"status (server health + response store stats); " +
		"set_projection (tune response fields for a tool — live + persisted, or session_only:true for temporary); " +
		"reload (re-read projection files from disk without restart); " +
		"add_server (connect a new upstream MCP mid-session, not persisted); " +
		"remove_server (disconnect upstream); " +
		"start_auth (begin OAuth2 PKCE flow for a server — returns URL for user to visit; reconnects automatically on completion); " +
		"auth_status (check whether a valid OAuth token exists for a server). " +
		"Use set_projection to reduce noise when tool responses are too large."
}

func miniConfigSchema() map[string]any {
	return map[string]any{
		"name":        "config",
		"description": "Runtime admin for mini. Actions: status (server health + response store stats); set_projection (tune response fields for a tool); reload (re-read projection files); add_server (connect a new upstream MCP mid-session); remove_server (disconnect upstream); start_auth (begin OAuth2 PKCE flow); auth_status (check OAuth token status).",
		"inputSchema": schema(map[string]any{
			"action":       prop("string", "status | set_projection | reload | add_server | remove_server | start_auth | auth_status"),
			"server":       prop("string", "Server name (for set_projection, add_server, remove_server)"),
			"tool":         prop("string", "Tool name (for set_projection)"),
			"projection":   map[string]any{"type": "object", "description": "ProjectionConfig: {mode, include_only, exclude, string_limits, array_limits, strip_markup}"},
			"session_only": prop("boolean", "If true, projection applies only to this session (not persisted). Default: false."),
			"config":       map[string]any{"type": "object", "description": "ServerConfig for add_server"},
		}),
	}
}

func executeCodeSchema() map[string]any {
	return map[string]any{
		"name": "execute_code",
		"description": "Execute TypeScript in a sandboxed Deno subprocess (no filesystem, network, env, " +
			"or subprocess access; no imports). code: source of an async function, e.g. " +
			"\"async (input) => input.items.filter(i => i.open)\". input: JSON value passed as its argument. " +
			"Returns the function's return value as JSON. Use for multi-step computation over data to keep " +
			"intermediate results out of context.",
		"inputSchema": schemaRequired(map[string]any{
			"code":  prop("string", "Source of an async function"),
			"input": map[string]any{"description": "JSON value passed as the function's argument"},
		}, "code"),
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
	b, _ := json.Marshal(s)
	return b
}

func schemaRequired(properties map[string]any, required ...string) json.RawMessage {
	s := map[string]any{
		"type":       "object",
		"properties": properties,
		"required":   required,
	}
	b, _ := json.Marshal(s)
	return b
}

func prop(typ, desc string) map[string]any {
	return map[string]any{"type": typ, "description": desc}
}
