package registry_test

import (
	"runtime"
	"slices"
	"sync"
	"testing"

	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/registry"
)

func TestReplaceServer_removesStaleHiddenTools(t *testing.T) {
	reg := registry.New()
	reg.AddServer(registry.ServerParams{
		Name: "svc",
		Defs: defs("visible", "secret"),
		Perm: &config.PermissionsConfig{Hidden: []string{"secret"}},
	})
	reg.AddServer(registry.ServerParams{Name: "other", Defs: defs("untouched")})
	if _, ok := reg.LookupWithHidden("svc.secret"); !ok {
		t.Fatal("hidden tool was not registered before replacement")
	}

	reg.ReplaceServer(registry.ServerParams{Name: "svc", Defs: defs("visible")})

	if _, ok := reg.LookupWithHidden("svc.secret"); ok {
		t.Fatal("removed hidden tool remained in LookupWithHidden")
	}
	if names := compactNames(reg.AllWithHidden()); !slices.Equal(names, []string{"other.untouched", "svc.visible"}) {
		t.Fatalf("AllWithHidden = %v", names)
	}
	if _, err := reg.Lookup("other.untouched"); err != nil {
		t.Fatalf("other server entry was removed: %v", err)
	}
}

func TestReplaceServer_replacesAliases(t *testing.T) {
	reg := registry.New()
	reg.AddServer(registry.ServerParams{
		Name:            "svc",
		Defs:            defs("real_tool"),
		AliasByToolName: map[string]string{"real_tool": "old_alias"},
	})
	if _, err := reg.Lookup("svc.old_alias"); err != nil {
		t.Fatalf("old alias was not registered before replacement: %v", err)
	}

	reg.ReplaceServer(registry.ServerParams{
		Name:            "svc",
		Defs:            defs("real_tool"),
		AliasByToolName: map[string]string{"real_tool": "new_alias"},
	})

	if _, err := reg.Lookup("svc.old_alias"); err == nil {
		t.Fatal("old alias remained after replacement")
	}
	entry, err := reg.Lookup("svc.new_alias")
	if err != nil {
		t.Fatalf("new alias was not registered: %v", err)
	}
	if entry.ToolName.UpstreamName != "real_tool" {
		t.Fatalf("new alias routes to %q, want real_tool", entry.ToolName.UpstreamName)
	}
}

func TestReplaceServer_readersSeeOnlyCompleteCatalogs(t *testing.T) {
	reg := registry.New()
	reg.AddServer(registry.ServerParams{Name: "svc", Defs: defs("old")})

	wantOld := []string{"svc.old"}
	wantNew := []string{"svc.new"}
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
		reg.ReplaceServer(registry.ServerParams{Name: "svc", Defs: defs("new")})
		reg.ReplaceServer(registry.ServerParams{Name: "svc", Defs: defs("old")})
	}
	reg.ReplaceServer(registry.ServerParams{Name: "svc", Defs: defs("new")})
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
