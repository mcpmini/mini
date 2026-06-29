package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/mcpmini/mini/internal/clock"
	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/invoke"
	"github.com/mcpmini/mini/internal/transport"
)

func listServerTools(configDir, serverName string, out io.Writer) error {
	tools, cleanup, err := fetchTools(configDir, serverName)
	if err != nil {
		return err
	}
	defer cleanup()
	printToolTable(out, tools)
	return nil
}

type toolDetailParams struct {
	ConfigDir  string
	ServerName string
	ToolName   string
	Out        io.Writer
}

func listToolDetail(p toolDetailParams) error {
	tools, cleanup, err := fetchTools(p.ConfigDir, p.ServerName)
	if err != nil {
		return err
	}
	defer cleanup()
	for _, t := range tools {
		if t.Name == p.ToolName {
			printToolDetail(p.Out, t)
			return nil
		}
	}
	return fmt.Errorf("tool %q not found on server %q", p.ToolName, p.ServerName)
}

func fetchTools(configDir, serverName string) ([]transport.ToolDefinition, func(), error) {
	conn, _, err := dialServer(configDir, serverName)
	if err != nil {
		return nil, nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	tools, err := conn.ListTools(ctx)
	if err != nil {
		cancel()
		conn.Close() //nolint:errcheck
		return nil, nil, fmt.Errorf("list tools: %w", err)
	}
	return tools, func() { cancel(); conn.Close() }, nil //nolint:errcheck
}

func dialServer(configDir, serverName string) (transport.Connection, *config.ServerConfig, error) {
	cfg, servers, err := config.Load(configDir)
	if err != nil {
		return nil, nil, fmt.Errorf("load config: %w", err)
	}
	sc := config.FindServer(servers, serverName)
	if sc == nil {
		return nil, nil, fmt.Errorf("server %q not found", serverName)
	}
	ctx := context.Background()
	if sc.Auth != nil && sc.Auth.Type == "oauth2" {
		injectToken(ctx, configDir, sc)
	}
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	conn, err := invoke.Dial(ctx, invoke.DialParams{Logger: logger, Config: cfg, Server: *sc, Clock: clock.System()})
	if err != nil {
		return nil, nil, fmt.Errorf("connect to %s: %w", serverName, err)
	}
	return conn, sc, nil
}

func printToolTable(out io.Writer, tools []transport.ToolDefinition) {
	w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "TOOL\tDESCRIPTION")
	for _, t := range tools {
		args := parseArgDefs(t.InputSchema)
		sig := formatSignature(t.Name, args)
		desc := firstLine(t.Description)
		fmt.Fprintf(w, "%s\t%s\n", sig, desc)
	}
	w.Flush()
}

func printToolDetail(out io.Writer, t transport.ToolDefinition) {
	fmt.Fprintln(out, t.Name)
	if t.Description != "" {
		fmt.Fprintln(out, "  "+strings.ReplaceAll(t.Description, "\n", "\n  "))
	}
	args := parseArgDefs(t.InputSchema)
	if len(args) == 0 {
		return
	}
	fmt.Fprintln(out)
	w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	for _, a := range args {
		req := "optional"
		if a.Required {
			req = "required"
		}
		fmt.Fprintf(w, "  %s\t%s\t%s\t%s\n", a.Name, a.Type, req, a.Description)
	}
	w.Flush()
}

type argDef struct {
	Name        string
	Type        string
	Description string
	Required    bool
}

type rawInputSchema struct {
	Properties map[string]struct {
		Type        string `json:"type"`
		Description string `json:"description"`
	} `json:"properties"`
	Required []string `json:"required"`
}

func parseArgDefs(schema json.RawMessage) []argDef {
	if len(schema) == 0 {
		return nil
	}
	var s rawInputSchema
	if err := json.Unmarshal(schema, &s); err != nil {
		return nil
	}
	return buildArgDefs(s)
}

func buildArgDefs(s rawInputSchema) []argDef {
	req := requiredSet(s.Required)
	args := make([]argDef, 0, len(s.Properties))
	for name, prop := range s.Properties {
		t := prop.Type
		if t == "" {
			t = "any"
		}
		args = append(args, argDef{Name: name, Type: t, Description: prop.Description, Required: req[name]})
	}
	sort.Slice(args, func(i, j int) bool {
		if args[i].Required != args[j].Required {
			return args[i].Required
		}
		return args[i].Name < args[j].Name
	})
	return args
}

func requiredSet(names []string) map[string]bool {
	m := make(map[string]bool, len(names))
	for _, n := range names {
		m[n] = true
	}
	return m
}

func formatSignature(toolName string, args []argDef) string {
	if len(args) == 0 {
		return toolName
	}
	parts := make([]string, 0, len(args))
	for _, a := range args {
		if a.Required {
			parts = append(parts, a.Name)
		} else {
			parts = append(parts, "["+a.Name+"]")
		}
	}
	return toolName + "(" + strings.Join(parts, ", ") + ")"
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}
