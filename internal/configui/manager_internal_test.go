//go:build test

package configui

import (
	"context"
	"encoding/json"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/mcpmini/mini/internal/clock"
	"github.com/mcpmini/mini/internal/testutil"
)

type fakeBackend map[string]ServerView

func (b fakeBackend) Servers(names []string) []ServerSummary {
	var out []ServerSummary
	for _, name := range names {
		if view, ok := b[name]; ok {
			out = append(out, ServerSummary{Name: name, State: view.State})
		}
	}
	return out
}

func (b fakeBackend) Server(name string) (ServerView, error) {
	view, ok := b[name]
	if !ok {
		return ServerView{}, ErrUnknownServer
	}
	return view, nil
}

type page struct {
	t     *testing.T
	m     *Manager
	clock *clock.Fake
	base  string
}

func newPage(t *testing.T) *page {
	t.Helper()
	backend := fakeBackend{
		"svc":   {Name: "svc", State: StateReady, Tools: []Tool{{Name: "getData"}, {Name: "drop", Hidden: true}}},
		"other": {Name: "other", State: StateReady},
	}
	c := clock.NewFake()
	m := NewManager(backend, c)
	t.Cleanup(m.Close)
	return &page{t: t, m: m, clock: c}
}

// open returns a fresh link's entry token and points the page at the link's host.
func (p *page) open(servers ...string) string {
	p.t.Helper()
	link, err := url.Parse(p.m.Open(servers))
	if err != nil || link.Host == "" {
		p.t.Fatalf("Open = %q (%v), want a link", link, err)
	}
	p.base = "http://" + link.Host
	return strings.TrimPrefix(link.Fragment, "t=")
}

func (p *page) redeem(entry string) string {
	p.t.Helper()
	status, body := p.do(
		request{method: http.MethodPost, path: "/api/session", body: `{"entry":"` + entry + `"}`, edit: nil},
	)
	var session struct{ Bearer string }
	if err := json.Unmarshal(body, &session); status != http.StatusOK || err != nil {
		p.t.Fatalf("redeem = %d %s, want a session", status, body)
	}
	return session.Bearer
}

type request struct {
	method, path, body string
	edit               func(*http.Request)
}

func (p *page) do(r request) (int, []byte) {
	p.t.Helper()
	req, err := http.NewRequest(r.method, p.base+r.path, strings.NewReader(r.body))
	if err != nil {
		p.t.Fatal(err)
	}
	req.Header.Set("Origin", p.base) // as a browser does on a same-origin fetch
	if r.body != "" {
		req.Header.Set("Content-Type", "application/json; charset=utf-8")
	}
	if r.edit != nil {
		r.edit(req)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		p.t.Fatal(err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		p.t.Fatal(err)
	}
	return resp.StatusCode, data
}

func bearer(token string) func(*http.Request) {
	return func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+token) }
}

// advance waits for the janitor's ticker first, so the move can't land before the ticker exists.
func (p *page) advance(d time.Duration) {
	p.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := p.clock.BlockUntilContext(ctx, 1); err != nil {
		p.t.Fatal("the janitor's ticker never started")
	}
	p.clock.Advance(d)
}

func TestPage_linkOpensASessionThatShowsItsServer(t *testing.T) {
	p := newPage(t)
	token := p.redeem(p.open("svc"))

	listStatus, list := p.do(request{method: http.MethodGet, path: "/api/servers", body: "", edit: bearer(token)})
	viewStatus, view := p.do(
		request{method: http.MethodGet, path: "/api/server?server=svc", body: "", edit: bearer(token)},
	)

	if listStatus != http.StatusOK || string(list) != `[{"name":"svc","state":"ready"}]`+"\n" {
		t.Errorf("GET /api/servers = %d %s, want only svc", listStatus, list)
	}
	want := `{"name":"svc","state":"ready","tools":[{"name":"getData","hidden":false},{"name":"drop","hidden":true}]}` + "\n"
	if viewStatus != http.StatusOK || string(view) != want {
		t.Errorf("GET /api/server = %d %s, want %s", viewStatus, view, want)
	}
}

func TestPage_serverWithNoToolsYetShowsAnEmptyList(t *testing.T) {
	p := newPage(t)
	token := p.redeem(p.open("other"))

	status, view := p.do(request{method: http.MethodGet, path: "/api/server?server=other", edit: bearer(token)})

	if want := `{"name":"other","state":"ready","tools":[]}` + "\n"; status != http.StatusOK || string(view) != want {
		t.Errorf("GET /api/server = %d %s, want %s", status, view, want)
	}
}

func TestPage_serverRemovedFromMiniIsNoLongerListed(t *testing.T) {
	p := newPage(t)
	token := p.redeem(p.open("gone"))

	listStatus, list := p.do(request{method: http.MethodGet, path: "/api/servers", edit: bearer(token)})
	viewStatus, _ := p.do(request{method: http.MethodGet, path: "/api/server?server=gone", edit: bearer(token)})

	if listStatus != http.StatusOK || string(list) != "[]\n" || viewStatus != http.StatusNotFound {
		t.Errorf("servers = %d %s, server = %d; want an empty list and 404", listStatus, list, viewStatus)
	}
}

func TestPage_servesEveryAssetWithItsSecurityHeaders(t *testing.T) {
	p := newPage(t)
	p.open("svc")
	paths := []string{"/"}
	eachAsset(t, func(path string, _ []byte) { paths = append(paths, strings.TrimPrefix(path, "assets")) })
	for _, path := range paths {
		resp, err := http.Get(p.base + path)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()

		if resp.StatusCode != http.StatusOK || !hasSecurityHeaders(resp.Header) {
			t.Errorf("GET %s = %d, headers %v; want it with the security headers", path, resp.StatusCode, resp.Header)
		}
	}
}

