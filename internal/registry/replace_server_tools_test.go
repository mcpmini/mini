package registry_test

import (
	"runtime"
	"slices"
	"sync"
	"testing"

	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/registry"
)

func TestReplaceServerTools_preservesHiddenActions(t *testing.T) {
	reg := registry.New()
	reg.AddServer(registry.ServerParams{Name: "svc", Defs: defs("base", "gone")})
	reg.AddAction(config.ActionConfig{
		Name:       "hidden_action",
		Server:     "svc",
		Tool:       "base",
		Permission: string(config.PermHidden),
	})

	before := compactNames(reg.AllWithHidden())
	reg.ReplaceServerTools(registry.ServerParams{Name: "svc", Defs: defs("base", "new")})
	after := compactNames(reg.AllWithHidden())

	if !slices.Contains(before, "svc.hidden_action") {
		t.Fatalf("hidden action missing before replace: %v", before)
	}
	if !slices.Contains(after, "svc.hidden_action") {
		t.Fatalf("hidden action missing after replace: %v", after)
	}
}

func TestReplaceServerTools_visibleActionKeepsPrecedenceOverCollidingUpstream(t *testing.T) {
	reg := registry.New()
	reg.AddServer(registry.ServerParams{Name: "svc", Defs: defs("base")})
	reg.AddAction(config.ActionConfig{Name: "shortcut", Server: "svc", Tool: "base"})

	reg.ReplaceServerTools(registry.ServerParams{Name: "svc", Defs: defs("base", "shortcut")})

	entry, err := reg.Lookup("svc.shortcut")
	if err != nil {
		t.Fatalf("Lookup: %v", err)
	}
	if entry.Kind != registry.ToolEntryAction {
		t.Fatalf("Kind = %v, want action", entry.Kind)
	}
	if names := fullNames(reg.AllFull()); !slices.Equal(names, []string{"svc.base", "svc.shortcut"}) {
		t.Fatalf("AllFull = %v", names)
	}
}

func TestReplaceServerTools_hiddenActionKeepsPrecedenceOverCollidingUpstream(t *testing.T) {
	reg := registry.New()
	reg.AddServer(registry.ServerParams{Name: "svc", Defs: defs("base")})
	reg.AddAction(config.ActionConfig{
		Name:       "shortcut",
		Server:     "svc",
		Tool:       "base",
		Permission: string(config.PermHidden),
	})

	reg.ReplaceServerTools(registry.ServerParams{Name: "svc", Defs: defs("base", "shortcut")})

	if _, err := reg.Lookup("svc.shortcut"); err == nil {
		t.Fatal("hidden action collision should stay hidden")
	}
	if names := compactNames(reg.AllWithHidden()); !slices.Equal(names, []string{"svc.base", "svc.shortcut"}) {
		t.Fatalf("AllWithHidden = %v", names)
	}
	if names := fullNames(reg.AllFull()); !slices.Equal(names, []string{"svc.base"}) {
		t.Fatalf("AllFull = %v", names)
	}
}

func TestReplaceServerTools_readersSeeOnlyCompleteCatalogs(t *testing.T) {
	reg := registry.New()
	reg.AddServer(registry.ServerParams{Name: "svc", Defs: defs("old")})
	reg.AddAction(config.ActionConfig{Name: "act", Server: "svc", Tool: "old"})

	wantOld := []string{"svc.act", "svc.old"}
	wantNew := []string{"svc.act", "svc.new"}
	errs := make(chan []string, 1)
	stop := make(chan struct{})
	var wg sync.WaitGroup
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				names := fullNames(reg.AllFull())
				if slices.Equal(names, wantOld) || slices.Equal(names, wantNew) {
					runtime.Gosched()
					continue
				}
				select {
				case errs <- names:
				default:
				}
				return
			}
		}()
	}

	for i := 0; i < 100; i++ {
		reg.ReplaceServerTools(registry.ServerParams{Name: "svc", Defs: defs("new")})
		reg.ReplaceServerTools(registry.ServerParams{Name: "svc", Defs: defs("old")})
	}
	reg.ReplaceServerTools(registry.ServerParams{Name: "svc", Defs: defs("new")})
	close(stop)
	wg.Wait()

	select {
	case names := <-errs:
		t.Fatalf("observed partial catalog: %v", names)
	default:
	}
}

func compactNames(entries []registry.CompactEntry) []string {
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name)
	}
	slices.Sort(names)
	return names
}

func fullNames(entries []*registry.ToolEntry) []string {
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.FullName)
	}
	slices.Sort(names)
	return names
}
