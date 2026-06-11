//go:build integration

package integration_test

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestConcurrency_20SequentialCalls(t *testing.T) {
	client := quickServer(t, map[string]string{"get_item": `{"id":1,"name":"ok"}`})

	for i := range 20 {
		e := client.execEnvelope("svc", "get_item", nil)
		if e.Error != "" {
			t.Errorf("call %d returned ok=false: %+v", i+1, e)
		}
	}
}

func TestConcurrency_100RapidSequential(t *testing.T) {
	client := quickServer(t, map[string]string{"get_item": `{"id":1}`})
	for i := range 100 {
		if client.execEnvelope("svc", "get_item", nil).Error != "" {
			t.Errorf("call %d failed", i+1)
		}
	}
}

func TestConcurrency_20ParallelClients(t *testing.T) {
	dir := mockFixtureDir(t, map[string]string{"get_item": `{"id":1}`})
	cfg := t.TempDir()
	writeFakeServer(t, cfg, "svc", dir)
	writeConfig(t, cfg, "inline_threshold: 50000\n")

	const n = 20
	clients := make([]*mcpClient, n)
	for i := range clients {
		clients[i] = startServer(t, cfg)
	}

	results := make(chan bool, n)
	for _, c := range clients {
		c := c
		go func() { results <- c.execEnvelope("svc", "get_item", nil).Error == "" }()
	}
	for range clients {
		if !<-results {
			t.Error("parallel client returned ok=false")
		}
	}
}

func TestConcurrency_parallelHTTPUpstream(t *testing.T) {
	f := newFakeHTTPMCP(t, nil)
	cfg := t.TempDir()
	writeHTTPServerYAML(t, cfg, "svc", f.srv.URL)
	writeConfig(t, cfg, "inline_threshold: 50000\n")

	const n = 10
	clients := make([]*mcpClient, n)
	for i := range clients {
		clients[i] = startServer(t, cfg)
	}

	results := make(chan bool, n)
	for _, c := range clients {
		c := c
		go func() { results <- c.execEnvelope("svc", "get_item", nil).Error == "" }()
	}
	for range clients {
		if !<-results {
			t.Error("parallel HTTP client returned ok=false")
		}
	}
}

func countRejected(client *mcpClient, n int) int {
	results := make(chan bool, n)
	for range n {
		go func() {
			// Use AllowError path to avoid t.Fatalf in goroutines (would deadlock the channel).
			text, isErr := client.execToolAllowError("svc", "get_item", nil)
			if !isErr {
				var e envelope
				json.Unmarshal([]byte(text), &e) //nolint:errcheck
				isErr = e.Error != ""
			}
			results <- isErr
		}()
	}
	var count int
	for range n {
		if <-results {
			count++
		}
	}
	return count
}

func TestConcurrency_MaxPendingRequests(t *testing.T) {
	dir := mockFixtureDir(t, map[string]string{"get_item": `{"id":1}`})
	cfg := t.TempDir()
	faultJSON := `{"tool":"get_item","method":"tools/call","type":"delay","delay_ms":2000}`
	writeFaultServer(t, faultServerParams{ConfigDir: cfg, ServerName: "svc", Fixtures: dir, FaultJSON: faultJSON, Extra: "max_pending_requests: 2\n"})
	writeConfig(t, cfg, "inline_threshold: 50000\n")
	client := startServer(t, cfg)
	if countRejected(client, 10) == 0 {
		t.Error("expected at least one request to be rejected with max_pending_requests: 2")
	}
}

func TestConcurrency_parallel(t *testing.T) {
	dir := mockFixtureDir(t, map[string]string{"get_item": `{"id":1}`})
	cfg := t.TempDir()
	writeFakeServer(t, cfg, "svc", dir)
	writeConfig(t, cfg, "inline_threshold: 50000\n")

	const n = 5
	clients := make([]*mcpClient, n)
	for i := range clients {
		clients[i] = startServer(t, cfg)
	}

	results := make(chan bool, n)
	for _, c := range clients {
		c := c
		go func() { results <- c.execEnvelope("svc", "get_item", nil).Error == "" }()
	}
	for range clients {
		if !<-results {
			t.Error("parallel client call returned ok=false")
		}
	}
}

func runNParallelExecs(client *mcpClient, n int) ([]bool, time.Duration) {
	results := make(chan bool, n)
	start := time.Now()
	for range n {
		go func() { results <- client.execEnvelope("svc", "get_item", nil).Error == "" }()
	}
	oks := make([]bool, n)
	for i := range n {
		oks[i] = <-results
	}
	return oks, time.Since(start)
}

func TestConcurrency_pipelinedRequests(t *testing.T) {
	const delay = 300 * time.Millisecond
	f := newFakeHTTPMCP(t, func(int) (int, []byte) {
		time.Sleep(delay)
		return 0, nil
	})
	cfg := t.TempDir()
	writeHTTPServerYAML(t, cfg, "svc", f.srv.URL)
	writeConfig(t, cfg, "inline_threshold: 50000\n")
	oks, elapsed := runNParallelExecs(startServer(t, cfg), 3)
	for _, ok := range oks {
		if !ok {
			t.Error("pipelined request failed")
		}
	}
	if elapsed > 2*delay {
		t.Errorf("requests appear serialized: took %v, expected ~%v", elapsed, delay)
	}
}

func assertSessionIsolation(t *testing.T, cfg string, existing *mcpClient) {
	t.Helper()
	b2, _ := json.Marshal(existing.execEnvelope("svc", "get_item", nil).Data)
	if !strings.Contains(string(b2), "secret") {
		t.Error("existing session should not be affected by persisted projection")
	}
	c3 := startServer(t, cfg)
	b3, _ := json.Marshal(c3.execEnvelope("svc", "get_item", nil).Data)
	if strings.Contains(string(b3), "secret") {
		t.Error("new session should apply persisted projection")
	}
}

func TestConcurrency_twoClientsSessionIsolation(t *testing.T) {
	dir := mockFixtureDir(t, map[string]string{"get_item": `{"id":1,"secret":"hidden"}`})
	cfg := t.TempDir()
	writeFakeServer(t, cfg, "svc", dir)
	writeConfig(t, cfg, "inline_threshold: 50000\n")
	c1 := startServer(t, cfg)
	c2 := startServer(t, cfg)
	b2, _ := json.Marshal(c2.execEnvelope("svc", "get_item", nil).Data)
	if !strings.Contains(string(b2), "secret") {
		t.Fatal("c2 should see secret before any projection")
	}
	c1.setProjection("svc", "get_item", map[string]any{"exclude_always": []string{"secret"}}, false)
	assertSessionIsolation(t, cfg, c2)
}
