//go:build test

package server

import (
	"testing"
	"time"

	"github.com/mcpmini/mini/internal/transport"
)

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

func TestToolListRefresherRefreshesOnlyStaleManagedLists(t *testing.T) {
	now := time.Unix(100, 0)
	starts := 0
	startWorker := func() { starts++ }
	for _, freshness := range []transport.Freshness{{}, transport.FreshUntil(now.Add(time.Second))} {
		r := toolListRefresher{startWorker: startWorker, freshness: freshness}
		if !isClosed(r.refreshIfStale(now)) || starts != 0 {
			t.Fatalf("a current list (%+v) started a refresh or held its reader", freshness)
		}
	}
	r := toolListRefresher{startWorker: startWorker, freshness: transport.FreshUntil(now)}
	done := r.refreshIfStale(now)
	if isClosed(done) || starts != 1 {
		t.Fatal("a stale list started no refresh")
	}
	if r.refreshIfStale(now) != done || starts != 1 {
		t.Fatal("a second reader did not join the running refresh")
	}
	r.next()
	r.published(transport.FreshUntil(now.Add(time.Hour)))
	if r.next() {
		t.Fatal("a joined reader scheduled an extra pass")
	}
	if !isClosed(done) {
		t.Fatal("the worker ended without releasing its waiters")
	}
}

func TestToolListRefresherNotificationDuringAPassKeepsTheListStale(t *testing.T) {
	now := time.Unix(100, 0)
	starts := 0
	r := toolListRefresher{startWorker: func() { starts++ }, freshness: transport.FreshUntil(now.Add(time.Hour))}
	r.request()
	r.next()
	r.request()
	r.published(transport.FreshUntil(now.Add(time.Hour)))
	if !r.next() {
		t.Fatal("a notification during a pass scheduled no follow-up pass")
	}
	if r.next() {
		t.Fatal("the failed follow-up scheduled another pass")
	}
	if starts != 1 {
		t.Fatalf("started %d workers, want the one that ran both passes", starts)
	}
	if isClosed(r.refreshIfStale(now)) {
		t.Fatal("a list published before the notification was served as fresh after the follow-up failed")
	}
}

func TestToolListRefresherNotificationLeavesAnUnmanagedListUnmanaged(t *testing.T) {
	starts := 0
	r := toolListRefresher{startWorker: func() { starts++ }}
	r.request()
	r.next()
	r.next()
	if !isClosed(r.refreshIfStale(time.Unix(100, 0))) || starts != 1 {
		t.Fatal("a reader of an unmanaged list started a refresh or waited")
	}
}

func TestToolListRefresherStopReleasesWaitersAndRefusesReaders(t *testing.T) {
	now := time.Unix(100, 0)
	starts := 0
	r := toolListRefresher{startWorker: func() { starts++ }, freshness: transport.FreshUntil(now)}
	done := r.refreshIfStale(now)
	r.stop()
	if r.next() {
		t.Fatal("a pass ran after stop")
	}
	if !isClosed(done) {
		t.Fatal("stop left a waiter blocked")
	}
	if !isClosed(r.refreshIfStale(now)) || starts != 1 {
		t.Fatal("a reader after stop started a worker or waited")
	}
}
