package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"time"

	"github.com/mcpmini/mini/cmd/mini/importers"
	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/ops"
	"github.com/mcpmini/mini/internal/server"
)

const addConnectTimeout = 15 * time.Second

type stringSlice []string

func (s *stringSlice) String() string     { return strings.Join(*s, ", ") }
func (s *stringSlice) Set(v string) error { *s = append(*s, v); return nil }

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
		return fmt.Errorf("usage: mini add NAME [--url URL | CMD ARGS...] [flags]")
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
		return fmt.Errorf("provide --url or a command after NAME")
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
		return fmt.Errorf("usage: mini rm NAME")
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

// connectAndAuthorizeIfNeeded probes a freshly-added HTTP server and, if it turns out to need
// OAuth (either just-discovered via a 401, or already known via a bundled default like
// atlassian/linear/slack), immediately runs the same interactive authorization flow `mini auth`
// uses — so `mini add` leaves the server ready to use in one step. Unlike `mini auth`, failure
// here must never abort the process: the server's config was already written successfully, so
// a failed auto-authorize attempt is reported and left for a later `mini auth` retry.
func connectAndAuthorizeIfNeeded(configDir, name string, out io.Writer) {
	sc, ok := loadServerConfigForAdd(configDir, name)
	if !ok || !sc.IsHTTPTransport() {
		return
	}
	if sc.Auth == nil {
		sc = probeAndReload(configDir, sc, out)
	}
	if sc.Auth == nil || sc.Auth.Type != "oauth2" {
		return
	}
	authorizeServer(authorizeParams{configDir: configDir, name: name, sc: sc, out: out})
}

func loadServerConfigForAdd(configDir, name string) (config.ServerConfig, bool) {
	_, servers, err := config.Load(configDir)
	if err != nil {
		return config.ServerConfig{}, false
	}
	sc := config.FindServer(servers, name)
	if sc == nil {
		return config.ServerConfig{}, false
	}
	return *sc, true
}

// probeAndReload attempts a connection and returns the reloaded config, which picks up any
// auth: type: oauth2 that connecting just caused to be persisted. Reports the outcome, except
// when OAuth was the reason for failure — authorizeServer reports that case instead, since a
// follow-up auth flow is about to run and a "could not connect" message would be misleading.
func probeAndReload(configDir string, sc config.ServerConfig, out io.Writer) config.ServerConfig {
	connectErr := probeConnection(configDir, sc)
	reloaded, ok := loadServerConfigForAdd(configDir, sc.Name)
	if !ok {
		return sc
	}
	switch {
	case connectErr == nil:
		fmt.Fprintf(out, "connected to %s\n", sc.Name)
	case reloaded.Auth != nil && reloaded.Auth.Type == "oauth2":
	default:
		fmt.Fprintf(out, "note: could not connect to %s yet; run `mini test` to retry\n", sc.Name)
	}
	return reloaded
}

func probeConnection(configDir string, sc config.ServerConfig) error {
	cfg, _, err := config.Load(configDir)
	if err != nil {
		return err
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := server.NewWithConfigDir(cfg, configDir, logger)
	defer srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), addConnectTimeout)
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
