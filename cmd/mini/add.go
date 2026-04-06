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
	// First pass: import flags (--from-*) come before NAME and are mutually exclusive with it.
	importFS := flag.NewFlagSet("add", flag.ContinueOnError)
	importFS.SetOutput(out)
	fromClaude := importFS.String("from-claude", "", "import from Claude Desktop / Claude Code config JSON")
	fromCursor := importFS.String("from-cursor", "", "import from Cursor mcp.json config")
	fromCodex := importFS.String("from-codex", "", "import from Codex config.toml")
	fromGemini := importFS.String("from-gemini", "", "import from Gemini CLI settings.json")
	fromOpenClaw := importFS.String("from-openclaw", "", "import from OpenClaw (MoltBot) openclaw.json config")
	if err := importFS.Parse(args); err != nil {
		return err
	}

	if handled, err := handleImportFlags(configDir, *fromClaude, *fromCursor, *fromCodex, *fromGemini, *fromOpenClaw); handled {
		return err
	}

	// Second pass: NAME is first positional arg; server flags (--url, --header, --protected) follow it.
	remaining := importFS.Args()
	if len(remaining) == 0 {
		return fmt.Errorf("usage: mini add NAME [--url URL | CMD ARGS...] [flags]")
	}
	name := remaining[0]

	serverFS := flag.NewFlagSet("add-server", flag.ContinueOnError)
	serverFS.SetOutput(out)
	url := serverFS.String("url", "", "HTTP/SSE server URL")
	var headers, protected stringSlice
	serverFS.Var(&headers, "header", "HTTP header as Key=Value (repeatable)")
	serverFS.Var(&protected, "protected", "tool name to mark protected (repeatable)")
	if err := serverFS.Parse(remaining[1:]); err != nil {
		return err
	}

	return addNamedServer(configDir, name, serverFS.Args(), *url, headers, protected)
}

func handleImportFlags(configDir, fromClaude, fromCursor, fromCodex, fromGemini, fromOpenClaw string) (handled bool, err error) {
	switch {
	case fromClaude != "":
		return true, importers.ImportFromClaude(configDir, fromClaude)
	case fromCursor != "":
		return true, importers.ImportFromCursor(configDir, fromCursor)
	case fromCodex != "":
		return true, importers.ImportFromCodex(configDir, fromCodex)
	case fromGemini != "":
		return true, importers.ImportFromGemini(configDir, fromGemini)
	case fromOpenClaw != "":
		return true, importers.ImportFromOpenClaw(configDir, fromOpenClaw)
	default:
		return false, nil
	}
}

func addNamedServer(configDir, name string, rest []string, url string, headers, protected stringSlice) error {
	if url != "" {
		return importers.WriteServerYAML(configDir, name, importers.ServerYAML{
			Name:        name,
			Transport:   "http",
			URL:         url,
			Headers:     parseHeaders(headers),
			Permissions: permissionsYAML(protected),
		})
	}
	if len(rest) == 0 {
		return fmt.Errorf("provide --url or a command after NAME")
	}
	return importers.WriteServerYAML(configDir, name, importers.ServerYAML{
		Name:        name,
		Command:     rest[0],
		Args:        rest[1:],
		Permissions: permissionsYAML(protected),
	})
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
