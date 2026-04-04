package transport

import (
	"testing"
)

var validateURLCases = []struct {
	name    string
	url     string
	wantErr bool
}{
	{"accepts public https", "https://mcp.example.com/api", false},
	{"accepts public http", "http://mcp.example.com/api", false},
	{"rejects non-http scheme", "ftp://mcp.example.com", true},
	{"rejects localhost", "http://localhost:8080", true},
	{"rejects subdomain of localhost", "http://evil.localhost:8080", true},
	{"rejects .local hostname", "http://internal.local", true},
	{"rejects .internal hostname", "http://db.internal", true},
	{"rejects loopback IP", "http://127.0.0.1:8080", true},
	{"rejects 127.x.x.x range", "http://127.1.2.3", true},
	{"rejects private 10.x.x.x", "http://10.0.0.1", true},
	{"rejects private 192.168.x.x", "http://192.168.1.1", true},
	{"rejects 172.16-31.x.x", "http://172.16.0.1", true},
	{"rejects link-local 169.254.x.x", "http://169.254.169.254", true},
	{"rejects IPv6 loopback", "http://[::1]:8080", true},
	{"rejects IPv4-in-IPv6 loopback", "http://[::ffff:127.0.0.1]", true},
	{"rejects malformed URL", ":not-a-url", true},
}

func TestValidateURL(t *testing.T) {
	for _, tc := range validateURLCases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateURL(tc.url)
			if tc.wantErr && err == nil {
				t.Errorf("expected error for %q", tc.url)
			}
			if !tc.wantErr && err != nil {
				t.Errorf("unexpected error for %q: %v", tc.url, err)
			}
		})
	}
}
