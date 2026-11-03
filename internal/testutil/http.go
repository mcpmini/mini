package testutil

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// Serve starts an httptest server and closes it when the test ends.
func Serve(t testing.TB, handler http.HandlerFunc) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return srv
}

func WriteJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v) //nolint:errcheck // a failed write shows up as the client's read error
}
