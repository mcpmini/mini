package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/mcpmini/mini/internal/config"
)

type stringSlice []string

func (s *stringSlice) String() string     { return strings.Join(*s, ", ") }
func (s *stringSlice) Set(v string) error { *s = append(*s, v); return nil }

func runAdd(configDir string, args []string) {
	fs := flag.NewFlagSet("add", flag.ExitOnError)
	url := fs.String("url", "", "HTTP/SSE server URL")
	fromClaude := fs.String("from-claude", "", "import from Claude Desktop / Claude Code config JSON")
	fromCursor := fs.String("from-cursor", "", "import from Cursor mcp.json config")
	fromCodex := fs.String("from-codex", "", "import from Codex config.toml")
	fromGemini := fs.String("from-gemini", "", "import from Gemini CLI settings.json")
	fromOpenClaw := fs.String("from-openclaw", "", "import from OpenClaw (MoltBot) openclaw.json config")
	var headers, protected stringSlice
	fs.Var(&headers, "header", "HTTP header as Key=Value (repeatable)")
	fs.Var(&protected, "protected", "tool name to mark protected (repeatable)")
	fs.Parse(args) //nolint:errcheck

	if handleImportFlags(configDir, *fromClaude, *fromCursor, *fromCodex, *fromGemini, *fromOpenClaw) {
		return
	}
	addNamedServer(configDir, args, url, &headers, &protected, fs)
}

func handleImportFlags(configDir, fromClaude, fromCursor, fromCodex, fromGemini, fromOpenClaw string) bool {
	switch {
	case fromClaude != "":
		importFromClaude(configDir, fromClaude)
	case fromCursor != "":
		importFromCursor(configDir, fromCursor)
	case fromCodex != "":
		importFromCodex(configDir, fromCodex)
	case fromGemini != "":
		importFromGemini(configDir, fromGemini)
	case fromOpenClaw != "":
		importFromOpenClaw(configDir, fromOpenClaw)
	default:
		return false
	}
	return true
}

func addNamedServer(configDir string, args []string, url *string, headers, protected *stringSlice, fs *flag.FlagSet) {
	if len(args) == 0 {
		fatalf("usage: mini add NAME [--url URL | CMD ARGS...] [flags]")
	}
	name := args[0]
	fs.Parse(args[1:]) //nolint:errcheck
	rest := fs.Args()

	sc := buildServerYAML(name, *url, rest, *headers, *protected)
	writeServerYAML(configDir, name, sc)
}

func buildServerYAML(name, url string, rest []string, headers, protected stringSlice) serverYAML {
	var sc serverYAML
	sc.Name = name
	if url != "" {
		sc.Transport = "http"
		sc.URL = url
		sc.Headers = parseHeaders(headers)
	} else {
		if len(rest) == 0 {
			fatalf("provide --url or a command after NAME")
		}
		sc.Command = rest[0]
		sc.Args = rest[1:]
	}
	if len(protected) > 0 {
		sc.Permissions = &permissionsYAML{Protected: []string(protected)}
	}
	return sc
}

func runRemove(configDir string, args []string) {
	if len(args) == 0 {
		fatalf("usage: mini rm NAME")
	}
	if !config.ValidServerName.MatchString(args[0]) {
		fatalf("invalid server name %q: must match ^[a-zA-Z0-9_-]+$", args[0])
	}
	path := filepath.Join(configDir, "servers", args[0]+".yaml")
	if err := os.Remove(path); err != nil {
		fatalf("remove %s: %v", path, err)
	}
	fmt.Printf("removed %s\n", args[0])
}

func parseHeaders(pairs []string) map[string]string {
	out := make(map[string]string, len(pairs))
	for _, p := range pairs {
		k, v, _ := strings.Cut(p, "=")
		out[strings.TrimSpace(k)] = strings.TrimSpace(v)
	}
	return out
}
