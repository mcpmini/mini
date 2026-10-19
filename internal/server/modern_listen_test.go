//go:build test

package server_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/server"
	"github.com/mcpmini/mini/internal/transport"
)

func TestModernListenFilterAndNoListenSuppressNotifications(t *testing.T) {
	for _, tc := range []struct {
		name   string
		listen bool
	}{
		{name: "empty filter", listen: true},
		{name: "no listen"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := newTestServer(t, server.Params{})
			if err := srv.AddConnection(
				context.Background(),
				config.ServerConfig{Name: "removeme"},
				fakeConn("ping"),
			); err != nil {
				t.Fatal(err)
			}
			lines := [][]byte{}
			if tc.listen {
				lines = append(
					lines,
					modernRPC("subscriptions/listen", 21, map[string]any{"notifications": map[string]any{}}),
				)
			}
			lines = append(
				lines,
				modernRPC(
					"tools/call",
					22,
					map[string]any{
						"name":      "config",
						"arguments": map[string]any{"action": "remove_server", "server": "removeme"},
					},
				),
			)
			messages := serveModern(t, srv, lines...)
			if tc.listen {
				ack := findMethod(messages, "notifications/subscriptions/acknowledged")
				if len(ack["params"].(map[string]any)["notifications"].(map[string]any)) != 0 {
					t.Errorf("empty filter acknowledgment = %v", ack)
				}
			}
			for _, message := range messages {
				if message["method"] == transport.NotificationToolsChanged {
					t.Errorf("unexpected tools changed notification: %v", message)
				}
			}
		})
	}
}

func TestModernConcurrentListensEachReceiveTaggedCopy(t *testing.T) {
	srv := newTestServer(t, server.Params{})
	if err := srv.AddConnection(
		context.Background(),
		config.ServerConfig{Name: "removeme"},
		fakeConn("ping"),
	); err != nil {
		t.Fatal(err)
	}
	in, inputWriter := io.Pipe()
	outputReader, out := io.Pipe()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- srv.Serve(ctx, in, out); out.Close() }()
	reader := bufio.NewReader(outputReader)
	writeLine(
		t,
		inputWriter,
		modernRPC(
			"subscriptions/listen",
			31,
			map[string]any{"notifications": map[string]any{"toolsListChanged": true}},
		),
	)
	ack := readMessage(t, reader)
	if ack["method"] != "notifications/subscriptions/acknowledged" {
		t.Fatalf("subscription acknowledgment = %v", ack)
	}
	writeLine(
		t,
		inputWriter,
		modernRPC(
			"subscriptions/listen",
			32,
			map[string]any{"notifications": map[string]any{"toolsListChanged": true}},
		),
	)
	ack = readMessage(t, reader)
	if ack["method"] != "notifications/subscriptions/acknowledged" {
		t.Fatalf("subscription acknowledgment = %v", ack)
	}
	writeLine(
		t,
		inputWriter,
		modernRPC(
			"tools/call",
			33,
			map[string]any{
				"name":      "config",
				"arguments": map[string]any{"action": "remove_server", "server": "removeme"},
			},
		),
	)
	seen := map[float64]int{}
	responseSeen := false
	for !responseSeen || len(seen) < 2 {
		message := readMessage(t, reader)
		if message["id"] == float64(33) {
			responseSeen = true
		}
		if message["method"] == transport.NotificationToolsChanged {
			meta := message["params"].(map[string]any)["_meta"].(map[string]any)
			seen[meta["io.modelcontextprotocol/subscriptionId"].(float64)]++
		}
	}
	inputWriter.Close()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if seen[31] != 1 || seen[32] != 1 || len(seen) != 2 {
		t.Fatalf("tagged notifications per subscription = %v", seen)
	}
}

