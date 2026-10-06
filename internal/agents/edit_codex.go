package agents

import (
	"bytes"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"

	"github.com/BurntSushi/toml"
)

type CodexServer struct {
	Command string   `toml:"command"`
	Args    []string `toml:"args"`
}

// EditCodexServers disables named servers and adds mini only when its entry is absent.
func EditCodexServers(data []byte, disable []string, mini *CodexServer) ([]byte, error) {
	before, err := decodeCodexConfig(data)
	if err != nil {
		return nil, err
	}
	doc := splitCodexLines(data)
	scanned := scanTOMLLines(doc.lines)
	plan, err := planCodexEdit(scanned, before, disable)
	if err != nil {
		return nil, err
	}
	miniLines, err := codexMiniTable(before, mini)
	if err != nil {
		return nil, err
	}
	doc.lines = plan.apply(scanned, miniLines, doc.eol)
	edited := doc.join()
	if err := verifyCodexEdit(data, edited, disable, miniLines); err != nil {
		return nil, err
	}
	return edited, nil
}

func decodeCodexConfig(data []byte) (map[string]any, error) {
	var doc map[string]any
	if _, err := toml.Decode(string(data), &doc); err != nil {
		return nil, fmt.Errorf("parse codex config: %w", err)
	}
	return doc, nil
}

type codexLines struct {
	lines []string
	eol   string
	bom   string
}

const utf8BOM = "\ufeff"

func splitCodexLines(data []byte) codexLines {
	text := string(data)
	doc := codexLines{eol: "\n"}
	if strings.HasPrefix(text, utf8BOM) {
		doc.bom, text = utf8BOM, strings.TrimPrefix(text, utf8BOM)
	}
	if strings.Contains(text, "\r\n") {
		doc.eol = "\r\n"
	}
	doc.lines = strings.SplitAfter(text, "\n")
	if doc.lines[len(doc.lines)-1] == "" {
		doc.lines = doc.lines[:len(doc.lines)-1]
	}
	return doc
}

func (d codexLines) join() []byte {
	return []byte(d.bom + strings.Join(d.lines, ""))
}

type codexEditPlan struct {
	replaceEnabled map[int]bool
	insertEnabled  map[int]bool
}

func planCodexEdit(lines []tomlLine, before map[string]any, disable []string) (codexEditPlan, error) {
	plan := codexEditPlan{replaceEnabled: map[int]bool{}, insertEnabled: map[int]bool{}}
	for _, name := range disable {
		if name == MiniKey {
			continue
		}
		if err := plan.disableServer(lines, before, name); err != nil {
			return codexEditPlan{}, err
		}
	}
	return plan, nil
}

func (p codexEditPlan) disableServer(lines []tomlLine, before map[string]any, name string) error {
	if !serverDefined(before, name) {
		return fmt.Errorf("codex config has no server %q", name)
	}
	if err := requireTableForm(lines, name); err != nil {
		return err
	}
	header := slices.IndexFunc(lines, func(l tomlLine) bool { return isServerHeader(l, name) })
	for i := header + 1; i < len(lines) && !lines[i].isHeader(); i++ {
		if slices.Equal(lines[i].key, []string{"enabled"}) {
			p.replaceEnabled[i] = true
			return nil
		}
	}
	p.insertEnabled[header] = true
	return nil
}

func serverDefined(doc map[string]any, name string) bool {
	servers, _ := doc["mcp_servers"].(map[string]any)
	_, ok := servers[name]
	return ok
}

func isServerHeader(l tomlLine, name string) bool {
	return !l.arrayTable && slices.Equal(l.header, []string{"mcp_servers", name})
}

func requireTableForm(lines []tomlLine, name string) error {
	if slices.ContainsFunc(lines, func(l tomlLine) bool { return isServerHeader(l, name) }) {
		return nil
	}
	return fmt.Errorf(
		"codex server %q is written as %s; mini can only edit [mcp_servers.%s] tables",
		name,
		codexServerForm(lines, name),
		name,
	)
}

