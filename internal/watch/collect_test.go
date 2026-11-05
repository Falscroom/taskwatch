package watch

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"taskwatch/internal/config"
	"taskwatch/internal/gitlab"
	"taskwatch/internal/testutil/fakeapi"
	"taskwatch/internal/youtrack"
)

func newCollector(srv *fakeapi.Server, youtrackOn bool, mutate func(*config.Config)) *Collector {
	cfg := config.Default("/tmp/taskwatch/config.yaml")
	cfg.GitLab = config.GitLab{URL: srv.URL, Token: "gl-token", Username: "me"}
	if youtrackOn {
		cfg.YouTrack = config.YouTrack{URL: srv.URL, Token: "yt-token", Login: "me", Query: "for: me"}
	}
	if mutate != nil {
		mutate(cfg)
	}
	return NewCollector(cfg, gitlab.New(cfg.GitLab.URL, cfg.GitLab.Token), youtrack.New(cfg.YouTrack.URL, cfg.YouTrack.Token))
}

func glMR(id int, state, branch, title string) fakeapi.MR {
	return fakeapi.MR{MergeRequest: gitlab.MergeRequest{
		ID: 1000 + id, IID: id, ProjectID: 7, State: state, SourceBranch: branch, Title: title, SHA: "sha" + branch,
	}}
}

func TestCollectLinksMRsToTasks(t *testing.T) {
	srv := fakeapi.New(t)
	srv.SetIssues(
		fakeapi.Issue{Issue: youtrack.Issue{ID: "ABC-1", Summary: "Login", CustomFields: []youtrack.CustomField{fakeapi.State("Code Review")}},
			Comments: []youtrack.Comment{{ID: "c1", Text: "hi"}}},
		fakeapi.Issue{Issue: youtrack.Issue{ID: "ABC-2", Summary: "Logout"}},
	)
	linked := glMR(1, "opened", "feature/abc-1-login", "Login form")
	linked.ApprovedBy = []string{"bob", "eve"}
	linked.Reviewers = []gitlab.Reviewer{{User: gitlab.User{Username: "bob"}, State: "approved"}}
	linked.Notes = []gitlab.Note{{ID: 9, Body: "nit"}}
	byTitle := glMR(2, "opened", "hotfix", "ABC-2: logout crash")
	orphan := glMR(3, "opened", "chore/deps", "Bump deps")
	closed := glMR(4, "closed", "feature/abc-1-old", "Old attempt")
	srv.SetMine(linked, byTitle, orphan, closed)
	review := glMR(5, "opened", "feature/xyz-9", "Their MR")
	srv.SetReviews(review)

	snap, err := newCollector(srv, true, nil).Collect(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	if len(snap.Tasks) != 2 {
		t.Fatalf("tasks = %d", len(snap.Tasks))
	}
	t1, t2 := snap.Tasks[0], snap.Tasks[1]
	if t1.State != "Code Review" || t1.URL != srv.URL+"/issue/ABC-1" || len(t1.Comments) != 1 {
		t.Errorf("task 1 = %+v", t1)
	}
	if len(t1.MRs) != 1 || t1.MRs[0].IID != 1 {
		t.Fatalf("task 1 MRs = %+v", t1.MRs)
	}
	m := t1.MRs[0]
	if m.Approvals() != 2 || strings.Join(m.ApprovedBy, ",") != "bob,eve" || len(m.Reviewers) != 1 || len(m.Notes) != 1 {
		t.Errorf("linked MR details = %+v", m)
	}
	if len(t2.MRs) != 1 || t2.MRs[0].IID != 2 {
		t.Errorf("task 2 MRs (key from title) = %+v", t2.MRs)
	}
	if len(snap.Orphans) != 1 || snap.Orphans[0].IID != 3 {
		t.Errorf("orphans = %+v (closed MR must be skipped)", snap.Orphans)
	}
	if len(snap.Reviews) != 1 || snap.Reviews[0].IID != 5 {
		t.Errorf("reviews = %+v", snap.Reviews)
	}
	if n := len(snap.AllMRs()); n != 4 {
		t.Errorf("AllMRs = %d, want 4", n)
	}
}

func TestCollectGitLabOnly(t *testing.T) {
	srv := fakeapi.New(t)
	srv.SetMine(glMR(1, "opened", "feature/abc-1", "x"))
	snap, err := newCollector(srv, false, nil).Collect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.Tasks) != 0 || len(snap.Orphans) != 1 {
		t.Errorf("snap = %+v, without YouTrack every own MR is an orphan", snap)
	}
	for _, r := range srv.Requests() {
		if !strings.HasPrefix(r, "GET /api/v4/") {
			t.Errorf("unexpected request without YouTrack: %s", r)
		}
	}
}

