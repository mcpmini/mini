//go:build evals

package evals

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func rawAllowedTools(servers map[string]string, extraBuiltins string) string {
	var names []string
	for serverName, dir := range servers {
		entries, _ := os.ReadDir(dir)
		for _, e := range entries {
			if !e.IsDir() && strings.HasSuffix(e.Name(), ".json") && !strings.HasSuffix(e.Name(), ".schema.json") {
				tool := strings.TrimSuffix(e.Name(), ".json")
				names = append(names, "mcp__"+serverName+"__"+tool)
			}
		}
	}
	if extraBuiltins != "" {
		names = append(names, strings.Split(extraBuiltins, ",")...)
	}
	return strings.Join(names, ",")
}

func miniMCPAllowedTools(extraBuiltins string) string {
	base := "mcp__mini__list,mcp__mini__call,mcp__mini__perm_call,mcp__mini__config"
	if extraBuiltins != "" {
		return base + "," + extraBuiltins
	}
	return base
}

func cliAllowedTools(extraBuiltins string) string {
	if extraBuiltins != "" {
		return "Bash," + extraBuiltins
	}
	return "Bash"
}

func rawMCPConfig(env *Env, r *Runner, servers map[string]string, callLogDir string) (string, error) {
	mcpServers := make(map[string]any, len(servers))
	for name, fixtureDir := range servers {
		mcpServers[name] = map[string]any{
			"command": r.FakemcpBin,
			"args":    fakemcpArgs(fixtureDir, callLogDir, name),
		}
	}
	return writeMCPConfig(env, map[string]any{"mcpServers": mcpServers})
}

func miniMCPConfig(env *Env, r *Runner, servers map[string]string, callLogDir string, format int) (string, error) {
	configDir, err := buildMiniConfigDir(env, r, servers, callLogDir, format)
	if err != nil {
		return "", err
	}
	return writeMCPConfig(env, map[string]any{
		"mcpServers": map[string]any{
			"mini": map[string]any{
				"command": r.MiniBin,
				"args":    []string{"--config", configDir, "serve", "--standalone", "--log-level", "error"},
			},
		},
	})
}

func miniCLIConfigDir(env *Env, r *Runner, servers map[string]string, callLogDir string, format int) (string, error) {
	return buildMiniConfigDir(env, r, servers, callLogDir, format)
}

func buildMiniConfigDir(env *Env, r *Runner, servers map[string]string, callLogDir string, format int) (string, error) {
	configDir := env.TempDir()
	if err := os.WriteFile(filepath.Join(configDir, "config.yaml"), []byte(miniConfigYAML(format)), 0600); err != nil {
		return "", err
	}
	if err := writeServersYAML(configDir, r.FakemcpBin, servers, callLogDir); err != nil {
		return "", err
	}
	if format != fmtPassthrough {
		if err := writeBundledProjections(configDir, r, servers); err != nil {
			return "", err
		}
	}
	return configDir, nil
}

func miniConfigYAML(format int) string {
	switch format {
	case fmtPassthrough:
		return "inline_threshold: 9999999\nresponse_format: json\n"
	case fmtProjected:
		return "inline_threshold: 50000\nresponse_format: json\n"
	case fmtLines:
		return "inline_threshold: 50000\nresponse_format: mini\n"
	default:
		panic(fmt.Sprintf("unknown format %d", format))
	}
}

func writeServersYAML(configDir, fakemcpBin string, servers map[string]string, callLogDir string) error {
	serverDir := filepath.Join(configDir, "servers")
	if err := os.MkdirAll(serverDir, 0700); err != nil {
		return err
	}
	for name, fixtureDir := range servers {
		yaml := buildServerYAML(fakemcpBin, name, fixtureDir, callLogDir)
		if err := os.WriteFile(filepath.Join(serverDir, name+".yaml"), []byte(yaml), 0600); err != nil {
			return err
		}
	}
	return nil
}

func buildServerYAML(fakemcpBin, name, fixtureDir, callLogDir string) string {
	y := "name: " + name + "\ncommand: " + fakemcpBin + "\nargs:\n  - --fixtures\n  - " + fixtureDir + "\n"
	if callLogDir != "" {
		y += "  - --call-log\n  - " + filepath.Join(callLogDir, name+".log") + "\n"
	}
	return y
}

func writeBundledProjections(configDir string, r *Runner, servers map[string]string) error {
	projDir := filepath.Join(configDir, "projections")
	if err := os.MkdirAll(projDir, 0700); err != nil {
		return err
	}
	srcDir := filepath.Join(r.RepoRoot, "internal", "defaults", "projections")
	for name := range servers {
		data, err := os.ReadFile(filepath.Join(srcDir, name+".yaml"))
		if err != nil {
			continue
		}
		if err := os.WriteFile(filepath.Join(projDir, name+".yaml"), data, 0600); err != nil {
			return err
		}
	}
	return nil
}

func writeMCPConfig(env *Env, cfg map[string]any) (string, error) {
	b, _ := json.Marshal(cfg)
	path := filepath.Join(env.TempDir(), "mcp.json")
	return path, os.WriteFile(path, b, 0600)
}

func fakemcpArgs(fixtureDir, callLogDir, serverName string) []string {
	args := []string{"--fixtures", fixtureDir}
	if callLogDir != "" {
		args = append(args, "--call-log", filepath.Join(callLogDir, serverName+".log"))
	}
	return args
}

func writeMiniWrapper(env *Env, miniBin, configDir string) (string, error) {
	dir := env.TempDir()
	script := "#!/bin/sh\nexec " + miniBin + " --config " + configDir + " \"$@\"\n"
	return dir, os.WriteFile(filepath.Join(dir, "mini"), []byte(script), 0755)
}

func freshWorkDir(env *Env, workSrcDir string) (string, error) {
	if workSrcDir == "" {
		return "", nil
	}
	d := env.DebugDir("work")
	return d, copyDir(workSrcDir, d)
}