func codexServerForm(lines []tomlLine, name string) string {
	target := []string{"mcp_servers", name}
	form := "sub-tables only"
	for _, l := range lines {
		path := append(slices.Clone(l.table), l.key...)
		switch {
		case l.arrayTable && slices.Equal(l.header, target):
			return "an array of tables"
		case l.key != nil && hasPrefix(target, path) && strings.HasPrefix(l.value, "{"):
			return "an inline table"
		case l.key != nil && len(l.table) < len(target) && len(path) > len(target) && hasPrefix(path, target):
			form = "dotted keys"
		}
	}
	return form
}

func hasPrefix(path, prefix []string) bool {
	return len(prefix) <= len(path) && slices.Equal(path[:len(prefix)], prefix)
}

func (p codexEditPlan) apply(lines []tomlLine, miniLines []string, eol string) []string {
	var out []string
	for i, l := range lines {
		text := l.text
		if p.replaceEnabled[i] {
			text = leadingSpace(text) + "enabled = false" + trailingEnabledComment(l.value)
		}
		ending := l.ending
		if p.insertEnabled[i] && ending == "" {
			ending = eol
		}
		out = append(out, text+ending)
		if p.insertEnabled[i] {
			out = append(out, "enabled = false"+ending)
		}
	}
	return appendTable(out, miniLines, eol)
}

func appendTable(lines, table []string, eol string) []string {
	if len(table) == 0 {
		return lines
	}
	if len(lines) > 0 {
		last := len(lines) - 1
		if !strings.HasSuffix(lines[last], "\n") {
			lines[last] += eol
		}
		if strings.TrimSpace(lines[last]) != "" {
			lines = append(lines, eol)
		}
	}
	for _, line := range table {
		lines = append(lines, line+eol)
	}
	return lines
}

func trailingEnabledComment(value string) string {
	if i := strings.Index(value, "#"); i >= 0 {
		return " " + value[i:]
	}
	return ""
}

func leadingSpace(text string) string {
	return text[:len(text)-len(strings.TrimLeft(text, " \t"))]
}

func codexMiniTable(before map[string]any, mini *CodexServer) ([]string, error) {
	if mini == nil || serverDefined(before, MiniKey) {
		return nil, nil
	}
	var body bytes.Buffer
	if err := toml.NewEncoder(&body).Encode(mini); err != nil {
		return nil, err
	}
	lines := strings.Split(strings.TrimRight(body.String(), "\n"), "\n")
	return append([]string{"[mcp_servers." + MiniKey + "]"}, lines...), nil
}

func verifyCodexEdit(original, edited []byte, disable []string, miniLines []string) error {
	want, err := expectedCodexConfig(original, disable, miniLines)
	if err != nil {
		return err
	}
	got, err := decodeCodexConfig(edited)
	if err != nil {
		return fmt.Errorf("codex config edit would not parse: %w", err)
	}
	if !reflect.DeepEqual(got, want) {
		return errors.New("codex config edit would change more than the intended servers; not written")
	}
	return nil
}

func expectedCodexConfig(original []byte, disable []string, miniLines []string) (map[string]any, error) {
	want, err := decodeCodexConfig(original)
	if err != nil {
		return nil, err
	}
	servers := tableAt(want, "mcp_servers")
	for _, name := range disable {
		if name == MiniKey {
			continue
		}
		server, ok := servers[name].(map[string]any)
		if !ok {
			return nil, fmt.Errorf("codex server %q is not a table", name)
		}
		server["enabled"] = false
	}
	return want, addExpectedMini(servers, miniLines)
}

func addExpectedMini(servers map[string]any, miniLines []string) error {
	if _, exists := servers[MiniKey]; exists || len(miniLines) == 0 {
		return nil
	}
	fresh, err := decodeCodexConfig([]byte(strings.Join(miniLines, "\n")))
	if err == nil {
		servers[MiniKey] = tableAt(fresh, "mcp_servers")[MiniKey]
	}
	return err
}

func tableAt(doc map[string]any, key string) map[string]any {
	table, ok := doc[key].(map[string]any)
	if !ok {
		table = map[string]any{}
		doc[key] = table
	}
	return table
}
