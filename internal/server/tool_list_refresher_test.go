//go:build test

package server

import "testing"

func TestToolListRefresherCoalescesRequestsDuringAPass(t *testing.T) {
	starts := 0
	r := toolListRefresher{startWorker: func() { starts++ }}

	r.request()
	if !r.next() {
		t.Fatal("the first request ran no pass")
	}
	r.request()
	r.request()
	if !r.next() {
		t.Fatal("requests during a pass scheduled no further pass")
	}
	if r.next() {
		t.Fatal("two requests during one pass scheduled two further passes")
	}
	if starts != 1 {
		t.Fatalf("started %d workers while one was running, want 1", starts)
	}
	r.request()
	if starts != 2 {
		t.Fatal("a request after the worker ended started no new worker")
	}
}

func TestToolListRefresherStopEndsTheWorkerAndRefusesNewOnes(t *testing.T) {
	started := false
	r := toolListRefresher{startWorker: func() { started = true }}
	r.request()
	r.stop()
	if r.next() {
		t.Fatal("a pending pass ran after stop")
	}
	started = false
	r.request()
	if started {
		t.Fatal("a worker started after stop")
	}
}
