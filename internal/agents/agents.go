package agents

import (
	"os"
	"path/filepath"
	"runtime"
)

type Agent struct {
	Name       string
	ConfigPath string
	Read       func(path string) (map[string]Server, error)
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
		{Name: "Claude Code", ConfigPath: filepath.Join(home, ".claude.json"), Read: ReadClaude},
		{Name: "Codex", ConfigPath: filepath.Join(home, ".codex", "config.toml"), Read: ReadCodex},
		{Name: "Cursor", ConfigPath: filepath.Join(home, ".cursor", "mcp.json"), Read: ReadClaude},
		{Name: "Windsurf", ConfigPath: filepath.Join(home, ".codeium", "windsurf", "mcp_config.json"), Read: ReadClaude},
		{Name: "Gemini CLI", ConfigPath: filepath.Join(home, ".gemini", "settings.json"), Read: ReadGemini},
		claudeDesktop(home),
	}
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
	return Agent{Name: "Claude Desktop", ConfigPath: path, Read: ReadClaude}
}
