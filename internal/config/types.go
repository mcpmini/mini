package config

// Config is the top-level mini configuration.
type Config struct {
	// InlineThreshold is the max projected-response token estimate below which
	// responses are returned inline (no file written). Measured after projection
	// and slimming — the actual tokens the agent would see inline. Default: 2000.
	InlineThreshold int `yaml:"inline_threshold"`

	// DefaultStringLimit is the default max chars for string fields across all
	// projections. 0 means no limit (default). Override per-field with
	// string_limits in a projection config.
	DefaultStringLimit int `yaml:"default_string_limit"`

	// DefaultDepthLimit is the default max nesting depth. 0 means no limit (default).
	DefaultDepthLimit int `yaml:"default_depth_limit"`

	// ContentFields are field names that get HTML/MD stripped automatically
	// when their value is >= AutoStripThreshold chars.
	// Defaults to common content fields: body, description, summary, etc.
	ContentFields []string `yaml:"content_fields,omitempty"`

	// AutoStripThreshold is the min char length for auto-stripping content fields.
	// 0 means disabled (default). Enable only if your upstreams embed raw HTML
	// in responses — modern markdown is better left intact for LLMs.
	AutoStripThreshold int `yaml:"auto_strip_threshold"`

	// ResponseDir overrides where response files are written.
	// Defaults to ~/.mini/responses/
	ResponseDir string `yaml:"response_dir"`

	// ResponseTTL is how long response files live (e.g. "1h"). Default: 1h.
	ResponseTTL string `yaml:"response_ttl"`

	// ResponseDiskBudgetMB is the max disk usage for response files. Default: 500.
	// Oldest files are evicted when the budget is exceeded.
	ResponseDiskBudgetMB int `yaml:"response_disk_budget_mb"`

	// ResponseFormat controls how tool results are rendered to agents.
	// "json" (default) returns the full envelope. "mini" returns a compact
	// key:value text format — useful for agents that handle plain text better.
	ResponseFormat string `yaml:"response_format"`

	// LogLevel: debug, info, warn, error. Default: info.
	LogLevel string `yaml:"log_level"`

	// DisableListHidden prevents agents from using list(hidden:true) to discover
	// hidden tools. When false (default), agents and admins can audit hidden tools.
	DisableListHidden bool `yaml:"disable_list_hidden"`

	// DangerousAllowRuntimeStdio permits add_server to launch arbitrary stdio
	// subprocesses at runtime. Off by default — stdio transports exec commands
	// and should only be registered at startup from trusted config files.
	DangerousAllowRuntimeStdio bool `yaml:"dangerous_allow_runtime_stdio"`

	// DangerousAllowPrivateURLs disables SSRF protection on add_server, allowing
	// upstream URLs that resolve to private/loopback addresses. Only useful in
	// test environments where upstreams run on localhost.
	DangerousAllowPrivateURLs bool `yaml:"dangerous_allow_private_urls"`

	// DaemonPort is the TCP port the daemon listens on. Default: 4857.
	// Agents connect to http://127.0.0.1:<DaemonPort>/mcp.
	DaemonPort int `yaml:"daemon_port"`

	// BrowserCommand sets the browser to open for OAuth flows. Applies to both
	// `mini auth` (CLI) and agent-initiated auth (config:start_auth).
	BrowserCommand string `yaml:"browser_command,omitempty"`

	// DisableAuthBrowserOpen prevents mini from opening the browser automatically
	// during config:start_auth. The auth URL is still returned to the agent.
	DisableAuthBrowserOpen bool `yaml:"disable_auth_browser_open,omitempty"`

	// Servers is a list of upstream MCP server configs (alternative to servers/ dir).
	Servers []ServerConfig `yaml:"servers,omitempty"`
}

// SessionModePerSession is the session_mode value that gives each session its own upstream connection.
// The default (empty string or "shared") pools connections across sessions.
const SessionModePerSession = "per_session"

var DefaultContentFields = []string{
	"body", "description", "text", "readme", "content", "message", "summary", "patch",
}

func DefaultConfig() *Config {
	return &Config{
		InlineThreshold:      3500,
		DefaultStringLimit:   0,
		DefaultDepthLimit:    0,
		AutoStripThreshold:   0,
		ResponseTTL:          "1h",
		ResponseDiskBudgetMB: 500,
		LogLevel:             "info",
		ContentFields:        DefaultContentFields,
		DaemonPort:           4857,
	}
}

