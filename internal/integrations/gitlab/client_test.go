package gitlab

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"taskwatch/internal/integrations/httpx"
)

func server(t *testing.T, h http.HandlerFunc) *Client {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return New(srv.URL+"/", "tok", httpx.WithRetryDelays(time.Millisecond, time.Millisecond))
}

func TestRequestShape(t *testing.T) {
	var got *http.Request
	c := server(t, func(w http.ResponseWriter, r *http.Request) {
		got = r
		_, _ = w.Write([]byte(`[]`))
	})
	since := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	if _, err := c.MyMergeRequests(context.Background(), "me", since); err != nil {
		t.Fatal(err)
	}
	if got.Method != http.MethodGet || got.URL.Path != "/api/v4/merge_requests" || got.Header.Get("PRIVATE-TOKEN") != "tok" {
		t.Errorf("request = %s %s token=%q", got.Method, got.URL.Path, got.Header.Get("PRIVATE-TOKEN"))
	}
	q := got.URL.Query()
	for k, v := range map[string]string{
		"scope": "all", "state": "all", "author_username": "me", "updated_after": "2026-01-02T03:04:05Z", "per_page": "100",
	} {
		if q.Get(k) != v {
			t.Errorf("query %s = %q, want %q", k, q.Get(k), v)
		}
	}

	if _, err := c.ReviewRequests(context.Background(), "me"); err != nil {
		t.Fatal(err)
	}
	if q := got.URL.Query(); q.Get("reviewer_username") != "me" || q.Get("state") != "opened" {
		t.Errorf("review query = %v", q)
	}
}

func TestEndpoints(t *testing.T) {
	c := server(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v4/projects/7/merge_requests/3":
			if r.URL.Query().Get("include_diverged_commits_count") != "true" {
				t.Errorf("diverged count not requested: %s", r.URL.RawQuery)
			}
			_, _ = w.Write([]byte(`{"id":103,"iid":3,"project_id":7,"diverged_commits_count":4,"head_pipeline":{"id":9,"status":"failed"},"blocking_discussions_resolved":true}`))
		case "/api/v4/projects/7/merge_requests/3/approvals":
			_, _ = w.Write([]byte(`{"approvals_left":1,"approved_by":[{"user":{"username":"bob"}}]}`))
		case "/api/v4/projects/7/merge_requests/3/reviewers":
			_, _ = w.Write([]byte(`[{"user":{"username":"bob"},"state":"approved"}]`))
		case "/api/v4/projects/7/merge_requests/3/notes":
			if r.URL.Query().Get("sort") != "desc" || r.URL.Query().Get("per_page") != "50" {
				t.Errorf("notes query = %s", r.URL.RawQuery)
			}
			_, _ = w.Write([]byte(`[{"id":1,"body":"hi","system":false,"author":{"username":"bob"}}]`))
		case "/api/v4/projects/7/repository/commits/abc/refs":
			if r.URL.Query().Get("type") != "branch" {
				t.Errorf("refs query = %s", r.URL.RawQuery)
			}
			_, _ = w.Write([]byte(`[{"type":"branch","name":"main"},{"type":"branch","name":"release/1"}]`))
		case "/api/v4/user":
			_, _ = w.Write([]byte(`{"username":"me","name":"Me"}`))
		default:
			http.NotFound(w, r)
		}
	})
	ctx := context.Background()

	mr, err := c.MergeRequest(ctx, 7, 3)
	if err != nil || mr.ID != 103 || mr.DivergedCommitsCount != 4 || mr.HeadPipeline == nil || mr.HeadPipeline.Status != "failed" || !mr.BlockingDiscussionsResolved {
		t.Errorf("mr = %+v, %v", mr, err)
	}
	a, err := c.Approvals(ctx, 7, 3)
	if err != nil || a.ApprovalsLeft != 1 || len(a.ApprovedBy) != 1 || a.ApprovedBy[0].User.Username != "bob" {
		t.Errorf("approvals = %+v, %v", a, err)
	}
	rv, err := c.Reviewers(ctx, 7, 3)
	if err != nil || len(rv) != 1 || rv[0].State != "approved" {
		t.Errorf("reviewers = %+v, %v", rv, err)
	}
	notes, err := c.Notes(ctx, 7, 3)
	if err != nil || len(notes) != 1 || notes[0].Author.Username != "bob" {
		t.Errorf("notes = %+v, %v", notes, err)
	}
	branches, err := c.CommitBranches(ctx, 7, "abc")
	if err != nil || strings.Join(branches, ",") != "main,release/1" {
		t.Errorf("branches = %v, %v", branches, err)
	}
	u, err := c.CurrentUser(ctx)
	if err != nil || u.Username != "me" {
		t.Errorf("user = %+v, %v", u, err)
	}
}

func TestStatusError(t *testing.T) {
	c := server(t, func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "401 Unauthorized", http.StatusUnauthorized)
	})
	_, err := c.CurrentUser(context.Background())
	var se *httpx.StatusError
	if !errors.As(err, &se) || se.Code != 401 || !strings.Contains(se.Message, "gitlab GET /user") {
		t.Errorf("err = %#v", err)
	}
}

func TestRetriesServerErrors(t *testing.T) {
	var calls atomic.Int32
	c := server(t, func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			http.Error(w, "busy", http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write([]byte(`{"username":"me"}`))
	})
	if u, err := c.CurrentUser(context.Background()); err != nil || u.Username != "me" || calls.Load() != 2 {
		t.Errorf("user = %+v, err = %v, calls = %d", u, err, calls.Load())
	}
}

func TestMergedSHA(t *testing.T) {
	cases := []struct {
		mr   MergeRequest
		want string
	}{
		{MergeRequest{MergeCommitSHA: "m", SquashCommitSHA: "s", SHA: "h"}, "m"},
		{MergeRequest{SquashCommitSHA: "s", SHA: "h"}, "s"},
		{MergeRequest{SHA: "h"}, "h"},
		{MergeRequest{}, ""},
	}
	for _, tc := range cases {
		if got := tc.mr.MergedSHA(); got != tc.want {
			t.Errorf("MergedSHA(%+v) = %q, want %q", tc.mr, got, tc.want)
		}
	}
}

func TestMRRef(t *testing.T) {
	if got := MRRef(MergeRequest{ProjectID: 7, IID: 3}); got != "7!3" {
		t.Errorf("MRRef = %q", got)
	}
}
