//go:build test

package forge

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os/exec"
	"strings"
	"sync"
	"testing"
)

type fakeBridge struct {
	mu         sync.Mutex
	calls      []bridgeCallRecord
	listResult any
	callResult json.RawMessage
	callErr    error
}

type bridgeCallRecord struct {
	Server string
	Tool   string
	Params map[string]any
}

func (f *fakeBridge) ListTools(_ context.Context) (any, error) {
	return f.listResult, nil
}

func (f *fakeBridge) CallTool(_ context.Context, server, tool string, params map[string]any) (json.RawMessage, error) {
	f.mu.Lock()
	f.calls = append(f.calls, bridgeCallRecord{Server: server, Tool: tool, Params: params})
	f.mu.Unlock()
	return f.callResult, f.callErr
}

func (f *fakeBridge) lastCall() bridgeCallRecord {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.calls) == 0 {
		return bridgeCallRecord{}
	}
	return f.calls[len(f.calls)-1]
}

func TestBridgeHTTP_auth(t *testing.T) {
	b, err := startToolBridge(context.Background(), &fakeBridge{})
	if err != nil {
		t.Fatal(err)
	}
	defer b.close()
	base := "http://" + b.hostPort()

	t.Run("missing token returns 401", func(t *testing.T) {
		resp, _ := http.Get(base + "/list") //nolint:noctx
		resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("status = %d, want 401", resp.StatusCode)
		}
	})
	t.Run("wrong token returns 401", func(t *testing.T) {
		req, _ := http.NewRequest("GET", base+"/list", nil)
		req.Header.Set("Authorization", "Bearer wrongtoken")
		resp, _ := http.DefaultClient.Do(req)
		resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("status = %d, want 401", resp.StatusCode)
		}
	})
}

func TestBridgeHTTP_routing(t *testing.T) {
	fake := &fakeBridge{
		listResult: []string{"toolA"},
		callResult: json.RawMessage(`{"ok":true}`),
	}
	b, err := startToolBridge(context.Background(), fake)
	if err != nil {
		t.Fatal(err)
	}
	defer b.close()
	base := "http://" + b.hostPort()

	t.Run("unknown path returns 404", func(t *testing.T) {
		req, _ := http.NewRequest("GET", base+"/unknown", nil)
		req.Header.Set("Authorization", "Bearer "+b.token)
		resp, _ := http.DefaultClient.Do(req)
		resp.Body.Close()
		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("status = %d, want 404", resp.StatusCode)
		}
	})
	t.Run("valid call returns raw", func(t *testing.T) {
		body := strings.NewReader(`{"server":"srv","tool":"toolA","params":{}}`)
		req, _ := http.NewRequest("POST", base+"/call", body)
		req.Header.Set("Authorization", "Bearer "+b.token)
		req.Header.Set("Content-Type", "application/json")
		resp, _ := http.DefaultClient.Do(req)
		data, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Errorf("status = %d, want 200", resp.StatusCode)
		}
		if !strings.Contains(string(data), `"ok":true`) {
			t.Errorf("body = %q, want it to contain ok:true", data)
		}
	})
}

func TestBridgeHTTP_nonJSONRawReencoded(t *testing.T) {
	f := &fakeBridge{callResult: json.RawMessage("plain text")}
	b, err := startToolBridge(context.Background(), f)
	if err != nil {
		t.Fatal(err)
	}
	defer b.close()

	body := strings.NewReader(`{"server":"s","tool":"t","params":{}}`)
	req, _ := http.NewRequest("POST", "http://"+b.hostPort()+"/call", body)
	req.Header.Set("Authorization", "Bearer "+b.token)
	req.Header.Set("Content-Type", "application/json")
	resp, _ := http.DefaultClient.Do(req)
	data, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	var s string
	if err := json.Unmarshal(data, &s); err != nil {
		t.Fatalf("body %q is not a JSON string: %v", data, err)
	}
	if s != "plain text" {
		t.Errorf("decoded string = %q, want %q", s, "plain text")
	}
}

func TestBridge_Deno_miniCall(t *testing.T) {
	if _, err := exec.LookPath("deno"); err != nil {
		t.Skip("deno not found in PATH")
	}

	fake := &fakeBridge{callResult: json.RawMessage(`{"ok":true}`)}
	got, err := Execute(context.Background(), Params{
		Code:  `async () => await mini.call("srv", "toolA", {x: 1})`,
		Tools: fake,
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	var result map[string]any
	if err := json.Unmarshal(got, &result); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if result["ok"] != true {
		t.Errorf("result = %v, want ok:true", result)
	}

	call := fake.lastCall()
	if call.Server != "srv" || call.Tool != "toolA" {
		t.Errorf("call = {%q, %q}, want {srv, toolA}", call.Server, call.Tool)
	}
	if xval, ok := call.Params["x"]; !ok || fmt.Sprint(xval) != "1" {
		t.Errorf("call.Params[x] = %v, want 1", call.Params["x"])
	}
}

func TestBridge_Deno_miniList(t *testing.T) {
	if _, err := exec.LookPath("deno"); err != nil {
		t.Skip("deno not found in PATH")
	}

	fake := &fakeBridge{listResult: []map[string]any{{"name": "myserver.toolA"}}}
	got, err := Execute(context.Background(), Params{
		Code:  `async () => await mini.list()`,
		Tools: fake,
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	var result []any
	if err := json.Unmarshal(got, &result); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if len(result) != 1 {
		t.Errorf("list result length = %d, want 1", len(result))
	}
}

func TestBridge_Deno_callToolError(t *testing.T) {
	if _, err := exec.LookPath("deno"); err != nil {
		t.Skip("deno not found in PATH")
	}

	fake := &fakeBridge{callErr: fmt.Errorf("upstream exploded")}
	_, err := Execute(context.Background(), Params{
		Code:  `async () => await mini.call("srv", "toolA", {})`,
		Tools: fake,
	})
	if err == nil {
		t.Fatal("expected an error, got nil")
	}
	ferr, ok := err.(*Error)
	if !ok {
		t.Fatalf("error type = %T, want *forge.Error", err)
	}
	if ferr.Kind != KindRuntime {
		t.Errorf("Kind = %q, want %q", ferr.Kind, KindRuntime)
	}
	if !strings.Contains(ferr.Message, "upstream exploded") {
		t.Errorf("Message = %q, want it to contain upstream exploded", ferr.Message)
	}
}

func TestBridge_Deno_nilToolsNotAvailable(t *testing.T) {
	if _, err := exec.LookPath("deno"); err != nil {
		t.Skip("deno not found in PATH")
	}

	_, err := Execute(context.Background(), Params{
		Code: `async () => await mini.call("srv", "toolA", {})`,
	})
	if err == nil {
		t.Fatal("expected an error, got nil")
	}
	ferr, ok := err.(*Error)
	if !ok {
		t.Fatalf("error type = %T, want *forge.Error", err)
	}
	if !strings.Contains(ferr.Message, "not available in this run") {
		t.Errorf("Message = %q, want it to mention not available in this run", ferr.Message)
	}
}
