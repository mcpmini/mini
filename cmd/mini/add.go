package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/mcpmini/mini/internal/agents"
	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/ops"
	"github.com/mcpmini/mini/internal/server"
)

// addProbeTimeout bounds only the connectivity check — doPKCEFlow has its own 5-minute OAuth window.
const addProbeTimeout = 15 * time.Second

type stringSlice []string

func (s *stringSlice) String() string     { return strings.Join(*s, ", ") }
func (s *stringSlice) Set(v string) error { *s = append(*s, v); return nil }
func (s *stringSlice) Type() string       { return "string" }

const addLongHelp = `Add an HTTP server with --url, import another client's config with one
--from-* flag, or use -- before a stdio command so every child argument is
preserved unchanged.

Examples:
  mini add api --url https://example.com/mcp
  mini add local --protected delete -- npx -y server-package`

func newAddCmd(opts *rootOptions) *cobra.Command {
	sf := serverFlags{}
	imports := importFlags{}
	cmd := &cobra.Command{
		Use:   "add NAME (--url URL | -- CMD [ARGS...])",
		Short: "Add a server",
		Long:  addLongHelp,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runAddParsed(addParams{
				configDir: opts.configDir,
				args:      args,
				dash:      cmd.ArgsLenAtDash(),
				server:    sf,
				imports:   imports,
				out:       cmd.OutOrStdout(),
				errOut:    cmd.ErrOrStderr(),
			})
		},
	}
	bindAddFlags(cmd, &sf, &imports)
	return cmd
}

func bindAddFlags(cmd *cobra.Command, sf *serverFlags, imports *importFlags) {
	flags := cmd.Flags()
	flags.StringVar(&sf.url, "url", "", "HTTP/SSE server URL")
	flags.Var(&sf.headers, "header", "HTTP header as Key=Value (repeatable)")
	flags.Var(&sf.protected, "protected", "tool name to mark protected (repeatable)")
	flags.BoolVar(&sf.noConnect, "no-connect", false, "skip connectivity check and OAuth authorization")
	flags.StringVar(&imports.claude, "from-claude", "", "import from Claude Desktop / Claude Code config JSON")
	flags.StringVar(&imports.cursor, "from-cursor", "", "import from Cursor mcp.json config")
	flags.StringVar(&imports.codex, "from-codex", "", "import from Codex config.toml")
	flags.StringVar(&imports.gemini, "from-gemini", "", "import from Gemini CLI settings.json")
	flags.StringVar(&imports.openclaw, "from-openclaw", "", "import from OpenClaw (MoltBot) openclaw.json config")
}

func newRmCmd(opts *rootOptions) *cobra.Command {
	return &cobra.Command{
		Use:     "rm NAME",
		Aliases: []string{"remove"},
		Short:   "Remove a server",
		Args:    usageArgs(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runRemove(opts.configDir, args, cmd.OutOrStdout())
		},
	}
}

type addParams struct {
	configDir string
	args      []string
	dash      int
	server    serverFlags
	imports   importFlags
	out       io.Writer
	errOut    io.Writer
}

func runAddParsed(p addParams) error {
	if handled, err := runAddImport(p); handled {
		return err
	}
	return runAddServer(p)
}

func runAddImport(p addParams) (bool, error) {
	if importCount(p.imports) > 0 {
		if importCount(p.imports) != 1 || len(p.args) != 0 || p.dash >= 0 || p.server.hasServerOptions() {
			return true, usageErrf("import mode accepts exactly one --from-* flag and no server arguments")
		}
		return true, importFromFlag(p)
	}
	return false, nil
}

func runAddServer(p addParams) error {
	if len(p.args) == 0 {
		return usageErrf("provide NAME with --url, or NAME -- CMD [ARGS...]")
	}
	p.server.name = p.args[0]
	if p.server.url != "" {
		if p.dash >= 0 || len(p.args) != 1 {
			return usageErrf("--url cannot be combined with a stdio command")
		}
		return addNamedServer(p.configDir, p.server, p.out)
	}
	if p.dash != 1 || len(p.args) < 2 {
		return usageErrf("stdio servers require NAME -- CMD [ARGS...]")
	}
	p.server.cmdArgs = p.args[1:]
	return addNamedServer(p.configDir, p.server, p.out)
}

type importFlags struct {
	claude, cursor, codex, gemini, openclaw string
}

func importCount(f importFlags) int {
	count := 0
	for _, path := range []string{f.claude, f.cursor, f.codex, f.gemini, f.openclaw} {
		if path != "" {
			count++
		}
	}
	return count
}

type serverFlags struct {
	name, url string
	headers   stringSlice
	protected stringSlice
	cmdArgs   []string
	noConnect bool
}

func (f serverFlags) hasServerOptions() bool {
	return f.url != "" || len(f.headers) > 0 || len(f.protected) > 0 || f.noConnect
}

type importSource struct {
	path string
	read func(path string) (map[string]agents.Server, error)
	tip  string
}

const (
	headersTip = "tip: replace any literal tokens in headers with ${ENV_VAR} references"
	envTip     = "tip: replace any literal tokens in env with ${ENV_VAR} references"
)

func selectedImport(f importFlags) importSource {
	switch {
	case f.claude != "":
		return importSource{path: f.claude, read: agents.ReadClaude, tip: headersTip}
	case f.cursor != "":
		return importSource{path: f.cursor, read: agents.ReadClaude, tip: headersTip}
	case f.codex != "":
		return importSource{path: f.codex, read: agents.ReadCodex, tip: envTip}
	case f.gemini != "":
		return importSource{path: f.gemini, read: agents.ReadGemini, tip: headersTip}
	default:
		return importSource{path: f.openclaw, read: agents.ReadOpenClaw, tip: envTip}
	}
}