func TestModernListenTaggedAndCancellation(t *testing.T) {
	srv := newTestServer(t, server.Params{})
	conn := fakeConn("ping")
	if err := srv.AddConnection(context.Background(), config.ServerConfig{Name: "removeme"}, conn); err != nil {
		t.Fatal(err)
	}
	in, inputWriter := io.Pipe()
	outputReader, out := io.Pipe()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- srv.Serve(ctx, in, out); out.Close() }()
	reader := bufio.NewReader(outputReader)
	writeLine(
		t,
		inputWriter,
		modernRPC(
			"subscriptions/listen",
			41,
			map[string]any{"notifications": map[string]any{"toolsListChanged": true}},
		),
	)
	ack := readMessage(t, reader)
	ackParams := ack["params"].(map[string]any)
	ackMeta := ackParams["_meta"].(map[string]any)
	if ack["method"] != "notifications/subscriptions/acknowledged" ||
		ackMeta["io.modelcontextprotocol/subscriptionId"] != float64(41) {
		t.Fatalf("acknowledgment = %v", ack)
	}
	writeLine(
		t,
		inputWriter,
		modernRPC(
			"tools/call",
			42,
			map[string]any{
				"name":      "config",
				"arguments": map[string]any{"action": "remove_server", "server": "removeme"},
			},
		),
	)
	change, response := readUntilChangeAndResponse(t, reader)
	changeParams := change["params"].(map[string]any)
	changeMeta := changeParams["_meta"].(map[string]any)
	if changeMeta["io.modelcontextprotocol/subscriptionId"] != float64(41) {
		t.Fatalf("notification metadata = %v", changeMeta)
	}
	if response["id"] != float64(42) {
		t.Fatalf("configure response = %v", response)
	}
	writeLine(t, inputWriter, notification(transport.NotificationCancelled, map[string]any{"requestId": 41}))
	inputWriter.Close()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	for {
		message, err := readOptionalMessage(reader)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if message["id"] == float64(41) || message["method"] == transport.NotificationToolsChanged {
			t.Fatalf("message followed cancellation: %v", message)
		}
	}
}

func writeLine(t *testing.T, writer *io.PipeWriter, line []byte) {
	t.Helper()
	if _, err := writer.Write(line); err != nil {
		t.Fatal(err)
	}
}

func readMessage(t *testing.T, reader *bufio.Reader) map[string]any {
	t.Helper()
	message, err := readOptionalMessage(reader)
	if err != nil {
		t.Fatal(err)
	}
	return message
}

func readOptionalMessage(reader *bufio.Reader) (map[string]any, error) {
	line, err := reader.ReadBytes('\n')
	if err != nil {
		return nil, err
	}
	var message map[string]any
	if err := json.Unmarshal(line, &message); err != nil {
		return nil, err
	}
	return message, nil
}

func readUntilChangeAndResponse(t *testing.T, reader *bufio.Reader) (map[string]any, map[string]any) {
	t.Helper()
	var change, response map[string]any
	for change == nil || response == nil {
		message := readMessage(t, reader)
		if message["method"] == transport.NotificationToolsChanged {
			change = message
		}
		if message["id"] == float64(42) {
			response = message
		}
	}
	return change, response
}

func TestModernListenEOFReturns(t *testing.T) {
	srv := newTestServer(t, server.Params{})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	var out bytes.Buffer
	if err := srv.Serve(ctx, bytes.NewReader(modernRPC("subscriptions/listen", 5,
		map[string]any{"notifications": map[string]any{"toolsListChanged": true}})), &out); err != nil {
		t.Fatalf("Serve with EOF and open listen: %v", err)
	}
	if len(parseMessages(out.Bytes())) != 1 {
		t.Fatalf("expected only acknowledgment at EOF: %s", out.Bytes())
	}
}

func TestModernListenRejectsWrongJSONRPCVersion(t *testing.T) {
	srv := newTestServer(t, server.Params{})
	line := bytes.Replace(
		modernRPC("subscriptions/listen", 4, map[string]any{"notifications": map[string]any{"toolsListChanged": true}}),
		[]byte(`"jsonrpc":"2.0"`), []byte(`"jsonrpc":"1.0"`), 1,
	)
	lines := rawServe(t, srv, line)
	if len(lines) != 1 {
		t.Fatalf("got %d messages, want one error: %s", len(lines), bytes.Join(lines, []byte("\n")))
	}
	var resp struct {
		Error *transport.RPCError `json:"error"`
	}
	if err := json.Unmarshal(
		lines[0],
		&resp,
	); err != nil || resp.Error == nil ||
		resp.Error.Code != transport.CodeInvalidRequest {
		t.Fatalf("response = %s, want invalid request", lines[0])
	}
}
