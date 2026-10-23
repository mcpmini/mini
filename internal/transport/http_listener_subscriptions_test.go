//go:build test

package transport

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mcpmini/mini/internal/clock"
)

func TestHTTPModernListener_acknowledgesChangesAndReconnects(t *testing.T) {
	fakeClock := clock.NewFake()
	var listens atomic.Int32
	listenIDs := make(chan any, 2)
	acknowledgements := make(chan struct{}, 2)
	notifications := make(chan struct{}, 2)
	server := modernListenerServer(t, true, func(w http.ResponseWriter, r *http.Request, req Request) {
		listen := listens.Add(1)
		listenIDs <- req.ID
		writeAck(t, w, req.ID)
		acknowledgements <- struct{}{}
		if listen == 1 {
			writeComplete(t, w, req.ID)
			return
		}
		<-r.Context().Done()
	})
	conn := mustHTTPConn(t, HTTPConnectionConfig{URL: server.URL, Clock: fakeClock})
	conn.SetNotificationHandler(func(n Notification) {
		if n.Method == NotificationToolsChanged {
			notifications <- struct{}{}
		}
	})
	t.Cleanup(func() { conn.Close() })
	if _, err := conn.Call(t.Context(), "ping", nil); err != nil {
		t.Fatalf("Call: %v", err)
	}
	advanceListenerTimer(t, fakeClock, time.Second)
	waitForListenerSignal(t, acknowledgements, "first acknowledgement was not sent")
	waitForListenerSignal(t, acknowledgements, "re-listen acknowledgement was not sent")
	waitForListenerSignal(t, notifications, "first acknowledgement did not refresh tools")
	waitForListenerSignal(t, notifications, "re-listen acknowledgement did not refresh tools")
	if got := listens.Load(); got != 2 {
		t.Errorf("listen requests = %d, want 2", got)
	}
	firstID, secondID := <-listenIDs, <-listenIDs
	if fmt.Sprint(firstID) == fmt.Sprint(secondID) {
		t.Errorf("listen request IDs = %v, %v; want a fresh ID per cycle", firstID, secondID)
	}
	select {
	case <-notifications:
		t.Error("tool-change handler fired more than once")
	default:
	}
}

func TestHTTPModernListener_dispatchesTaggedToolChange(t *testing.T) {
	notifications := make(chan struct{}, 2)
	server := modernListenerServer(t, true, func(w http.ResponseWriter, _ *http.Request, req Request) {
		writeAck(t, w, req.ID)
		writeSSEJSON(t, w, NewToolsListChangedNotification(rawID(t, req.ID)))
	})
	conn := mustHTTPConn(t, HTTPConnectionConfig{URL: server.URL})
	conn.SetNotificationHandler(func(Notification) { notifications <- struct{}{} })
	t.Cleanup(func() { conn.Close() })
	if _, err := conn.Call(t.Context(), "ping", nil); err != nil {
		t.Fatalf("Call: %v", err)
	}
	waitForListenerSignal(t, notifications, "first acknowledgement did not refresh tools")
	waitForListenerSignal(t, notifications, "tool-change notification was not dispatched")
}

func TestHTTPModernListener_firstAckAfterFailedListenRefreshes(t *testing.T) {
	fakeClock := clock.NewFake()
	var listens atomic.Int32
	acknowledgements := make(chan struct{}, 1)
	notifications := make(chan struct{}, 2)
	server := modernListenerServer(t, true, func(w http.ResponseWriter, r *http.Request, req Request) {
		if listens.Add(1) == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		writeAck(t, w, req.ID)
		acknowledgements <- struct{}{}
		<-r.Context().Done()
	})
	conn := mustHTTPConn(t, HTTPConnectionConfig{URL: server.URL, Clock: fakeClock})
	conn.SetNotificationHandler(func(n Notification) {
		if n.Method == NotificationToolsChanged {
			notifications <- struct{}{}
		}
	})
	t.Cleanup(func() { conn.Close() })
	if _, err := conn.Call(t.Context(), "ping", nil); err != nil {
		t.Fatalf("Call: %v", err)
	}
	advanceListenerTimer(t, fakeClock, time.Second)
	waitForListenerSignal(t, acknowledgements, "listen after the failed attempt was not acknowledged")
	waitForListenerSignal(t, notifications, "first acknowledgement after a failed listen did not refresh tools")
}

