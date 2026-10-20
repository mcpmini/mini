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

func (r *Runner) rawMCPConfig(env *Env, servers map[string]string, callLogDir string) (string, error) {
	mcpServers := make(map[string]any, len(servers))
	for name, fixtureDir := range servers {
		mcpServers[name] = map[string]any{
			"command": r.FakemcpBin,
			"args":    fakemcpArgs(fixtureDir, callLogDir, name),
		}
	}
	return writeMCPConfig(env, map[string]any{"mcpServers": mcpServers})
}

type miniConfigParams struct {
	env        *Env
	servers    map[string]string
	callLogDir string
	format     format
}

func (r *Runner) miniMCPConfig(p miniConfigParams) (string, error) {
	configDir, err := r.buildMiniConfigDir(p)
	if err != nil {
		return "", err
	}
	return writeMCPConfig(p.env, map[string]any{
		"mcpServers": map[string]any{
			"mini": map[string]any{
				"command": r.MiniBin,
				"args": []string{
					"--config",
					configDir,
					"connect",
					"--standalone",
					"--tool-mode",
					"compact",
					"--log-level",
					"error",
				},
			},
		},
	})
}

func (r *Runner) miniCLIConfigDir(p miniConfigParams) (string, error) {
	return r.buildMiniConfigDir(p)
}

func (r *Runner) buildMiniConfigDir(p miniConfigParams) (string, error) {
	configDir := p.env.TempDir()
	if err := os.WriteFile(
		filepath.Join(configDir, "config.yaml"),
		[]byte(miniConfigYAML(p.format)),
		0o600,
	); err != nil {
		return "", err
	}
	if err := writeServersYAML(writeServersYAMLParams{
		configDir: configDir, fakemcpBin: r.FakemcpBin, servers: p.servers, callLogDir: p.callLogDir,
	}); err != nil {
		return "", err
	}
	if p.format != fmtPassthrough {
		if err := writeBundledProjections(configDir, r, p.servers); err != nil {
			return "", err
		}
	}
	return configDir, nil
}

func miniConfigYAML(format format) string {
	switch format {
	case fmtToon:
		return "response_format: toon\n"
	case fmtPassthrough, fmtProjected:
		return "response_format: json\n"
	default:
		panic(fmt.Sprintf("unknown format %d", format))
	}
}

type writeServersYAMLParams struct {
	configDir  string
	fakemcpBin string
	servers    map[string]string
	callLogDir string
}

func writeServersYAML(p writeServersYAMLParams) error {
	serverDir := filepath.Join(p.configDir, "servers")
	if err := os.MkdirAll(serverDir, 0o700); err != nil {
		return err
	}
	for name, fixtureDir := range p.servers {
		yaml := buildServerYAML(buildServerYAMLParams{
			fakemcpBin: p.fakemcpBin, name: name, fixtureDir: fixtureDir, callLogDir: p.callLogDir,
		})
		if err := os.WriteFile(filepath.Join(serverDir, name+".yaml"), []byte(yaml), 0o600); err != nil {
			return err
		}
	}
	return nil
}

type buildServerYAMLParams struct {
	fakemcpBin string
	name       string
	fixtureDir string
	callLogDir string
}

func buildServerYAML(p buildServerYAMLParams) string {
	y := "command: " + p.fakemcpBin + "\nargs:\n  - --fixtures\n  - " + p.fixtureDir + "\n"
	if p.callLogDir != "" {
		y += "  - --call-log\n  - " + filepath.Join(p.callLogDir, p.name+".log") + "\n"
	}
	return y
}

func writeBundledProjections(configDir string, r *Runner, servers map[string]string) error {
	serversDir := filepath.Join(configDir, "servers")
	if err := os.MkdirAll(serversDir, 0o700); err != nil {
		return err
	}
	srcDir := filepath.Join(r.RepoRoot, "internal", "defaults", "projections")
	for name := range servers {
		if err := writeBundledProjection(srcDir, serversDir, name); err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return err
		}
	}
	return nil
}

