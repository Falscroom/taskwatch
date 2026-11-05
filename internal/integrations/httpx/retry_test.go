package httpx

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"syscall"
	"testing"
	"time"
)

type timeoutErr struct{}

func (timeoutErr) Error() string   { return "timeout" }
func (timeoutErr) Timeout() bool   { return true }
func (timeoutErr) Temporary() bool { return true }

var _ net.Error = timeoutErr{}

func TestRetryable(t *testing.T) {
	cases := []struct {
		err  error
		want bool
	}{
		{nil, false},
		{&StatusError{Code: 429}, true},
		{&StatusError{Code: 500}, true},
		{&StatusError{Code: 503}, true},
		{&StatusError{Code: 404}, false},
		{&StatusError{Code: 401}, false},
		{fmt.Errorf("wrapped: %w", &StatusError{Code: 502}), true},
		{timeoutErr{}, true},
		{fmt.Errorf("dial: %w", syscall.ECONNREFUSED), true},
		{syscall.ECONNRESET, true},
		{syscall.EPIPE, true},
		{io.EOF, true},
		{io.ErrUnexpectedEOF, true},
		{errors.New("bad json"), false},
		{context.Canceled, false},
	}
	for _, tc := range cases {
		if got := Retryable(tc.err); got != tc.want {
			t.Errorf("Retryable(%v) = %v, want %v", tc.err, got, tc.want)
		}
	}
}

var fast = []time.Duration{time.Millisecond, time.Millisecond}

func TestRetryStopsOnSuccessAndPermanentErrors(t *testing.T) {
	calls := 0
	if err := retry(context.Background(), fast, func() error { calls++; return nil }); err != nil || calls != 1 {
		t.Errorf("success: err=%v calls=%d", err, calls)
	}
	calls = 0
	permanent := &StatusError{Code: 404, Message: "nope"}
	if err := retry(context.Background(), fast, func() error { calls++; return permanent }); !errors.Is(err, permanent) || calls != 1 {
		t.Errorf("permanent: err=%v calls=%d", err, calls)
	}
}

func TestRetryGivesUpAfterAllDelays(t *testing.T) {
	calls := 0
	err := retry(context.Background(), fast, func() error { calls++; return &StatusError{Code: 503, Message: "busy"} })
	if calls != 3 || err == nil || err.Error() != "busy" {
		t.Errorf("err=%v calls=%d", err, calls)
	}
}

func TestDefaultBackoff(t *testing.T) {
	calls := 0
	started := time.Now()
	_ = retry(context.Background(), defaultDelays, func() error { calls++; return io.EOF })
	if calls != 3 {
		t.Errorf("calls = %d, want 3", calls)
	}
	if d := time.Since(started); d < 1400*time.Millisecond {
		t.Errorf("backoff too short: %v (want 500ms + 1s)", d)
	}
}

func TestRetryRecovers(t *testing.T) {
	calls := 0
	err := retry(context.Background(), fast, func() error {
		calls++
		if calls == 1 {
			return io.EOF
		}
		return nil
	})
	if err != nil || calls != 2 {
		t.Errorf("err=%v calls=%d", err, calls)
	}
}

func TestRetryCanceledDuringBackoff(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	calls := 0
	err := retry(ctx, []time.Duration{time.Hour}, func() error {
		calls++
		cancel()
		return io.EOF
	})
	if !errors.Is(err, context.Canceled) || calls != 1 {
		t.Errorf("err=%v calls=%d", err, calls)
	}
}

func TestNoDelaysNoRetries(t *testing.T) {
	calls := 0
	_ = retry(context.Background(), nil, func() error { calls++; return io.EOF })
	if calls != 1 {
		t.Errorf("calls = %d", calls)
	}
}
