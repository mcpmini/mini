package agents

import (
	"strings"
	"testing"
)

var testMini = CodexServer{Command: "/usr/local/bin/mini", Args: []string{"connect"}}

const testMiniTable = `[mcp_servers.mini]
command = "/usr/local/bin/mini"
args = ["connect"]
`

func editCodex(t *testing.T, config string, disable ...string) string {
	t.Helper()
	edited, err := EditCodexServers([]byte(config), disable, testMini)
	if err != nil {
		t.Fatalf("EditCodexServers: %v", err)
	}
	return string(edited)
}

func TestEditCodexServers_switchesOffServersAndAddsMini(t *testing.T) {
	tests := []struct {
		name, config string
		disable      []string
		want         string
	}{
		{
			name:    "enabled is inserted after the header",
			config:  "[mcp_servers.github]\nurl = \"https://example.com/mcp\"\n",
			disable: []string{"github"},
			want:    "[mcp_servers.github]\nenabled = false\nurl = \"https://example.com/mcp\"\n\n" + testMiniTable,
		},
		{
			name:    "an existing enabled line is replaced in place",
			config:  "[mcp_servers.github]\n  enabled = true # on\nurl = \"https://example.com/mcp\"\n",
			disable: []string{"github"},
			want:    "[mcp_servers.github]\n  enabled = false # on\nurl = \"https://example.com/mcp\"\n\n" + testMiniTable,
		},
		{
			name:    "sub-tables and comments are left byte for byte",
			config:  "# my codex setup\nmodel = \"o3\" # pinned\n\n[mcp_servers.github]\nurl = \"https://example.com/mcp\"\n\n[mcp_servers.github.env]\n# keep me\nenabled = true\n",
			disable: []string{"github"},
			want:    "# my codex setup\nmodel = \"o3\" # pinned\n\n[mcp_servers.github]\nenabled = false\nurl = \"https://example.com/mcp\"\n\n[mcp_servers.github.env]\n# keep me\nenabled = true\n\n" + testMiniTable,
		},
		{
			name:    "a header with spaces, quotes and a trailing comment is recognized",
			config:  "[ mcp_servers . \"my server\" ] # work\ncommand = \"npx\"\n",
			disable: []string{"my server"},
			want:    "[ mcp_servers . \"my server\" ] # work\nenabled = false\ncommand = \"npx\"\n\n" + testMiniTable,
		},
		{
			name:    "a header inside a multi-line string is content",
			config:  "[mcp_servers.notes]\ncommand = \"notes\"\nargs = [\"\"\"\n[mcp_servers.notes]\n\"\"\"]\nenabled = true\n",
			disable: []string{"notes"},
			want:    "[mcp_servers.notes]\ncommand = \"notes\"\nargs = [\"\"\"\n[mcp_servers.notes]\n\"\"\"]\nenabled = false\n\n" + testMiniTable,
		},
		{
			name:    "a nested array line inside a multi-line array is content",
			config:  "[mcp_servers.grid]\ncommand = \"grid\"\nmatrix = [\n  [1, 2],\n]\nenabled = true\n",
			disable: []string{"grid"},
			want:    "[mcp_servers.grid]\ncommand = \"grid\"\nmatrix = [\n  [1, 2],\n]\nenabled = false\n\n" + testMiniTable,
		},
		{
			name:    "a byte order mark is kept and doesn't hide the first header",
			config:  "\ufeff[mcp_servers.github]\nurl = \"https://example.com/mcp\"\n",
			disable: []string{"github"},
			want:    "\ufeff[mcp_servers.github]\nenabled = false\nurl = \"https://example.com/mcp\"\n\n" + testMiniTable,
		},
		{
			name:   "an empty config gets just the mini table",
			config: "",
			want:   testMiniTable,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := editCodex(t, tt.config, tt.disable...); got != tt.want {
				t.Errorf("edited config:\n%s\nwant:\n%s", got, tt.want)
			}
		})
	}
}

func TestEditCodexServers_keepsWindowsLineEndings(t *testing.T) {
	config := "model = \"o3\"\r\n\r\n[mcp_servers.github]\r\nurl = \"https://example.com/mcp\"\r\n"

	got := editCodex(t, config, "github")

	want := "model = \"o3\"\r\n\r\n[mcp_servers.github]\r\nenabled = false\r\nurl = \"https://example.com/mcp\"\r\n\r\n" + strings.ReplaceAll(testMiniTable, "\n", "\r\n")
	if got != want {
		t.Errorf("edited config = %q, want %q", got, want)
	}
}

func TestEditCodexServers_runningTwiceLeavesOneMiniTable(t *testing.T) {
	once := editCodex(t, "[mcp_servers.github]\nurl = \"https://example.com/mcp\"\n", "github")

	twice := editCodex(t, once, "github")

	if twice != once {
		t.Errorf("second edit changed the config:\n%s\nwant:\n%s", twice, once)
	}
}

