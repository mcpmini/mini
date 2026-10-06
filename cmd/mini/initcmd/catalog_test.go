package initcmd

import (
	"reflect"
	"testing"

	"github.com/mcpmini/mini/internal/catalog"
	"github.com/mcpmini/mini/internal/config"
)

func entryNames(entries []catalog.Entry) []string {
	var names []string
	for _, entry := range entries {
		names = append(names, entry.Name)
	}
	return names
}

func TestAvailableEntries(t *testing.T) {
	t.Run("filters configured names and URLs", func(t *testing.T) {
		entries := []catalog.Entry{
			{Name: "github", URL: "https://github.example.com/mcp", Category: "Dev"},
			{Name: "linear", URL: "https://linear.example.com/mcp", Category: "Dev"},
			{Name: "notion", URL: "https://notion.example.com/mcp", Category: "Dev"},
		}
		servers := []config.ServerConfig{{Name: "GitHub"}, {Name: "my-linear", URL: "https://LINEAR.example.com/mcp/"}}
		if names := entryNames(AvailableEntries(entries, servers)); !reflect.DeepEqual(names, []string{"notion"}) {
			t.Errorf("available = %v, want only notion", names)
		}
	})
	t.Run("keeps catalog order after filtering", func(t *testing.T) {
		entries := []catalog.Entry{
			{Name: "c", Category: "Dev"}, {Name: "taken", Category: "Configured"},
			{Name: "b", Category: "Data"}, {Name: "a", Category: "Dev"},
		}
		available := AvailableEntries(entries, []config.ServerConfig{{Name: "taken"}})
		if names := entryNames(available); !reflect.DeepEqual(names, []string{"c", "b", "a"}) {
			t.Errorf("available = %v, want [c b a]", names)
		}
	})
}