func importFromFlag(p addParams) error {
	src := selectedImport(p.imports)
	servers, err := src.read(src.path)
	if err != nil {
		return err
	}
	if len(servers) == 0 {
		fmt.Fprintf(p.out, "no MCP servers found in %s\n", src.path)
		return nil
	}
	imp := serverImport{configDir: p.configDir, source: src.path, out: p.out, errOut: p.errOut}
	added, failed := imp.addAll(servers)
	if len(added) > 0 {
		fmt.Fprintln(p.out, src.tip)
	}
	if failed > 0 {
		return fmt.Errorf("%d of %d servers in %s could not be added", failed, len(servers), src.path)
	}
	return nil
}

func addNamedServer(configDir string, sf serverFlags, out io.Writer) error {
	if sf.url == "" && len(sf.cmdArgs) == 0 {
		return usageErrf("provide --url or a command after NAME")
	}
	added, err := ops.AddServer(configDir, sf.serverConfig())
	if errors.Is(err, ops.ErrAlreadyConfigured) {
		return fmt.Errorf("%s is already configured; run `mini rm %s` first to replace it", sf.name, sf.name)
	}
	if err != nil {
		return err
	}
	printAdded(out, added)
	if sf.url != "" && !sf.noConnect {
		connectAndAuthorizeIfNeeded(configDir, sf.name, out)
	}
	return nil
}

func (f serverFlags) serverConfig() config.ServerConfig {
	sc := config.ServerConfig{Name: f.name}
	if len(f.protected) > 0 {
		sc.Permissions = &config.PermissionsConfig{Protected: f.protected}
	}
	if f.url != "" {
		sc.Transport = "http"
		sc.URL = f.url
		sc.Headers = parseHeaders(f.headers)
		return sc
	}
	sc.Command = f.cmdArgs[0]
	sc.Args = f.cmdArgs[1:]
	return sc
}

func runRemove(configDir string, args []string, out io.Writer) error {
	if len(args) != 1 {
		return usageErrf("usage: mini rm NAME")
	}
	name := args[0]
	if err := ops.RemoveServer(configDir, name); err != nil {
		return err
	}
	fmt.Fprintf(out, "removed %s\n", name)
	return nil
}

func parseHeaders(pairs []string) map[string]string {
	out := make(map[string]string, len(pairs))
	for _, p := range pairs {
		k, v, _ := strings.Cut(p, "=")
		out[strings.TrimSpace(k)] = strings.TrimSpace(v)
	}
	return out
}

func connectAndAuthorizeIfNeeded(configDir, name string, out io.Writer) {
	scp, err := loadServerConfigForAdd(configDir, name)
	if err != nil {
		fmt.Fprintf(out, "warning: could not reload config to check for required auth: %v\n", err)
		return
	}
	if scp == nil {
		return
	}
	if scp.ProjectionsErr != nil {
		warnUnprojected(out, *scp.ProjectionsErr)
	}
	if !scp.IsHTTPTransport() {
		return
	}
	sc := *scp
	if authUndiscovered(sc) {
		sc = probeAndReload(configDir, sc, out)
	}
	// Static auth (the auth header or auth.token) means the user chose their own credentials;
	// never override it with a browser login.
	if !sc.UsesOAuthLogin() {
		return
	}
	authorizeServer(authorizeParams{configDir: configDir, name: name, sc: sc, out: out})
}

func loadServerConfigForAdd(configDir, name string) (*config.ServerConfig, error) {
	sc, err := config.LoadServer(configDir, name)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &sc, nil
}

func probeAndReload(configDir string, sc config.ServerConfig, out io.Writer) config.ServerConfig {
	ctx, cancel := context.WithTimeout(context.Background(), addProbeTimeout)
	defer cancel()
	connectErr := server.ProbeServer(ctx, configDir, sc)
	// Connecting may have triggered OAuth detection (markOAuthIfRequired) — reload to see it merged in.
	reloaded, err := loadServerConfigForAdd(configDir, sc.Name)
	if err != nil || reloaded == nil {
		return sc
	}
	switch {
	case connectErr == nil:
		fmt.Fprintf(out, "connected to %s\n", sc.Name)
	case reloaded.Auth != nil && reloaded.Auth.Type == config.AuthTypeOAuth2:
		// authorizeServer reports this case next; "could not connect" here would be misleading.
	default:
		fmt.Fprintf(out, "note: could not connect to %s yet; run `mini test` to retry\n", sc.Name)
	}
	return *reloaded
}

// LoadServer and LoadServers already merge bundled and detected auth, so a non-nil Auth leaves nothing to discover.
func authUndiscovered(sc config.ServerConfig) bool {
	return sc.IsHTTPTransport() && sc.Auth == nil
}

type authorizeParams struct {
	configDir string
	name      string
	sc        config.ServerConfig
	out       io.Writer
}

func authorizeServer(p authorizeParams) {
	cfg, err := config.LoadMain(p.configDir)
	if err != nil {
		fmt.Fprintf(p.out, "warning: reload config for auth: %v\n", err)
		return
	}
	fmt.Fprintf(p.out, "%s requires OAuth authorization\n", p.name)
	if _, err := logIn(logInParams{configDir: p.configDir, cfg: cfg, sc: &p.sc, out: p.out}); err != nil {
		fmt.Fprintf(p.out, "note: automatic authorization failed (%v); run `mini auth %s` to retry\n", err, p.name)
	}
}
