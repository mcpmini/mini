package main

import (
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/mcpmini/mini/cmd/mini/importers"
	"github.com/mcpmini/mini/internal/ops"
)

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
