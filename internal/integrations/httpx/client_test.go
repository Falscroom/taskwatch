package httpx

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"
	"time"
)

func newServer(t *testing.T, h http.HandlerFunc) *Client {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return New("svc", srv.URL+"/api/", func(r *http.Request) { r.Header.Set("X-Token", "tok") },
		WithRetryDelays(time.Millisecond, time.Millisecond))
}

func TestGet(t *testing.T) {
	var got *http.Request
	c := newServer(t, func(w http.ResponseWriter, r *http.Request) {
		got = r
		_, _ = w.Write([]byte(`{"name":"x"}`))
	})
	var out struct{ Name string }
	if err := c.Get(context.Background(), "/items", url.Values{"a": {"1"}}, &out); err != nil {
		t.Fatal(err)
	}
	if out.Name != "x" || got.Method != http.MethodGet || got.URL.Path != "/api/items" || got.URL.RawQuery != "a=1" ||
		got.Header.Get("X-Token") != "tok" || got.Header.Get("Accept") != "application/json" || got.Header.Get("Content-Type") != "" {
		t.Errorf("request = %s %s?%s headers=%v, out=%+v", got.Method, got.URL.Path, got.URL.RawQuery, got.Header, out)
	}
}

func TestPost(t *testing.T) {
	var body map[string]any
	var ct string
	c := newServer(t, func(w http.ResponseWriter, r *http.Request) {
		ct = r.Header.Get("Content-Type")
		data, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(data, &body)
		w.WriteHeader(http.StatusNoContent)
	})
	if err := c.Post(context.Background(), "/cmd", map[string]string{"q": "v"}, nil); err != nil {
		t.Fatal(err)
	}
	if ct != "application/json" || body["q"] != "v" {
		t.Errorf("content-type %q body %v", ct, body)
	}
}

func TestStatusError(t *testing.T) {
	c := newServer(t, func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "denied", http.StatusForbidden)
	})
	err := c.Get(context.Background(), "/user", nil, nil)
	var se *StatusError
	if !errors.As(err, &se) || se.Code != 403 || se.Message != "svc GET /user: 403 Forbidden: denied\n" {
		t.Errorf("err = %#v", err)
	}
}

func TestGetRetriesPostDoesNot(t *testing.T) {
	var calls atomic.Int32
	c := newServer(t, func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			http.Error(w, "busy", http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write([]byte(`{}`))
	})
	if err := c.Get(context.Background(), "/x", nil, &struct{}{}); err != nil || calls.Load() != 2 {
		t.Errorf("GET: err=%v calls=%d", err, calls.Load())
	}

	calls.Store(0)
	if err := c.Post(context.Background(), "/x", map[string]int{}, nil); err == nil || calls.Load() != 1 {
		t.Errorf("POST: err=%v calls=%d — must not retry", err, calls.Load())
	}
}

func TestBadJSON(t *testing.T) {
	c := newServer(t, func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(`{`)) })
	// Обрезанное тело похоже на оборванное соединение — его тоже повторяют.
	if err := c.Get(context.Background(), "/x", nil, &struct{}{}); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Errorf("err = %v", err)
	}
}

func TestBase(t *testing.T) {
	if got := New("x", "https://h/api/", nil).Base(); got != "https://h/api" {
		t.Errorf("Base = %q", got)
	}
}
