package auth

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	"golang.org/x/oauth2"

	"github.com/mcpmini/mini/internal/config"
)

func TestParseWWWAuthParam(t *testing.T) {
	mixed := `Basic realm="ignored", bEaReR realm="mcp, api", RESOURCE_METADATA="https://example.test/prm?a=1,b=2", ScOpE="mcp:read mcp:write", Basic scope="wrong"`
	for _, tc := range []struct{ name, header, param, want string }{
		{name: "quoted realm", header: mixed, param: "realm", want: "mcp, api"},
		{name: "quoted metadata URL", header: mixed, param: "resource_metadata", want: "https://example.test/prm?a=1,b=2"},
		{name: "mixed schemes", header: mixed, param: "scope", want: "mcp:read mcp:write"},
		{name: "whitespace around equals", header: `Bearer scope = "mcp:read"`, param: "scope", want: "mcp:read"},
		{name: "empty element", header: `Bearer realm="a",, scope="b"`, param: "scope", want: "b"},
		{name: "whitespace element", header: `Bearer realm="a", , scope="b"`, param: "scope", want: "b"},
		{name: "bare Bearer first", header: `Bearer, Basic realm="x"`, param: "realm", want: ""},
		{name: "bare Bearer between schemes", header: `Basic realm="x", Bearer, Digest realm="y"`, param: "realm", want: ""},
		{name: "quoted pair", header: `Bearer scope="a\qb\\c\"d"`, param: "scope", want: `aqb\c"d`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := parseWWWAuthParam(tc.header, tc.param); got != tc.want {
				t.Fatalf("parseWWWAuthParam(%q, %q) = %q, want %q", tc.header, tc.param, got, tc.want)
			}
		})
	}
}

func TestRequiresOAuthWithMultipleChallenges(t *testing.T) {
	if !RequiresOAuth(context.Background(), config.ServerConfig{URL: "https://mcp.example.test"},
		`Basic realm="ignored", Bearer scope="mcp:read"`) {
		t.Fatal("a separate Bearer challenge should confirm OAuth")
	}
}

func TestChallengeStorageBindsEndpointAndCredentialCleanup(t *testing.T) {
	dir := t.TempDir()
	challenge := Challenge{
		ServerURL: "https://mcp.example.test/api", StatusCode: http.StatusUnauthorized,
		WWWAuthenticate: `Bearer resource_metadata="https://mcp.example.test/prm"`,
	}
	if err := saveChallenge(dir, "srv", challenge); err != nil {
		t.Fatal(err)
	}
	if _, err := loadChallenge(dir, "srv", "https://mcp.example.test/other"); !IsNotFound(err) {
		t.Fatalf("LoadChallenge for changed endpoint = %v, want not found", err)
	}
	got, err := loadChallenge(dir, "srv", challenge.ServerURL)
	if err != nil || *got != challenge {
		t.Fatalf("LoadChallenge = %#v, %v; want %#v", got, err, challenge)
	}
	if err := DeleteCredentials(dir, "srv"); err != nil {
		t.Fatal(err)
	}
	if _, err := loadChallenge(dir, "srv", challenge.ServerURL); !IsNotFound(err) {
		t.Fatalf("LoadChallenge after credential cleanup = %v, want not found", err)
	}
}

func TestValidAuthorizationChallenge(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		header string
		want   bool
	}{
		{name: "Basic", status: 401, header: `Basic realm="mcp"`, want: false},
		{name: "Bearer", status: 401, header: `Bearer realm="mcp"`, want: true},
		{name: "bare Bearer", status: 401, header: `Bearer`, want: true},
		{name: "Bearer before Basic", status: 401, header: `Bearer, Basic realm="x"`, want: true},
		{name: "Bearer between schemes", status: 401, header: `Basic realm="x", Bearer, Digest realm="y"`, want: true},
		{name: "quoted Bearer", status: 401, header: `Basic realm="x, Bearer scope=evil"`, want: false},
		{name: "invalid token on forbidden", status: 403, header: `Bearer error="invalid_token"`, want: false},
		{name: "scope without error", status: 403, header: `Bearer scope="mcp:write"`, want: false},
		{name: "insufficient scope", status: 403, header: `Bearer error="insufficient_scope", scope="mcp:write"`, want: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := ValidAuthorizationChallenge(tc.status, tc.header); got != tc.want {
				t.Fatalf("ValidAuthorizationChallenge(%d, %q) = %t, want %t", tc.status, tc.header, got, tc.want)
			}
		})
	}
}

func TestSuccessfulLoginConsumesOnlyItsChallenge(t *testing.T) {
	for _, newer := range []bool{false, true} {
		t.Run(fmt.Sprint(newer), func(t *testing.T) {
			dir := t.TempDir()
			consumed := Challenge{
				ServerURL:       "https://mcp.example.test",
				StatusCode:      403,
				WWWAuthenticate: `Bearer error="insufficient_scope", scope="write"`,
			}
			if err := saveChallenge(dir, "srv", consumed); err != nil {
				t.Fatal(err)
			}
			current := consumed
			if newer {
				current.WWWAuthenticate = `Bearer error="insufficient_scope", scope="admin"`
				if err := saveChallenge(dir, "srv", current); err != nil {
					t.Fatal(err)
				}
			}
			login := &Login{
				ServerURL:         consumed.ServerURL,
				Resource:          consumed.ServerURL,
				Issuer:            "https://as.example.test",
				TokenURL:          "https://as.example.test/token",
				Client:            LoginClient{ID: "client"},
				Token:             &oauth2.Token{AccessToken: "new"},
				consumedChallenge: &consumed,
			}
			if err := SaveLogin(dir, "srv", login); err != nil {
				t.Fatal(err)
			}
			got, err := loadChallenge(dir, "srv", consumed.ServerURL)
			if newer {
				if err != nil || *got != current {
					t.Fatalf("new challenge lost: %#v %v", got, err)
				}
			} else if !IsNotFound(err) {
				t.Fatalf("consumed challenge remains: %#v %v", got, err)
			}
		})
	}
}
