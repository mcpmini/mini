package server

import (
	"encoding/json"
	"fmt"
	"io"
	"sync"
)

type serializedWriter struct {
	mu        sync.Mutex
	out       io.Writer
	err       error
	cancel    func()
	onFailure func(error)
}

func newSerializedWriter(out io.Writer, cancel func()) *serializedWriter {
	return &serializedWriter{out: out, cancel: cancel}
}

func (w *serializedWriter) observeResponseFailure(out io.Writer) {
	if failed, ok := out.(interface{ ResponseFailed(error) }); ok {
		w.onFailure = failed.ResponseFailed
	}
}

func (w *serializedWriter) Write(v any) {
	w.mu.Lock()
	if w.err != nil {
		w.mu.Unlock()
		return
	}
	err := writeJSON(w.out, v)
	if err != nil {
		w.err = err
	}
	w.mu.Unlock()
	if err != nil {
		if w.onFailure != nil {
			w.onFailure(err)
		}
		w.cancel()
	}
}

func (w *serializedWriter) Err() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.err
}

func startSessionNotifyForwarder(session *Session, writeOut func(any)) func() {
	ch, ok := session.openToolsChangedStream()
	if !ok {
		return func() {}
	}
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for notification := range ch {
			session.forwardToolsChanged(notification, writeOut)
		}
	}()
	return func() { session.closeToolsChangedStream(ch); wg.Wait() }
}

type modernToolsChangedNotification struct {
	JSONRPC string                   `json:"jsonrpc"`
	Method  string                   `json:"method"`
	Params  modernToolsChangedParams `json:"params"`
}

type modernToolsChangedParams struct {
	Meta subscriptionMeta `json:"_meta"`
}

func (s *Session) forwardToolsChanged(raw json.RawMessage, writeOut func(any)) {
	if !s.modern.Load() {
		writeOut(raw)
		return
	}
	for _, id := range s.listenIDs() {
		writeOut(modernToolsChangedNotification{
			JSONRPC: "2.0", Method: "notifications/tools/list_changed",
			Params: modernToolsChangedParams{Meta: subscriptionMeta{SubscriptionID: id}},
		})
	}
}

func writeJSON(w io.Writer, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	n, err := fmt.Fprintf(w, "%s\n", b)
	if err != nil {
		return err
	}
	if n != len(b)+1 {
		return io.ErrShortWrite
	}
	return nil
}
