package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/mcpmini/mini/cmd/mini/importers"
	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/ops"
	"github.com/mcpmini/mini/internal/server"
)

// addProbeTimeout bounds only the connectivity check — doPKCEFlow has its own 5-minute OAuth window.
const addProbeTimeout = 15 * time.Second

type stringSlice []string

func (s *stringSlice) String() string     { return strings.Join(*s, ", ") }
func (s *stringSlice) Set(v string) error { *s = append(*s, v); return nil }

const addLongHelp = `Add a server by name with an HTTP/SSE URL or a stdio command, or import
servers from another agent's config.

Flags:
  --url URL             HTTP/SSE server URL
  --header K=V          HTTP header (repeatable)
  --protected TOOL      Mark tool as protected (repeatable)
  --no-connect          Skip the post-add connectivity check and OAuth authorization
  --from-claude PATH    Import from Claude Desktop / Claude Code config JSON
  --from-cursor PATH    Import from Cursor mcp.json
  --from-codex PATH     Import from Codex config.toml
  --from-gemini PATH    Import from Gemini CLI settings.json
  --from-openclaw PATH  Import from OpenClaw (MoltBot) openclaw.json config

The stdio command is positional, given after NAME with no leading flag, e.g.:
mini add gh npx -y server-github`

func newAddCmd(configDir string) *cobra.Command {
	return &cobra.Command{
		Use:                "add NAME [--url URL | CMD ARGS...] [flags]",
		Short:              "Add a server",
		Long:               addLongHelp,
		DisableFlagParsing: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runAdd(configDir, args, cmd.OutOrStdout())
		},
	}
}

func newRmCmd(configDir string) *cobra.Command {
	return &cobra.Command{
		Use:                "rm NAME",
		Aliases:            []string{"remove"},
		Short:              "Remove a server",
		DisableFlagParsing: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runRemove(configDir, args, cmd.OutOrStdout())
		},
	}
}

func runAdd(configDir string, args []string, out io.Writer) error {
	remaining, fromFlags, err := parseImportFlags(args, out)
	if err != nil {
		return err
	}
	if handled, err := handleImportFlags(configDir, fromFlags); handled {
		return err
	}
	return addByName(configDir, remaining, out)
}

func addByName(configDir string, remaining []string, out io.Writer) error {
	if len(remaining) == 0 {
		return usageErrf("usage: mini add NAME [--url URL | CMD ARGS...] [flags]")
	}
	sf, err := parseServerFlags(remaining, out)
	if err != nil {
		return err
	}
	return addNamedServer(configDir, sf, out)
}

type importFlags struct {
	claude, cursor, codex, gemini, openclaw string
}

func parseImportFlags(args []string, out io.Writer) (remaining []string, flags importFlags, err error) {
	fs := flag.NewFlagSet("add", flag.ContinueOnError)
	fs.SetOutput(out)
	fs.StringVar(&flags.claude, "from-claude", "", "import from Claude Desktop / Claude Code config JSON")
	fs.StringVar(&flags.cursor, "from-cursor", "", "import from Cursor mcp.json config")
	fs.StringVar(&flags.codex, "from-codex", "", "import from Codex config.toml")
	fs.StringVar(&flags.gemini, "from-gemini", "", "import from Gemini CLI settings.json")
	fs.StringVar(&flags.openclaw, "from-openclaw", "", "import from OpenClaw (MoltBot) openclaw.json config")
	err = fs.Parse(args)
	return fs.Args(), flags, err
}

type serverFlags struct {
	name, url string
	headers   stringSlice
	protected stringSlice
	cmdArgs   []string
	noConnect bool
}

func parseServerFlags(args []string, out io.Writer) (serverFlags, error) {
	f := serverFlags{name: args[0]}
	fs := flag.NewFlagSet("add-server", flag.ContinueOnError)
	fs.SetOutput(out)
	fs.StringVar(&f.url, "url", "", "HTTP/SSE server URL")
	fs.Var(&f.headers, "header", "HTTP header as Key=Value (repeatable)")
	fs.Var(&f.protected, "protected", "tool name to mark protected (repeatable)")
	fs.BoolVar(&f.noConnect, "no-connect", false, "skip the post-add connectivity check and OAuth authorization")
	if err := fs.Parse(args[1:]); err != nil {
		return serverFlags{}, err
	}
	f.cmdArgs = fs.Args()
	return f, nil
}

