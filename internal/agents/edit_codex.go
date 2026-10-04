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

// EditCodexServers switches off the named servers and writes mini's entry, editing lines in
// place so the user's comments and layout survive. The result is re-parsed and must differ from
// the original only in those servers' enabled values and the mini table, or nothing is returned.
func EditCodexServers(data []byte, disable []string, mini CodexServer) ([]byte, error) {
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
	miniLines, err := codexMiniTable(mini)
	if err != nil {
		return nil, err
	}
	doc.lines = plan.apply(scanned, miniLines)
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
	text = strings.TrimSuffix(strings.TrimSuffix(text, "\n"), "\r")
	if text != "" {
		doc.lines = strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	}
	return doc
}

func (d codexLines) join() []byte {
	text := strings.Join(d.lines, d.eol)
	if len(d.lines) > 0 {
		text += d.eol
	}
	return []byte(d.bom + text)
}

type codexEditPlan struct {
	replaceEnabled map[int]bool
	insertEnabled  map[int]bool
	dropMini       map[int]bool
	miniAt         int
}

func planCodexEdit(lines []tomlLine, before map[string]any, disable []string) (codexEditPlan, error) {
	plan := codexEditPlan{replaceEnabled: map[int]bool{}, insertEnabled: map[int]bool{}, miniAt: -1}
	for _, name := range disable {
		if err := plan.disableServer(lines, before, name); err != nil {
			return codexEditPlan{}, err
		}
	}
	if serverDefined(before, "mini") {
		if err := requireTableForm(lines, "mini"); err != nil {
			return codexEditPlan{}, err
		}
	}
	plan.dropMini, plan.miniAt = miniTableLines(lines)
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

// Line edits only understand [mcp_servers.<name>] tables; anything else could be rewritten
// wrongly, so it is refused by name.
func requireTableForm(lines []tomlLine, name string) error {
	if slices.ContainsFunc(lines, func(l tomlLine) bool { return isServerHeader(l, name) }) {
		return nil
	}
	return fmt.Errorf("codex server %q is written as %s; mini can only edit [mcp_servers.%s] tables", name, codexServerForm(lines, name), name)
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

// The comments and blank lines ending a mini section introduce whatever table follows, so they stay.
func miniTableLines(lines []tomlLine) (map[int]bool, int) {
	drop, at := map[int]bool{}, -1
	inMini := false
	for i, l := range lines {
		if l.isHeader() {
			inMini = hasPrefix(l.header, []string{"mcp_servers", "mini"})
			if inMini && at < 0 {
				at = i
			}
		}
		drop[i] = inMini
	}
	for i := len(lines) - 1; i > at; i-- {
		if drop[i] && isBlankOrComment(lines[i]) && (i+1 == len(lines) || !drop[i+1]) {
			drop[i] = false
		}
	}
	return drop, at
}

func isBlankOrComment(l tomlLine) bool {
	trimmed := strings.TrimSpace(l.text)
	return l.key == nil && !l.isHeader() && (trimmed == "" || strings.HasPrefix(trimmed, "#"))
}

func (p codexEditPlan) apply(lines []tomlLine, miniLines []string) []string {
	var out []string
	for i, l := range lines {
		if i == p.miniAt {
			out = append(out, miniLines...)
		}
		switch {
		case p.dropMini[i]:
		case p.replaceEnabled[i]:
			out = append(out, leadingSpace(l.text)+"enabled = false"+trailingComment(l.value))
		default:
			out = append(out, l.text)
		}
		if p.insertEnabled[i] {
			out = append(out, "enabled = false")
		}
	}
	if p.miniAt < 0 {
		out = appendTable(out, miniLines)
	}
	return out
}

func appendTable(lines, table []string) []string {
	if len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) != "" {
		lines = append(lines, "")
	}
	return append(lines, table...)
}

// enabled holds a bool, so a # in its value can only start a comment.
func trailingComment(value string) string {
	if i := strings.Index(value, "#"); i >= 0 {
		return " " + value[i:]
	}
	return ""
}

func leadingSpace(text string) string {
	return text[:len(text)-len(strings.TrimLeft(text, " \t"))]
}

func codexMiniTable(mini CodexServer) ([]string, error) {
	var body bytes.Buffer
	if err := toml.NewEncoder(&body).Encode(mini); err != nil {
		return nil, err
	}
	lines := strings.Split(strings.TrimRight(body.String(), "\n"), "\n")
	return append([]string{"[mcp_servers.mini]"}, lines...), nil
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
	fresh, err := decodeCodexConfig([]byte(strings.Join(miniLines, "\n")))
	if err != nil {
		return nil, err
	}
	servers := tableAt(want, "mcp_servers")
	for _, name := range disable {
		server, ok := servers[name].(map[string]any)
		if !ok {
			return nil, fmt.Errorf("codex server %q is not a table", name)
		}
		server["enabled"] = false
	}
	servers["mini"] = tableAt(fresh, "mcp_servers")["mini"]
	return want, nil
}

func tableAt(doc map[string]any, key string) map[string]any {
	table, ok := doc[key].(map[string]any)
	if !ok {
		table = map[string]any{}
		doc[key] = table
	}
	return table
}