func TestEditCodexServers_refusesWhatLineEditsCannotChangeSafely(t *testing.T) {
	tests := []struct {
		name, config, disable, want string
	}{
		{"inline table", "[mcp_servers]\ngithub = { url = \"https://example.com/mcp\" }\n", "github", `"github" is written as an inline table`},
		{"inline tables at the root", "mcp_servers = { github = { url = \"https://example.com/mcp\" } }\n", "github", `"github" is written as an inline table`},
		{"dotted keys", "[mcp_servers]\ngithub.url = \"https://example.com/mcp\"\n", "github", `"github" is written as dotted keys`},
		{"dotted keys at the root", "mcp_servers.github.url = \"https://example.com/mcp\"\n", "github", `"github" is written as dotted keys`},
		{"array of tables", "[[mcp_servers.github]]\nurl = \"https://example.com/mcp\"\n", "github", `"github" is written as an array of tables`},
		{"only a sub-table", "[mcp_servers.github.env]\nTOKEN = \"x\"\n", "github", `"github" is written as sub-tables only`},
		{"unknown server", "[mcp_servers.github]\nurl = \"https://example.com/mcp\"\n", "linear", `codex config has no server "linear"`},
		{"invalid TOML", "[mcp_servers.github\n", "github", "parse codex config"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var disable []string
			if tt.disable != "" {
				disable = []string{tt.disable}
			}
			edited, err := EditCodexServers([]byte(tt.config), disable, testMini)
			if err == nil || !strings.Contains(err.Error(), tt.want) || edited != nil {
				t.Fatalf("EditCodexServers = %q, %v; want no output and an error containing %q", edited, err, tt.want)
			}
		})
	}
}

func TestEditCodexServers_preservesExistingMiniInEveryForm(t *testing.T) {
	for name, entry := range map[string]string{
		"table with settings and env": "[mcp_servers.mini]\ncommand = \"custom-mini\"\nenabled = false\ntool_timeout_sec = 99\n\n[mcp_servers.mini.env]\nTOKEN = \"synthetic-token\"",
		"inline table":                "[mcp_servers]\nmini = { command = \"custom-mini\", enabled = false }",
		"root inline table":           "mcp_servers = { mini = { command = \"custom-mini\" } }",
		"sub-tables only":             "[mcp_servers.mini.env]\nTOKEN = \"synthetic-token\"",
		"dotted keys":                 "mcp_servers.mini.command = \"custom-mini\"\nmcp_servers.mini.enabled = false",
		"array of tables":             "[[mcp_servers.mini]]\ncommand = \"custom-mini\"",
	} {
		t.Run(name, func(t *testing.T) {
			config := "[mcp_servers.other]\ncommand = \"other\"\n\n" + entry
			if name == "root inline table" || name == "dotted keys" {
				config = entry + "\n\n[mcp_servers.other]\ncommand = \"other\"\n"
			}
			want := strings.Replace(config, "[mcp_servers.other]\n", "[mcp_servers.other]\nenabled = false\n", 1)
			if got := editCodex(t, config, "mini", "other"); got != want {
				t.Fatalf("existing mini changed:\n%s\nwant:\n%s", got, want)
			}
		})
	}
}

func TestEditCodexServers_ignoresMiniWhenAbsentFromDisable(t *testing.T) {
	if got := editCodex(t, "", "mini"); got != testMiniTable {
		t.Fatalf("config = %q, want mini added", got)
	}
}

func TestEditCodexServers_preservesMixedEndingsAndFinalMiniBytes(t *testing.T) {
	config := "[mcp_servers.other]\r\ncommand = \"other\"\n\r\n[mcp_servers.mini]\ncommand = \"custom-mini\"\r\nenabled = false"
	want := strings.Replace(config, "[mcp_servers.other]\r\n", "[mcp_servers.other]\r\nenabled = false\r\n", 1)
	if got := editCodex(t, config, "other"); got != want {
		t.Fatalf("config = %q, want %q", got, want)
	}
}

func TestEditCodexServers_quoteRunsDoNotHideLaterServerHeaders(t *testing.T) {
	for _, quote := range []string{"\"", "'"} {
		for _, count := range []int{4, 5} {
			value := "[" + strings.Repeat(quote, 3) + "x" + strings.Repeat(quote, count) + "]"
			t.Run(value, func(t *testing.T) {
				config := "[mcp_servers.a]\nargs = " + value + "\n[mcp_servers.b]\ncommand = \"b\"\n"
				want := strings.Replace(config, "[mcp_servers.b]\n", "[mcp_servers.b]\nenabled = false\n", 1) + "\n" + testMiniTable
				if got := editCodex(t, config, "b"); got != want {
					t.Fatalf("config = %q, want %q", got, want)
				}
			})
		}
	}
}

func TestEditCodexServers_refusesAnEditThatWouldNotParse(t *testing.T) {
	config := "[mcp_servers.other]\nenabled = \"\"\"first\nsecond\"\"\"\n"
	edited, err := EditCodexServers([]byte(config), []string{"other"}, testMini)
	if err == nil || edited != nil || !strings.Contains(err.Error(), "edit would not parse") {
		t.Fatalf("edit = %q, %v; want refused unparseable result", edited, err)
	}
}

func TestVerifyCodexEdit_refusesUnintendedChangesAndInvalidTOML(t *testing.T) {
	original := "model = \"synthetic-model\"\n[mcp_servers.mini]\ncommand = \"custom-mini\"\n"
	for name, edited := range map[string]string{
		"changed unrelated setting": strings.Replace(original, "synthetic-model", "changed-model", 1),
		"changed existing mini":     strings.Replace(original, "custom-mini", "changed-mini", 1),
		"invalid result":            "[mcp_servers.mini\n",
	} {
		t.Run(name, func(t *testing.T) {
			err := verifyCodexEdit([]byte(original), []byte(edited), nil, nil)
			if err == nil {
				t.Fatalf("verification accepted %s", edited)
			}
		})
	}
}