func TestHTTPModernListener_requestCarriesProtocolAndFilter(t *testing.T) {
	fakeClock := clock.NewFake()
	var listens atomic.Int32
	checked := make(chan struct{}, 1)
	server := modernListenerServer(t, true, func(w http.ResponseWriter, _ *http.Request, req Request) {
		listens.Add(1)
		if req.Method != MethodSubscriptionsListen || req.ID == nil {
			t.Errorf("listen request = %+v", req)
		}
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(req.Params, &fields); err != nil {
			t.Errorf("decode listen params: %v", err)
		}
		var filter ListenParams
		if err := json.Unmarshal(req.Params, &filter); err != nil || !filter.Notifications.ToolsListChanged {
			t.Errorf("listen filter = %+v, err = %v", filter, err)
		}
		var meta map[string]json.RawMessage
		if err := json.Unmarshal(fields["_meta"], &meta); err != nil || len(meta) != 3 {
			t.Errorf("listen _meta = %s, err = %v", fields["_meta"], err)
		}
		checked <- struct{}{}
		w.WriteHeader(http.StatusMethodNotAllowed)
	})
	conn := mustHTTPConn(t, HTTPConnectionConfig{URL: server.URL, Clock: fakeClock})
	t.Cleanup(func() { conn.Close() })
	if _, err := conn.Call(t.Context(), "ping", nil); err != nil {
		t.Fatalf("Call: %v", err)
	}
	waitForListenerSignal(t, checked, "modern listen request was not sent")
	waitForListenerSignal(t, conn.listener.done, "405 did not stop the modern listener")
	if got := listens.Load(); got != 1 {
		t.Errorf("listen requests after 405 = %d, want 1", got)
	}
}

func TestHTTPModernListener_stopsWhenAckOmitsToolChanges(t *testing.T) {
	server := modernListenerServer(t, true, func(w http.ResponseWriter, _ *http.Request, req Request) {
		writeSSEJSON(t, w, NewSubscriptionAck(rawID(t, req.ID), ListenNotifications{}))
	})
	conn := mustHTTPConn(t, HTTPConnectionConfig{URL: server.URL})
	t.Cleanup(func() { conn.Close() })
	if _, err := conn.Call(t.Context(), "ping", nil); err != nil {
		t.Fatalf("Call: %v", err)
	}
	waitForListenerSignal(t, conn.listener.done, "listener kept running after an acknowledgement without tool changes")
}

func TestHTTPModernListener_errorResponseIsARejectedAttempt(t *testing.T) {
	for _, contentType := range []string{"text/event-stream", "application/json"} {
		t.Run(contentType, func(t *testing.T) {
			server := modernListenerServer(t, true, func(w http.ResponseWriter, _ *http.Request, req Request) {
				w.Header().Set("Content-Type", contentType)
				message := fmt.Sprintf(`{"jsonrpc":"2.0","id":%v,"error":{"code":-32603,"message":"boom"}}`, req.ID)
				if contentType == "application/json" {
					if _, err := fmt.Fprint(w, message); err != nil {
						t.Errorf("write JSON error: %v", err)
					}
					return
				}
				writeSSEMessage(t, w, message)
			})
			conn := mustHTTPConn(t, HTTPConnectionConfig{URL: server.URL, Clock: clock.NewFake()})
			t.Cleanup(func() { conn.Close() })
			if _, err := conn.Call(t.Context(), "ping", nil); err != nil {
				t.Fatalf("Call: %v", err)
			}
			status, err := conn.consumeNotificationStream(t.Context(), &listenerState{modern: true})
			if !errors.Is(err, errListenRejected) {
				t.Fatalf("listen status %d error = %v, want errListenRejected", status, err)
			}
			if delay, next := listenerDelay(status, err, time.Second); delay != time.Second || next != 2*time.Second {
				t.Fatalf("delay after rejection = %v then %v, want backoff 1s then 2s", delay, next)
			}
		})
	}
}

func TestHTTPModernListener_401PausesUntilAuthChanges(t *testing.T) {
	fakeClock := clock.NewFake()
	provider := &fakeAuthProvider{current: "Bearer old", next: "Bearer new"}
	var attempts atomic.Int32
	server := modernListenerServer(t, true, func(w http.ResponseWriter, _ *http.Request, _ Request) {
		attempts.Add(1)
		w.WriteHeader(http.StatusUnauthorized)
	})
	conn := mustHTTPConn(t, HTTPConnectionConfig{
		URL: server.URL, Clock: fakeClock, AuthProvider: provider, AuthHeaderName: "Authorization",
	})
	if _, err := conn.Call(t.Context(), "ping", nil); err != nil {
		t.Fatalf("Call: %v", err)
	}
	advanceListenerTimerAndAwaitNextSleep(t, fakeClock, maxListenerBackoff)
	if got := attempts.Load(); got != 2 || provider.refreshCount() != 1 {
		t.Errorf("listen attempts / refreshes = %d / %d, want 2 / 1", got, provider.refreshCount())
	}
	advanceListenerTimer(t, fakeClock, maxListenerBackoff)
	if got := attempts.Load(); got != 2 {
		t.Errorf("listen attempts after unchanged rejected auth = %d, want 2", got)
	}
}

