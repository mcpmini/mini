//go:build test

package main

import (
	"errors"
	"reflect"
	"testing"
)

func TestParseToolTable(t *testing.T) {
	output := "TOOL  HINTS\nalpha  read-only\nbeta  destructive\n\nDetails: mini ls conf <tool>\n"
	want := []tool{{Name: "alpha"}, {Name: "beta"}}
	if got := parseToolTable(output); !reflect.DeepEqual(got, want) {
		t.Fatalf("parseToolTable() = %#v, want %#v", got, want)
	}
	if got := parseToolTable("No tools available.\n"); got != nil {
		t.Fatalf("parseToolTable(empty table) = %#v, want nil", got)
	}
}

func TestFailedOrUnreadableMiniOutputIsAnError(t *testing.T) {
	exited := errors.New("exit status 42")
	if _, err := listOutput(cliResult{stdout: "TOOL HINTS\nalpha\n", err: exited}); err == nil {
		t.Error("listOutput: a failed ls that printed tools returned no error")
	}
	if _, err := callOutput("alpha", cliResult{stdout: `{"data":"ok"}`, err: exited}); err == nil {
		t.Error("callOutput: a failed call returned no error")
	}
	if _, err := callOutput("alpha", cliResult{stdout: "not json"}); err == nil {
		t.Error("callOutput: output that is not JSON returned no error")
	}
	got, err := callOutput("alpha", cliResult{stdout: `{"data":"ok"}`})
	if err != nil || got["data"] != "ok" {
		t.Errorf("callOutput(success) = %v, %v; want the decoded envelope", got, err)
	}
}