func writeBundledProjection(srcDir, serversDir, name string) error {
	data, err := os.ReadFile(filepath.Join(srcDir, name+".yaml"))
	if err != nil {
		return err
	}
	path := filepath.Join(serversDir, name+".yaml")
	server, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(server, inlineProjections(data)...), 0o600)
}

func inlineProjections(bundled []byte) []byte {
	lines := strings.Split(strings.TrimRight(string(bundled), "\n"), "\n")
	for i, line := range lines {
		if line != "" {
			lines[i] = "  " + line
		}
	}
	return []byte("projections:\n" + strings.Join(lines, "\n") + "\n")
}

func (r *Runner) proxyMCPConfig(p miniConfigParams) (string, error) {
	configDir, err := r.writeMiniProxyConfig(p)
	if err != nil {
		return "", err
	}
	return writeMCPConfig(p.env, miniServerConfig(r.MiniBin, configDir, "connect", "--log-level", "error"))
}

func (r *Runner) writeMiniProxyConfig(p miniConfigParams) (string, error) {
	configDir := p.env.TempDir()
	if err := os.WriteFile(
		filepath.Join(configDir, "config.yaml"),
		[]byte(miniConfigYAML(p.format)),
		0o600,
	); err != nil {
		return "", err
	}
	if err := writeServersYAML(writeServersYAMLParams{
		configDir: configDir, fakemcpBin: r.FakemcpBin, servers: p.servers, callLogDir: p.callLogDir,
	}); err != nil {
		return "", err
	}
	if p.format == fmtPassthrough {
		return configDir, nil
	}
	if err := writeBundledProjections(configDir, r, p.servers); err != nil {
		return "", err
	}
	return configDir, nil
}

func miniServerConfig(miniBin, configDir string, args ...string) map[string]any {
	return map[string]any{
		"mcpServers": map[string]any{
			"mini": map[string]any{
				"command": miniBin,
				"args":    append([]string{"--config", configDir}, args...),
			},
		},
	}
}

func writeMCPConfig(env *Env, cfg map[string]any) (string, error) {
	b, _ := json.Marshal(cfg)
	path := filepath.Join(env.TempDir(), "mcp.json")
	return path, os.WriteFile(path, b, 0o600)
}

func fakemcpArgs(fixtureDir, callLogDir, serverName string) []string {
	args := []string{"--fixtures", fixtureDir}
	if callLogDir != "" {
		args = append(args, "--call-log", filepath.Join(callLogDir, serverName+".log"))
	}
	return args
}

func proxyAllowedTools(servers map[string]string, extraBuiltins string) string {
	names := []string{"mcp__mini__config", "mcp__mini__read"}
	for serverName, dir := range servers {
		entries, _ := os.ReadDir(dir)
		for _, e := range entries {
			if !e.IsDir() && strings.HasSuffix(e.Name(), ".json") && !strings.HasSuffix(e.Name(), ".schema.json") {
				tool := strings.TrimSuffix(e.Name(), ".json")
				names = append(names, "mcp__mini__"+serverName+"__"+tool)
			}
		}
	}
	if extraBuiltins != "" {
		names = append(names, strings.Split(extraBuiltins, ",")...)
	}
	return strings.Join(names, ",")
}

func writeMiniWrapper(env *Env, miniBin, configDir string) (string, error) {
	dir := env.TempDir()
	script := "#!/bin/sh\nexec " + miniBin + " --config " + configDir + " \"$@\"\n"
	return dir, os.WriteFile(filepath.Join(dir, "mini"), []byte(script), 0o755)
}

func freshWorkDir(env *Env, workSrcDir string) (string, error) {
	if workSrcDir == "" {
		return "", nil
	}
	d := env.DebugDir("work")
	return d, copyDir(workSrcDir, d)
}
