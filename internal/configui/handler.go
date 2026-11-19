package configui

import (
	"embed"
	"encoding/json"
	"errors"
	"io/fs"
	"mime"
	"net/http"
	"strings"

	"github.com/mcpmini/mini/internal/response"
)

//go:embed assets
var assetFiles embed.FS

const contentSecurityPolicy = "default-src 'none'; script-src 'self'; style-src 'self'; img-src 'self' data:; " +
	"connect-src 'self'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'"

func (m *Manager) handler(host string) http.Handler {
	assets, err := fs.Sub(assetFiles, "assets")
	if err != nil {
		panic(err) // the embedded directory is fixed at build time
	}
	mux := http.NewServeMux()
	mux.Handle("GET /", http.FileServerFS(assets))
	mux.HandleFunc("POST /api/session", m.serveSession)
	mux.HandleFunc("GET /api/servers", m.withSession(m.serveServers))
	mux.HandleFunc("GET /api/server", m.withSession(m.serveServer))
	mux.HandleFunc("POST /api/touch", m.withSession(func(w http.ResponseWriter, _ *http.Request, _ session) {
		w.WriteHeader(http.StatusNoContent)
	}))
	return guard(host, mux)
}

// guard rejects what a malicious website open in the same browser could send: a rebound DNS name
// (Host), a cross-site request (Origin) or a form post (Content-Type).
func guard(host string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Security-Policy", contentSecurityPolicy)
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		if strings.HasPrefix(r.URL.Path, "/api/") {
			w.Header().Set("Cache-Control", "no-store")
		}
		if origin := r.Header.Get("Origin"); r.Host != host || origin != "" && origin != "http://"+host {
			writeError(w, apiError{http.StatusForbidden, "forbidden", "request from another site"})
			return
		}
		if r.Method == http.MethodPost && !isJSON(r.Header.Get("Content-Type")) {
			writeError(w, apiError{http.StatusUnsupportedMediaType, "unsupported_media_type", "send application/json"})
			return
		}
		next.ServeHTTP(w, r)
	})
}

func isJSON(contentType string) bool {
	mediaType, _, err := mime.ParseMediaType(contentType)
	return err == nil && mediaType == "application/json"
}

func (m *Manager) serveSession(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Entry string `json:"entry"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<10)).Decode(&req); err != nil {
		writeError(w, apiError{http.StatusBadRequest, "bad_request", "invalid session request"})
		return
	}
	bearer, s, ok := m.redeem(req.Entry)
	if !ok {
		writeExpired(w)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"bearer": bearer, "servers": s.servers})
}

type sessionHandler func(http.ResponseWriter, *http.Request, session)

// withSession admits a live bearer; only a POST counts as the user acting, so a forgotten tab that
// keeps polling still expires.
func (m *Manager) withSession(next sessionHandler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		bearer, _ := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		s, ok := m.liveSession(bearer, r.Method == http.MethodPost)
		if !ok {
			writeExpired(w)
			return
		}
		next(w, r, s)
	}
}

func (m *Manager) serveServers(w http.ResponseWriter, _ *http.Request, s session) {
	servers := m.backend.Servers(s.servers)
	if servers == nil {
		servers = []ServerSummary{}
	}
	writeJSON(w, http.StatusOK, servers)
}

func (m *Manager) serveServer(w http.ResponseWriter, r *http.Request, s session) {
	name := r.URL.Query().Get("server")
	if !s.allows(name) {
		writeError(w, apiError{http.StatusNotFound, "not_found", "no such server in this session"})
		return
	}
	view, err := m.backend.Server(name)
	if errors.Is(err, ErrUnknownServer) {
		writeError(w, apiError{http.StatusNotFound, "not_found", name + " is no longer in mini"})
		return
	}
	if err != nil {
		writeError(w, apiError{http.StatusInternalServerError, "internal", err.Error()})
		return
	}
	if view.Tools == nil {
		view.Tools = []Tool{} // a server still connecting has none yet, and the page reads a list
	}
	writeJSON(w, http.StatusOK, view)
}

func writeExpired(w http.ResponseWriter) {
	writeJSON(w, http.StatusUnauthorized, response.BuildError(response.BuildErrorParams{
		Code:    "session_expired",
		Message: "this page's session has ended",
		Action:  "run `mini auth <server>` to open it again",
	}))
}

type apiError struct {
	status        int
	code, message string
}

func writeError(w http.ResponseWriter, e apiError) {
	writeJSON(w, e.status, response.BuildError(response.BuildErrorParams{Code: e.code, Message: e.message}))
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v) //nolint:errcheck // a failed write means the page went away
}
