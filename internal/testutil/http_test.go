package testutil

import (
	"io"
	"net/http"
	"testing"
)

func TestServe_writeJSONSetsStatusTypeAndBody(t *testing.T) {
	srv := Serve(t, func(w http.ResponseWriter, _ *http.Request) {
		WriteJSON(w, http.StatusCreated, map[string]string{"id": "1"})
	})

	resp, err := http.Get(srv.URL) //nolint:noctx // local server that answers at once
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close() //nolint:errcheck // read fully below; nothing to report on close
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}

	if resp.StatusCode != http.StatusCreated || resp.Header.Get("Content-Type") != "application/json" {
		t.Errorf("got %d %q, want 201 application/json", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
	if string(body) != "{\"id\":\"1\"}\n" {
		t.Errorf("body = %q, want the encoded value", body)
	}
}
