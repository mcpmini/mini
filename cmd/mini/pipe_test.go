//go:build test

package main

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/mcpmini/mini/internal/transport"
)

func TestCallPipeViaMCP_ConnectionError_NotMarkedAsRanOnDaemon(t *testing.T) {
	conn := &transport.FakeConnection{Err: errors.New("connection refused")}

	_, err := callPipeViaMCP(context.Background(), conn, "my_pipe", nil)
	if err == nil {
		t.Fatal("expected error")
	}
	var ranOnDaemon *errPipeRanOnDaemon
	if errors.As(err, &ranOnDaemon) {
		t.Error("connection error should not be marked as errPipeRanOnDaemon, since the daemon never ran the pipe")
	}
}

func TestCallPipeViaMCP_UnreadableResult_MarkedAsRanOnDaemon(t *testing.T) {
	conn := &transport.FakeConnection{
		Responses: map[string]json.RawMessage{
			"tools/call": json.RawMessage(`{"content":[{"type":"text","text":"not json"}],"isError":true}`),
		},
	}

	_, err := callPipeViaMCP(context.Background(), conn, "my_pipe", nil)
	if err == nil {
		t.Fatal("expected error")
	}
	var ranOnDaemon *errPipeRanOnDaemon
	if !errors.As(err, &ranOnDaemon) {
		t.Error("expected errPipeRanOnDaemon, since the daemon already executed the pipe")
	}
}

func TestCallPipeViaMCP_HappyPath(t *testing.T) {
	conn := &transport.FakeConnection{
		Responses: map[string]json.RawMessage{
			"tools/call": json.RawMessage(`{"content":[{"type":"text","text":"{\"server\":\"user\",\"tool\":\"my_pipe\",\"ok\":true,\"steps\":[]}"}]}`),
		},
	}

	result, err := callPipeViaMCP(context.Background(), conn, "my_pipe", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.OK {
		t.Error("expected result.OK = true")
	}
	if result.Tool != "my_pipe" {
		t.Errorf("tool = %q, want my_pipe", result.Tool)
	}
}