// ServerConfig describes one upstream MCP server.
type ServerConfig struct {
	// Name is the server identifier used in tool namespacing.
	Name string `yaml:"name"`

	// Command and Args for stdio transport.
	Command string   `yaml:"command,omitempty"`
	Args    []string `yaml:"args,omitempty"`

	// Env vars to set for the subprocess.
	Env []string `yaml:"env,omitempty"`

	// Transport type: "stdio" (default), "sse", "streamable".
	Transport string `yaml:"transport,omitempty"`

	// URL for SSE/streamable transport.
	URL string `yaml:"url,omitempty"`

	// Headers for HTTP transport. Values support ${ENV_VAR} expansion.
	Headers map[string]string `yaml:"headers,omitempty"`

	// Auth configuration.
	Auth *AuthConfig `yaml:"auth,omitempty"`

	// Permissions config.
	Permissions *PermissionsConfig `yaml:"permissions,omitempty"`

	// Projections maps tool name → projection config.
	Projections map[string]*ProjectionConfig `yaml:"projections,omitempty"`

	// ToolTimeout is the per-call deadline (e.g. "30s", "5m"). Default "30s", "0" = no timeout.
	// Supports long durations for slow tools (e.g. "10m" for AI-powered analysis).
	ToolTimeout string `yaml:"tool_timeout,omitempty"`

	// HTTPClientTimeout is the hard deadline on the HTTP transport connection, applied
	// independently of ToolTimeout as a network-level backstop. Default "60s".
	// Set to "0" to disable (not recommended for production).
	HTTPClientTimeout string `yaml:"http_client_timeout,omitempty"`

	// MaxPendingRequests is the max number of concurrent in-flight calls to this
	// upstream. New requests beyond this limit are rejected immediately with an
	// error rather than queuing. 0 means unlimited (default).
	MaxPendingRequests int `yaml:"max_pending_requests,omitempty"`

	// DisableRetryOnRateLimit disables mini's automatic 429/503 retry for this server.
	// When true, rate-limit errors are returned immediately to the agent.
	// Default false: mini retries with Retry-After back-off, hiding transient limits.
	DisableRetryOnRateLimit bool `yaml:"disable_retry_on_rate_limit,omitempty"`

	// SessionMode controls upstream connection sharing:
	//   "shared" (default) — one connection pooled across all sessions (stateless APIs)
	//   "per_session"      — one connection per session (Playwright, Puppeteer, Memory)
	SessionMode string `yaml:"session_mode,omitempty"`

	// Enabled defaults to true.
	Enabled *bool `yaml:"enabled,omitempty"`

	// RuntimeAdded marks servers registered at runtime via the MCP config tool
	// (not from a config file). Runtime-added servers are untrusted — they may
	// have been injected by an agent — so their connections get SSRF dial-time
	// validation in addition to the add_server URL check.
	RuntimeAdded bool `yaml:"-" json:"-"`
}

func (sc ServerConfig) IsEnabled() bool {
	return sc.Enabled == nil || *sc.Enabled
}

// AuthConfig describes how to authenticate with an upstream server.
type AuthConfig struct {
	// Type: "apikey", "bearer", "oauth2"
	Type string `yaml:"type"`

	// For apikey/bearer: the token value or env var reference ($MY_TOKEN)
	Token string `yaml:"token"`

	// Header name for injection. Default: "Authorization"
	Header string `yaml:"header"`

	// OAuth2 fields
	ClientID     string   `yaml:"client_id"`
	ClientSecret string   `yaml:"client_secret"`
	AuthURL      string   `yaml:"auth_url"`
	TokenURL     string   `yaml:"token_url"`
	Scopes       []string `yaml:"scopes"`

	// ResourceURL is the canonical URI of the MCP server sent as the RFC 8707
	// resource parameter in auth and token requests. Populated automatically
	// from the server URL during discovery; not set by users in YAML.
	ResourceURL string `yaml:"-"`

	// BrowserCmd overrides the command used to open the OAuth2 browser window.
	// Useful for targeting a specific browser profile, e.g.:
	//   open -na "Google Chrome" --args --profile-directory="Profile 1"
	// The auth URL is appended as the final argument.
	// If unset, the platform default opener is used (open/xdg-open/start).
	BrowserCmd string `yaml:"browser_cmd,omitempty"`
}

// PermissionsConfig defines tool access tiers for a server.
type PermissionsConfig struct {
	// Default tier for tools not explicitly listed: "open" (default) or "protected"
	Default string `yaml:"default"`

	// Protected tools require execute_protected + human approval.
	Protected []string `yaml:"protected"`

	// Hidden tools are not visible in discover at all.
	Hidden []string `yaml:"hidden"`
}

// PermissionLevel represents the access tier of a tool.
type PermissionLevel string

const (
	PermOpen      PermissionLevel = "open"
	PermProtected PermissionLevel = "protected"
	PermHidden    PermissionLevel = "hidden"
)

// ActionConfig defines a virtual tool that pre-fills arguments for a real tool.
// Actions live in ~/.mini/actions/<name>.yaml.
type ActionConfig struct {
	// Name is the virtual tool name (without server prefix).
	Name string `yaml:"name"`

	// Description shown in list output.
	Description string `yaml:"description"`

	// Server is the upstream to delegate to.
	Server string `yaml:"server"`

	// Tool is the real tool name on the upstream server.
	Tool string `yaml:"tool"`

	// DefaultArgs are merged with call-time args (call-time wins on conflicts).
	// Values support ${ENV_VAR} expansion.
	DefaultArgs map[string]any `yaml:"default_args,omitempty"`

	// Permission overrides the target tool's permission. Default: inherit.
	Permission string `yaml:"permission,omitempty"`
}

// ProjectionConfig defines how to trim tool responses.
type ProjectionConfig struct {
	// Alias replaces the tool's name in list output and call routing. Agents see
	// and use the alias; mini translates back to the real name when forwarding to
	// the upstream. Empty means no alias (default).
	Alias         string         `yaml:"alias,omitempty"          json:"alias,omitempty"`
	Mode          string         `yaml:"mode,omitempty"           json:"mode,omitempty"`
	Include       []string       `yaml:"include,omitempty"        json:"include,omitempty"`
	ExcludeAlways []string       `yaml:"exclude_always,omitempty" json:"exclude_always,omitempty"`
	Passthrough   []string       `yaml:"passthrough,omitempty"    json:"passthrough,omitempty"`
	ArrayLimits  map[string]int `yaml:"array_limits,omitempty"   json:"array_limits,omitempty"`
	StringLimits map[string]int `yaml:"string_limits,omitempty"  json:"string_limits,omitempty"`
	DepthLimit   int            `yaml:"depth_limit,omitempty"    json:"depth_limit,omitempty"`
	StripMarkup  bool           `yaml:"strip_markup,omitempty"   json:"strip_markup,omitempty"`
	Format       string         `yaml:"format,omitempty"         json:"format,omitempty"`
}
