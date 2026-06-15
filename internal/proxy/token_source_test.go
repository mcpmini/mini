package proxy

import (
	"errors"
	"sync"
	"testing"
)

func TestTokenSource_refreshPicksUpRotatedValue(t *testing.T) {
	ts := &tokenSource{value: "old", reload: func() (string, error) { return "new", nil }}
	ts.refresh()
	if got := ts.current(); got != "new" {
		t.Errorf("current() = %q, want %q", got, "new")
	}
}

func TestTokenSource_nilReloadIsNoOp(t *testing.T) {
	ts := &tokenSource{value: "tok"}
	ts.refresh()
	if got := ts.current(); got != "tok" {
		t.Errorf("current() = %q, want %q", got, "tok")
	}
}

func TestTokenSource_reloadErrorKeepsCurrentValue(t *testing.T) {
	ts := &tokenSource{value: "tok", reload: func() (string, error) { return "", errors.New("disk gone") }}
	ts.refresh()
	if got := ts.current(); got != "tok" {
		t.Errorf("a failed reload must not blank the token; got %q, want %q", got, "tok")
	}
}

func TestTokenSource_concurrentCurrentAndRefresh(t *testing.T) {
	ts := &tokenSource{value: "0", reload: func() (string, error) { return "1", nil }}
	var wg sync.WaitGroup
	for range 50 {
		wg.Add(2)
		go func() { defer wg.Done(); ts.refresh() }()
		go func() { defer wg.Done(); _ = ts.current() }()
	}
	wg.Wait()
}
