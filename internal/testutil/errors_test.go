package testutil

import (
	"errors"
	"fmt"
	"runtime"
	"testing"
)

type recordingTB struct {
	testing.TB
	failure string
}

func (r *recordingTB) Helper() {}

func (r *recordingTB) Fatalf(format string, args ...any) {
	r.failure = fmt.Sprintf(format, args...)
	runtime.Goexit()
}

func failureOf(check func(testing.TB)) string {
	rec := &recordingTB{}
	done := make(chan struct{})
	go func() {
		defer close(done)
		check(rec)
	}()
	<-done
	return rec.failure
}

func TestErrorContains(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want string
	}{
		{name: "matching error passes", err: errors.New("dial tcp: connection refused"), want: ""},
		{name: "nil error fails", err: nil, want: `got no error, want one containing "refused"`},
		{name: "other error fails", err: errors.New("timeout"), want: `error "timeout" does not contain "refused"`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := failureOf(func(tb testing.TB) { ErrorContains(tb, tc.err, "refused") })
			if got != tc.want {
				t.Errorf("failure = %q, want %q", got, tc.want)
			}
		})
	}
}
