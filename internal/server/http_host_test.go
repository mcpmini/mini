//go:build test

package server_test

import (
	"bytes"
	"net/http"
	"testing"
)

func postWithHost(t *testing.T, url, host string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, url+"/mcp", bytes.NewReader(initRequest()))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Host = host
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func TestHTTPServer_HostLoopbackCheck(t *testing.T) {
	_, ts := newHTTPTestServer(t)
	cases := []struct {
		name string
		host string
		want int
	}{
		{"evil domain rejected", "evil.com", http.StatusForbidden},
		{"evil domain with port rejected", "evil.com:1234", http.StatusForbidden},
		{"loopback IP with port", "127.0.0.1:1234", http.StatusOK},
		{"ipv6 loopback", "[::1]:1234", http.StatusOK},
		{"localhost", "localhost:1234", http.StatusOK},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp := postWithHost(t, ts.URL, tc.host)
			resp.Body.Close()
			if resp.StatusCode != tc.want {
				t.Errorf("Host %q: got %d, want %d", tc.host, resp.StatusCode, tc.want)
			}
		})
	}
}