func TestCollectRequests(t *testing.T) {
	srv := fakeapi.New(t)
	srv.SetMine(glMR(1, "opened", "a", "a"))
	if _, err := newCollector(srv, false, nil).Collect(context.Background()); err != nil {
		t.Fatal(err)
	}
	reqs := srv.Requests()
	want := []string{
		"GET /api/v4/merge_requests?author_username=me",
		"GET /api/v4/projects/7/merge_requests/1?include_diverged_commits_count=true",
		"GET /api/v4/projects/7/merge_requests/1/approvals",
		"GET /api/v4/projects/7/merge_requests/1/reviewers",
		"GET /api/v4/projects/7/merge_requests/1/notes?",
		"GET /api/v4/merge_requests?",
	}
	if len(reqs) != len(want) {
		t.Fatalf("requests = %v", reqs)
	}
	for i, prefix := range want {
		if !strings.HasPrefix(reqs[i], strings.Split(prefix, "?")[0]) {
			t.Errorf("request %d = %s, want %s…", i, reqs[i], prefix)
		}
	}
	if !strings.Contains(reqs[0], "author_username=me") || !strings.Contains(reqs[0], "state=all") ||
		!strings.Contains(reqs[0], "updated_after=") || !strings.Contains(reqs[5], "reviewer_username=me") ||
		!strings.Contains(reqs[5], "state=opened") {
		t.Errorf("list queries = %s / %s", reqs[0], reqs[5])
	}
}

func TestCollectMergedMR(t *testing.T) {
	srv := fakeapi.New(t)
	merged := glMR(1, "merged", "feature/abc-1", "x")
	merged.MergeCommitSHA = "m1"
	merged.Branches = []string{"main", "release/1.0", "release/1.1", "feature/release/x"}
	merged.ApprovedBy = []string{"bob"}
	srv.SetMine(merged)

	snap, err := newCollector(srv, false, nil).Collect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	mr := snap.Orphans[0]
	if strings.Join(mr.ReleaseBranches, ",") != "release/1.0,release/1.1" {
		t.Errorf("release branches = %v", mr.ReleaseBranches)
	}
	if mr.Approvals() != 0 || mr.Reviewers != nil || mr.Notes != nil {
		t.Errorf("merged MR must not load approvals/reviewers/notes: %+v", mr)
	}
	for _, r := range srv.Requests() {
		if strings.Contains(r, "/approvals") || strings.Contains(r, "/notes") {
			t.Errorf("unexpected request for merged MR: %s", r)
		}
	}

	// Без префикса релизных веток ветки коммита не запрашиваем вовсе.
	srv2 := fakeapi.New(t)
	srv2.SetMine(merged)
	if _, err := newCollector(srv2, false, func(c *config.Config) { c.Rules.ReleaseBranchPrefix = "" }).Collect(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, r := range srv2.Requests() {
		if strings.Contains(r, "/refs") {
			t.Errorf("refs requested without release prefix: %s", r)
		}
	}
}

func TestCollectExcludeDraft(t *testing.T) {
	srv := fakeapi.New(t)
	draft := glMR(1, "opened", "a", "Draft: a")
	draft.Draft = true
	reviewDraft := glMR(2, "opened", "b", "Draft: b")
	reviewDraft.Draft = true
	srv.SetMine(draft)
	srv.SetReviews(reviewDraft)

	snap, err := newCollector(srv, false, nil).Collect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.Orphans) != 1 || len(snap.Reviews) != 1 {
		t.Errorf("drafts shown by default: %+v", snap)
	}

	snap, err = newCollector(srv, false, func(c *config.Config) { c.Rules.ExcludeDraft = true }).Collect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.Orphans) != 0 || len(snap.Reviews) != 0 {
		t.Errorf("drafts with exclude_draft: %+v", snap)
	}
}

func TestCollectError(t *testing.T) {
	srv := fakeapi.New(t)
	srv.SetMine(glMR(1, "opened", "a", "a"))
	srv.FailWith(http.StatusForbidden)
	if _, err := newCollector(srv, true, nil).Collect(context.Background()); err == nil || !strings.Contains(err.Error(), "403") {
		t.Errorf("err = %v", err)
	}
}

func TestCollectNothingConfigured(t *testing.T) {
	srv := fakeapi.New(t)
	snap, err := newCollector(srv, false, func(c *config.Config) { c.GitLab = config.GitLab{} }).Collect(context.Background())
	if err != nil || len(snap.AllMRs()) != 0 || len(srv.Requests()) != 0 {
		t.Errorf("snap = %+v, err = %v, requests = %v", snap, err, srv.Requests())
	}
}

func TestCollectLookback(t *testing.T) {
	srv := fakeapi.New(t)
	before := time.Now().Add(-14 * 24 * time.Hour).Add(-time.Minute)
	if _, err := newCollector(srv, false, nil).Collect(context.Background()); err != nil {
		t.Fatal(err)
	}
	first := srv.Requests()[0]
	i := strings.Index(first, "updated_after=")
	raw := first[i+len("updated_after="):]
	if j := strings.IndexByte(raw, '&'); j >= 0 {
		raw = raw[:j]
	}
	raw = strings.ReplaceAll(raw, "%3A", ":")
	raw = strings.ReplaceAll(raw, "%2B", "+")
	got, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		t.Fatalf("updated_after %q: %v", raw, err)
	}
	if got.Before(before) || got.After(time.Now().Add(-14*24*time.Hour).Add(time.Minute)) {
		t.Errorf("updated_after = %v, want now-14d", got)
	}
}