func hasSecurityHeaders(h http.Header) bool {
	return h.Get("Content-Security-Policy") == contentSecurityPolicy &&
		h.Get("X-Content-Type-Options") == "nosniff" && h.Get("Referrer-Policy") == "no-referrer"
}

// Upstream descriptions and schemas are untrusted, so the page may only ever insert them as text.
func TestAssets_neverInsertHTML(t *testing.T) {
	insertsHTML := regexp.MustCompile(`innerHTML|dangerouslySetInnerHTML`)
	eachAsset(t, func(path string, data []byte) {
		if !strings.HasPrefix(path, "assets/vendor/") && insertsHTML.Match(data) {
			t.Errorf("%s inserts HTML", path)
		}
	})
}

func eachAsset(t *testing.T, visit func(path string, data []byte)) {
	t.Helper()
	err := fs.WalkDir(assetFiles, "assets", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, err := assetFiles.ReadFile(path)
		visit(path, data)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestPage_rejectsRequestsWithoutTheRightHostOriginTypeOrSession(t *testing.T) {
	p := newPage(t)
	token := p.redeem(p.open("svc"))
	for name, tc := range map[string]struct {
		request
		want int
	}{
		"host other than 127.0.0.1:<port>": {method: http.MethodPost, path: "/api/touch", body: "{}", edit: func(r *http.Request) {
			bearer(token)(r)
			r.Host = strings.Replace(r.URL.Host, "127.0.0.1", "localhost", 1)
		}, want: http.StatusForbidden},
		"foreign origin": {method: http.MethodPost, path: "/api/touch", body: "{}", edit: func(r *http.Request) {
			bearer(token)(r)
			r.Header.Set("Origin", "https://attacker.example")
		}, want: http.StatusForbidden},
		"form post": {method: http.MethodPost, path: "/api/touch", body: "a=b", edit: func(r *http.Request) {
			bearer(token)(r)
			r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		}, want: http.StatusUnsupportedMediaType},
		"no bearer":          {method: http.MethodPost, path: "/api/touch", body: "{}", want: http.StatusUnauthorized},
		"server not granted": {method: http.MethodGet, path: "/api/server?server=other", edit: bearer(token), want: http.StatusNotFound},
		"unknown bearer":     {method: http.MethodPost, path: "/api/touch", body: "{}", edit: bearer("not-a-session"), want: http.StatusUnauthorized},
	} {
		t.Run(name, func(t *testing.T) {
			if status, body := p.do(tc.request); status != tc.want {
				t.Errorf("%s %s = %d %s, want %d", tc.method, tc.path, status, body, tc.want)
			}
		})
	}
}

func TestPage_refusesASpentOrStaleLink(t *testing.T) {
	for name, spoil := range map[string]func(p *page, entry string){
		"already used": func(p *page, entry string) { p.redeem(entry) },
		"a minute old": func(p *page, _ string) {
			p.advance(entryTTL / 2)
			p.open("svc") // keeps the port open past the first link's expiry
			p.advance(entryTTL / 2)
		},
	} {
		t.Run(name, func(t *testing.T) {
			p := newPage(t)
			entry := p.open("svc")
			spoil(p, entry)

			status, _ := p.do(
				request{method: http.MethodPost, path: "/api/session", body: `{"entry":"` + entry + `"}`, edit: nil},
			)

			if status != http.StatusUnauthorized {
				t.Errorf("redeem = %d, want %d", status, http.StatusUnauthorized)
			}
		})
	}
}

func TestSession_endsAfterAnHourWithoutATouch(t *testing.T) {
	for name, tc := range map[string]struct {
		touched bool
		alive   bool
	}{
		"polled with GETs": {touched: false, alive: false},
		"touched":          {touched: true, alive: true},
	} {
		t.Run(name, func(t *testing.T) {
			p := newPage(t)
			token := p.redeem(p.open("svc"))
			p.advance(idleTimeout - 10*time.Minute)
			if tc.touched {
				p.do(request{method: http.MethodPost, path: "/api/touch", body: "{}", edit: bearer(token)})
			} else {
				p.do(request{method: http.MethodGet, path: "/api/servers", edit: bearer(token)})
			}

			p.advance(20 * time.Minute)

			if _, alive := p.m.liveSession(token, false); alive != tc.alive {
				t.Errorf("session alive 1h10m in = %v, want %v", alive, tc.alive)
			}
		})
	}
}

func TestSession_endsAfterEightHoursEvenWhenTouched(t *testing.T) {
	p := newPage(t)
	token := p.redeem(p.open("svc"))
	for range 10 {
		p.advance(50 * time.Minute)
		p.m.liveSession(token, true)
	}

	if _, alive := p.m.liveSession(token, false); alive {
		t.Error("session alive after 8h20m of touches, want it ended at 8h")
	}
}

func TestManager_closesThePortOnceNothingIsLive(t *testing.T) {
	for name, live := range map[string]func(p *page){
		"unused link":     func(p *page) { p.open("svc") },
		"expired session": func(p *page) { p.redeem(p.open("svc")) },
	} {
		t.Run(name, func(t *testing.T) {
			p := newPage(t)
			live(p)
			closed := p.m.listener.closed

			p.advance(idleTimeout)

			testutil.WaitForReceive(t, closed, "the page's port to close")
			if link := p.m.Open([]string{"svc"}); link == "" {
				t.Error("Open after the port closed = \"\", want a link on a new port")
			}
		})
	}
}
