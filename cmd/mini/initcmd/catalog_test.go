package initcmd

import (
	"testing"

	"github.com/mcpmini/mini/internal/catalog"
	"github.com/mcpmini/mini/internal/config"
)

func TestPlan_InMini_matchesAConfiguredServerByNameOrURL(t *testing.T) {
	plan := Plan{
		written: []config.ServerConfig{{Name: "GitHub"}, {Name: "my-linear", URL: "https://LINEAR.example.com/mcp/"}},
	}
	for _, tt := range []struct {
		entry catalog.Entry
		want  bool
	}{
		{catalog.Entry{Name: "github", URL: "https://github.example.com/mcp"}, true},
		{catalog.Entry{Name: "linear", URL: "https://linear.example.com/mcp"}, true},
		{catalog.Entry{Name: "notion", URL: "https://notion.example.com/mcp"}, false},
	} {
		if got := plan.InMini(tt.entry); got != tt.want {
			t.Errorf("InMini(%s) = %v, want %v", tt.entry.Name, got, tt.want)
		}
	}
}
