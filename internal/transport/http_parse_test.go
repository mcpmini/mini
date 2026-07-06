//go:build test

package transport

import (
	"bytes"
	"testing"
)

func TestSplitHTTPMessages_empty(t *testing.T) {
	got, err := splitHTTPMessages([]byte{})
	if err != nil {
		t.Fatal(err)
	}
	if got != nil {
		t.Fatalf("expected nil, got %q", got)
	}
}

func TestSplitHTTPMessages_whitespaceOnly(t *testing.T) {
	got, err := splitHTTPMessages([]byte("   \n  "))
	if err != nil {
		t.Fatal(err)
	}
	if got != nil {
		t.Fatalf("expected nil, got %q", got)
	}
}

func TestSplitHTTPMessages_plainJSON(t *testing.T) {
	payload := []byte(`{"jsonrpc":"2.0","id":1,"result":{"tools":[]}}`)
	got, err := splitHTTPMessages(payload)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || !bytes.Equal(got[0], payload) {
		t.Fatalf("splitHTTPMessages() = %q", got)
	}
}