func handleImportFlags(configDir string, f importFlags) (handled bool, err error) {
	switch {
	case f.claude != "":
		return true, importers.ImportFromClaude(configDir, f.claude)
	case f.cursor != "":
		return true, importers.ImportFromCursor(configDir, f.cursor)
	case f.codex != "":
		return true, importers.ImportFromCodex(configDir, f.codex)
	case f.gemini != "":
		return true, importers.ImportFromGemini(configDir, f.gemini)
	case f.openclaw != "":
		return true, importers.ImportFromOpenClaw(configDir, f.openclaw)
	default:
		return false, nil
	}
}

func addNamedServer(configDir string, sf serverFlags, out io.Writer) error {
	if sf.url != "" {
		if err := importers.WriteServerYAML(configDir, sf.name, httpServerYAML(sf.name, sf.url, sf.headers, sf.protected)); err != nil {
			return err
		}
		if !sf.noConnect {
			connectAndAuthorizeIfNeeded(configDir, sf.name, out)
		}
		return nil
	}
	if len(sf.cmdArgs) == 0 {
		return usageErrf("provide --url or a command after NAME")
	}
	return importers.WriteServerYAML(configDir, sf.name, stdioServerYAML(sf.name, sf.cmdArgs, sf.protected))
}

func httpServerYAML(name, url string, headers, protected stringSlice) importers.ServerYAML {
	return importers.ServerYAML{
		Name:        name,
		Transport:   "http",
		URL:         url,
		Headers:     parseHeaders(headers),
		Permissions: permissionsYAML(protected),
	}
}

func stdioServerYAML(name string, rest []string, protected stringSlice) importers.ServerYAML {
	return importers.ServerYAML{
		Name:        name,
		Command:     rest[0],
		Args:        rest[1:],
		Permissions: permissionsYAML(protected),
	}
}

func permissionsYAML(protected stringSlice) *importers.PermissionsYAML {
	if len(protected) == 0 {
		return nil
	}
	return &importers.PermissionsYAML{Protected: []string(protected)}
}

func runRemove(configDir string, args []string, out io.Writer) error {
	if len(args) == 0 {
		return usageErrf("usage: mini rm NAME")
	}
	name := args[0]
	if err := ops.DeleteServer(configDir, name); err != nil {
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
	if scp == nil || !scp.IsHTTPTransport() {
		return
	}
	sc := *scp
	// Only probe if Auth is still unknown — config.Load already merges in bundled/detected
	// auth, so a non-nil Auth here means there's nothing left to discover.
	if sc.Auth == nil {
		sc = probeAndReload(configDir, sc, out)
	}
	// A hand-set header means the user already chose their own auth — never override it
	// with an interactive OAuth flow, even for a known vendor's bundled default.
	if sc.Auth == nil || sc.Auth.Type != config.AuthTypeOAuth2 || len(sc.Headers) > 0 {
		return
	}
	authorizeServer(authorizeParams{configDir: configDir, name: name, sc: sc, out: out})
}

func loadServerConfigForAdd(configDir, name string) (*config.ServerConfig, error) {
	_, servers, err := config.Load(configDir)
	if err != nil {
		return nil, err
	}
	return config.FindServer(servers, name), nil
}

func probeAndReload(configDir string, sc config.ServerConfig, out io.Writer) config.ServerConfig {
	connectErr := probeConnection(configDir, sc)
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

func probeConnection(configDir string, sc config.ServerConfig) error {
	cfg, _, err := config.Load(configDir)
	if err != nil {
		return err
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := server.NewWithConfigDir(cfg, configDir, logger)
	defer srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), addProbeTimeout)
	defer cancel()
	return srv.AddUpstream(ctx, sc)
}

type authorizeParams struct {
	configDir string
	name      string
	sc        config.ServerConfig
	out       io.Writer
}

func authorizeServer(p authorizeParams) {
	cfg, _, err := config.Load(p.configDir)
	if err != nil {
		fmt.Fprintf(p.out, "warning: reload config for auth: %v\n", err)
		return
	}
	fmt.Fprintf(p.out, "%s requires OAuth authorization\n", p.name)
	token, err := doPKCEFlow(pkceFlowParams{
		configDir:  p.configDir,
		serverName: p.name,
		opener:     authOpener(p.sc.Auth.BrowserCmd, cfg.BrowserCommand, cfg.DisableAuthBrowserOpen),
		sc:         &p.sc,
	})
	if err != nil {
		fmt.Fprintf(p.out, "note: automatic authorization failed (%v); run `mini auth %s` to retry\n", err, p.name)
		return
	}
	printAuthResult(p.name, token.Expiry)
}