func TestHTTPModernListener_noListenWithoutAdvertisedCapability(t *testing.T) {
	var listens atomic.Int32
	server := modernListenerServer(t, false, func(w http.ResponseWriter, _ *http.Request, _ Request) {
		listens.Add(1)
		w.WriteHeader(http.StatusMethodNotAllowed)
	})
	conn := mustHTTPConn(t, HTTPConnectionConfig{URL: server.URL})
	t.Cleanup(func() { conn.Close() })
	if _, err := conn.Call(t.Context(), "ping", nil); err != nil {
		t.Fatalf("Call: %v", err)
	}
	if listens.Load() != 0 {
		t.Errorf("listen requests = %d, want 0", listens.Load())
	}
}

func TestHTTPModernListener_noListenForUnsupportedVersionError(t *testing.T) {
	srv := newJsonRPCServer(t, func(w http.ResponseWriter, r *http.Request) {
		req := decodeRequest(t, r)
		if req.Method == "server/discover" {
			response := Response{
				JsonRPC: JsonRPCVersion,
				ID:      req.ID,
				Error:   &RPCError{Code: -32022, Data: json.RawMessage(`{"supported":["2026-07-28"]}`)},
			}
			if err := json.NewEncoder(w).Encode(response); err != nil {
				t.Errorf("write unsupported-version response: %v", err)
			}
			return
		}
		writeRPCResult(t, rpcResultParams{
			Writer: w,
			ID:     req.ID,
			Result: map[string]bool{"ok": true},
		})
	})
	conn := mustHTTPConn(t, HTTPConnectionConfig{URL: srv.URL})
	if _, err := conn.Call(t.Context(), "ping", nil); err != nil {
		t.Fatalf("Call: %v", err)
	}
	if conn.listener != nil {
		t.Fatal("-32022 probe started a notification listener without a DiscoverResult")
	}
}

