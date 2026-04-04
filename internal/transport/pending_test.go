package transport

import (
	"fmt"
	"sync"
	"testing"
	"time"
)

func TestPendingMap_registerAndDeliver(t *testing.T) {
	p := newPendingMap()
	ch := p.register(1)
	p.deliver(int64(1), &Response{Result: []byte(`"ok"`)})

	select {
	case got := <-ch:
		if string(got.Result) != `"ok"` {
			t.Errorf("unexpected result: %s", got.Result)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for deliver")
	}
}

func TestPendingMap_float64IdNormalizedToInt64(t *testing.T) {
	p := newPendingMap()
	ch := p.register(42)
	p.deliver(float64(42), &Response{Result: []byte(`"hit"`)})

	select {
	case got := <-ch:
		if string(got.Result) != `"hit"` {
			t.Errorf("unexpected result: %s", got.Result)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out")
	}
}

func TestPendingMap_removePreventsDelivery(t *testing.T) {
	p := newPendingMap()
	ch := p.register(7)
	p.remove(7)
	p.deliver(int64(7), &Response{Result: []byte(`"dropped"`)})

	select {
	case <-ch:
		t.Error("expected no delivery after remove")
	case <-time.After(50 * time.Millisecond):
	}
}

func TestPendingMap_unknownIdIsNoop(t *testing.T) {
	p := newPendingMap()
	p.deliver(int64(999), &Response{Result: []byte(`"x"`)})
}

func TestPendingMap_multipleInflightRoutedIndependently(t *testing.T) {
	p := newPendingMap()
	ch1 := p.register(1)
	ch2 := p.register(2)

	p.deliver(int64(2), &Response{Result: []byte(`"two"`)})
	p.deliver(int64(1), &Response{Result: []byte(`"one"`)})

	got1 := <-ch1
	got2 := <-ch2
	if string(got1.Result) != `"one"` || string(got2.Result) != `"two"` {
		t.Errorf("misrouted: got1=%s got2=%s", got1.Result, got2.Result)
	}
}

func registerChannels(p *pendingMap, n int) []chan *Response {
	chs := make([]chan *Response, n)
	for i := range n {
		chs[i] = p.register(int64(i))
	}
	return chs
}

func deliverAll(p *pendingMap, n int) {
	var wg sync.WaitGroup
	for i := range n {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			p.deliver(int64(id), &Response{Result: []byte(fmt.Sprintf(`%d`, id))})
		}(i)
	}
	wg.Wait()
}

func TestPendingMap_concurrentDeliversRouteCorrectly(t *testing.T) {
	const n = 100
	p := newPendingMap()
	channels := registerChannels(&p, n)
	deliverAll(&p, n)
	for i, ch := range channels {
		select {
		case resp := <-ch:
			if want := fmt.Sprintf(`%d`, i); string(resp.Result) != want {
				t.Errorf("id %d: got %s, want %s", i, resp.Result, want)
			}
		case <-time.After(time.Second):
			t.Errorf("id %d: timed out waiting for delivery", i)
		}
	}
}

func TestPendingMap_doubleDeliverAfterConsumeIsNoop(t *testing.T) {
	p := newPendingMap()
	ch := p.register(5)
	p.deliver(int64(5), &Response{Result: []byte(`"first"`)})
	<-ch
	p.deliver(int64(5), &Response{Result: []byte(`"second"`)})
}
