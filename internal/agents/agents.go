package agents

import (
	"os"
	"path/filepath"
	"runtime"
)

type Agent struct {
	Name       string
	ConfigPath string
	// Dir is the agent's own directory: when it exists the agent is installed, even before its
	// MCP config file does.
	Dir     string
	Read    func(path string) (map[string]Server, error)
	Connect func(config []byte, remove []string, mini MiniEntry) ([]byte, error)
	// RemoveDisables is set for agents where removing an entry switches it off instead.
	RemoveDisables bool
}

func Detect() []Agent {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	var found []Agent
	for _, a := range Known(home) {
		if _, err := os.Stat(a.ConfigPath); err == nil {
			found = append(found, a)
		}
	}
	return found
}

func Known(home string) []Agent {
	return []Agent{
		jsonAgent("Claude Code", filepath.Join(home, ".claude.json"), filepath.Join(home, ".claude"), ReadClaude),
		codex(home),
		jsonAgent("Cursor", filepath.Join(home, ".cursor", "mcp.json"), filepath.Join(home, ".cursor"), ReadClaude),
		jsonAgent("Windsurf", filepath.Join(home, ".codeium", "windsurf", "mcp_config.json"), filepath.Join(home, ".codeium", "windsurf"), ReadClaude),
		jsonAgent("Gemini CLI", filepath.Join(home, ".gemini", "settings.json"), filepath.Join(home, ".gemini"), ReadGemini),
		claudeDesktop(home),
	}
}

func codex(home string) Agent {
	dir := os.Getenv("CODEX_HOME")
	if dir == "" {
		dir = filepath.Join(home, ".codex")
	}
	return Agent{
		Name: "Codex", ConfigPath: filepath.Join(dir, "config.toml"), Dir: dir,
		Read: ReadCodex, Connect: connectCodex, RemoveDisables: true,
	}
}

func jsonAgent(name, configPath, dir string, read func(string) (map[string]Server, error)) Agent {
	return Agent{Name: name, ConfigPath: configPath, Dir: dir, Read: read, Connect: connectJSON}
}

func claudeDesktop(home string) Agent {
	var path string
	switch runtime.GOOS {
	case "darwin":
		path = filepath.Join(home, "Library", "Application Support", "Claude", "claude_desktop_config.json")
	case "windows":
		if appData := os.Getenv("APPDATA"); appData != "" {
			path = filepath.Join(appData, "Claude", "claude_desktop_config.json")
		}
	default:
		path = filepath.Join(home, ".config", "Claude", "claude_desktop_config.json")
	}
	if path == "" {
		return Agent{Name: "Claude Desktop", Read: ReadClaude, Connect: connectJSON}
	}
	return jsonAgent("Claude Desktop", path, filepath.Dir(path), ReadClaude)
}