func TestHTTPModernListener_reprobeKeepsTheRunningListener(t *testing.T) {
	var pings atomic.Int32
	listen := listenOnly(t, func(w http.ResponseWriter, r *http.Request, req Request) {
		writeAck(t, w, req.ID)
		<-r.Context().Done()
	})
	server := newModernServer(
		t,
		modernUpstream{ListChanged: true, Handle: func(w http.ResponseWriter, r *http.Request, req Request) {
			if req.Method == "ping" && pings.Add(1) == 1 {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			listen(w, r, req)
		}},
	)
	conn := mustHTTPConn(t, HTTPConnectionConfig{URL: server.URL})
	t.Cleanup(func() { conn.Close() })
	if _, err := conn.Call(t.Context(), "ping", nil); !IsConnectionError(err) {
		t.Fatalf("first ping error = %v, want a ConnectionError that resets the era", err)
	}
	running := conn.listener
	if _, err := conn.Call(t.Context(), "ping", nil); err != nil {
		t.Fatalf("ping after re-probe: %v", err)
	}
	if conn.listener != running {
		t.Fatal("re-probe replaced the running listener")
	}
}

func TestHTTPModernListener_reprobeReplacesAStoppedListener(t *testing.T) {
	relisten := make(chan struct{}, 1)
	server := newModernServer(t, modernUpstream{
		ListChanged: true,
		Handle:      resetOnFirstPing(t, refuseFirstListen(t, relisten)),
	})
	conn := mustHTTPConn(t, HTTPConnectionConfig{URL: server.URL})
	t.Cleanup(func() { conn.Close() })
	if _, err := conn.Call(t.Context(), "ping", nil); err == nil {
		t.Fatal("first ping succeeded, want the -32022 that resets the era")
	}
	<-conn.listener.done
	if _, err := conn.Call(t.Context(), "ping", nil); err != nil {
		t.Fatalf("ping after re-probe: %v", err)
	}
	waitForListenerSignal(t, relisten, "re-probe kept the stopped listener instead of subscribing again")
}

func TestHTTPModernListener_eraResetWaitsForReprobeAndListensAgain(t *testing.T) {
	fakeClock := clock.NewFake()
	var pings, listens, gets atomic.Int32
	releaseFirst := make(chan struct{})
	relisten := make(chan struct{}, 1)
	listen := listenOnly(t, func(w http.ResponseWriter, r *http.Request, req Request) {
		writeAck(t, w, req.ID)
		if listens.Add(1) == 1 {
			<-releaseFirst
			return
		}
		relisten <- struct{}{}
		<-r.Context().Done()
	})
	server := newModernServer(t, modernUpstream{
		ListChanged: true,
		GetStreams:  &gets,
		Handle: func(w http.ResponseWriter, r *http.Request, req Request) {
			if req.Method == "ping" && pings.Add(1) == 2 {
				writeHTTPRPCError(t, w, httpRPCErrorParams{id: req.ID, code: CodeUnsupportedProtocolVersion})
				return
			}
			listen(w, r, req)
		},
	})
	conn := mustHTTPConn(t, HTTPConnectionConfig{URL: server.URL, Clock: fakeClock})
	t.Cleanup(func() { conn.Close() })
	if _, err := conn.Call(t.Context(), "ping", nil); err != nil {
		t.Fatalf("first ping: %v", err)
	}
	if _, err := conn.Call(t.Context(), "ping", nil); err == nil {
		t.Fatal("second ping succeeded, want the -32022 that resets the era")
	}
	close(releaseFirst)
	advanceListenerTimerAndAwaitNextSleep(t, fakeClock, time.Second)
	if _, err := conn.Call(t.Context(), "ping", nil); err != nil {
		t.Fatalf("ping after re-probe: %v", err)
	}
	advanceListenerTimer(t, fakeClock, time.Second)
	waitForListenerSignal(t, relisten, "listener did not subscribe again after the re-probe")
	if got := gets.Load(); got != 0 {
		t.Errorf("legacy GET streams = %d, want 0 from a modern listener", got)
	}
}

func TestHTTPModernListener_probeFinishingAfterCloseStartsNoListener(t *testing.T) {
	probing := make(chan struct{})
	releaseProbe := make(chan struct{})
	server := newJsonRPCServer(t, func(w http.ResponseWriter, r *http.Request) {
		req := decodeRequest(t, r)
		if req.Method == "server/discover" {
			close(probing)
			<-releaseProbe
			writeRPCResult(t, rpcResultParams{
				Writer: w,
				ID:     req.ID,
				Result: DiscoverResult{
					SupportedVersions: ModernVersions,
					Capabilities:      ServerCapabilities{Tools: &ToolsCapability{ListChanged: true}},
				},
			})
			return
		}
		writeRPCResult(t, rpcResultParams{
			Writer: w,
			ID:     req.ID,
			Result: map[string]bool{"ok": true},
		})
	})
	conn := mustHTTPConn(t, HTTPConnectionConfig{URL: server.URL})
	called := make(chan struct{})
	go func() {
		defer close(called)
		conn.Call(t.Context(), "ping", nil) //nolint:errcheck // Only the listener state after Close matters here.
	}()
	<-probing
	if err := conn.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	close(releaseProbe)
	<-called
	if conn.listener != nil {
		t.Fatal("a probe that finished after Close started a notification listener")
	}
}

func TestHTTPModernListener_closeCancelsStream(t *testing.T) {
	entered := make(chan struct{}, 1)
	server := modernListenerServer(t, true, func(w http.ResponseWriter, r *http.Request, _ Request) {
		entered <- struct{}{}
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	})
	conn := mustHTTPConn(t, HTTPConnectionConfig{URL: server.URL})
	if _, err := conn.Call(t.Context(), "ping", nil); err != nil {
		t.Fatalf("Call: %v", err)
	}
	waitForListenerSignal(t, entered, "listener did not open its stream")
	if err := conn.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

func waitForListenerSignal(t *testing.T, signal <-chan struct{}, failure string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	select {
	case <-signal:
	case <-ctx.Done():
		t.Fatal(failure)
	}
}

func TestListenResponseOutcome(t *testing.T) {
	rejection := &RPCError{Code: CodeInvalidParams, Message: "no"}
	if err := listenResponseOutcome(Response{ID: 1, Error: rejection}); !errors.Is(err, errListenRejected) ||
		!errors.Is(err, rejection) {
		t.Fatalf("error response outcome = %v, want a rejected listen carrying the RPC error", err)
	}
	if err := listenResponseOutcome(
		Response{ID: 1, Result: json.RawMessage(`{}`)},
	); !errors.Is(
		err,
		errListenComplete,
	) {
		t.Fatalf("result response outcome = %v, want the subscription completed", err)
	}
	if err := listenResponseOutcome(Response{ID: 1}); err != nil {
		t.Fatalf("empty response outcome = %v, want none", err)
	}
}

func TestHandleSubscriptionMessageIgnoresUnrecognizedMessages(t *testing.T) {
	conn := &HTTPConnection{}
	for _, message := range []string{`{}`, `[1]`, `"text"`, `{"id":null,"error":{"code":1}}`} {
		if err := conn.handleSubscriptionMessage(json.RawMessage(message)); err != nil {
			t.Errorf("message %s ended the subscription: %v", message, err)
		}
	}
}
