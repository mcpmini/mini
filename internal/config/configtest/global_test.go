package configtest

import (
	"reflect"
	"testing"

	"github.com/mcpmini/mini/internal/config"
)

func TestWriteConfigPreservesDefaultsAndExplicitZeros(t *testing.T) {
	for _, zeroLimits := range []bool{false, true} {
		t.Run(map[bool]string{false: "defaults", true: "explicit zeros"}[zeroLimits], func(t *testing.T) {
			dir := t.TempDir()
			want := config.DefaultConfig()
			want.ResponseDir = t.TempDir()
			if zeroLimits {
				want.DefaultStringLimit = 0
				want.ResponseDiskBudgetMB = 0
			}
			WriteConfig(t, dir, want)
			got, err := config.LoadMain(dir)
			if err != nil {
				t.Fatalf("LoadMain: %v", err)
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("loaded config = %#v, want %#v", got, want)
			}
		})
	}
}

func TestWriteActionPreservesSerializedNameArgsAndPermission(t *testing.T) {
	dir := t.TempDir()
	want := config.ActionConfig{
		Name:        "fetch",
		Description: "Fetch item",
		Server:      "svc",
		Tool:        "get_item",
		DefaultArgs: map[string]any{"id": 42, "state": "open"},
		Permission:  "protected",
	}
	WriteAction(t, dir, want)
	got, err := config.LoadActions(dir)
	if err != nil {
		t.Fatalf("LoadActions: %v", err)
	}
	if !reflect.DeepEqual(got, []config.ActionConfig{want}) {
		t.Fatalf("loaded actions = %#v, want %#v", got, want)
	}
}
