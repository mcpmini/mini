//go:build test

package server

import (
	"net/http"
	"testing"
)

func TestIsSameHost_matching(t *testing.T) {
	r := &http.Request{Host: "127.0.0.1:4857"}
	cases := []struct {
		origin string
		want   bool
	}{
		{"http://127.0.0.1:4857", true},
		{"https://127.0.0.1:4857", true},
		{"http://evil.com", false},
		{"http://127.0.0.1:9999", false},
	}
	for _, c := range cases {
		if got := isSameHost(r, c.origin); got != c.want {
			t.Errorf("isSameHost(%q) = %v, want %v", c.origin, got, c.want)
		}
	}
}
