package collect_test

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"taskwatch/internal/collect"
	"taskwatch/internal/domain"
	"taskwatch/internal/integrations/gitlab"
	"taskwatch/internal/integrations/httpx"
	"taskwatch/internal/integrations/youtrack"
	"taskwatch/internal/testutil/fakeapi"
)

// setup — как собрать сборщик поверх поддельного сервера.
type setup struct {
	youtrack      bool
	gitlab        bool
	releasePrefix string
	opts          collect.Options
}

func newCollector(srv *fakeapi.Server, s setup) *collect.Collector {
	fast := httpx.WithRetryDelays()
	var tasks collect.TaskSource
	var mrs collect.MRSource
	if s.youtrack {
		tasks = youtrack.NewSource(youtrack.New(srv.URL, "yt-token", fast), "for: me")
	}
	if s.gitlab {
		mrs = gitlab.NewSource(gitlab.New(srv.URL, "gl-token", fast), "me", s.releasePrefix)
	}
	if s.opts.Lookback == 0 {
		s.opts.Lookback = 14 * 24 * time.Hour
	}
	return collect.New(tasks, mrs, s.opts)
}

var both = setup{youtrack: true, gitlab: true, releasePrefix: "release/"}
var gitlabOnly = setup{gitlab: true, releasePrefix: "release/"}

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
	srv.SetReviews(glMR(5, "opened", "feature/xyz-9", "Their MR"))

	snap, err := newCollector(srv, both).Collect(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	if len(snap.Tasks) != 2 {
		t.Fatalf("tasks = %d", len(snap.Tasks))
	}
	t1, t2 := snap.Tasks[0], snap.Tasks[1]
	if t1.State != "Code Review" || t1.URL != srv.URL+"/issue/ABC-1" || len(t1.Comments) != 1 || t1.Comments[0].Text != "hi" {
		t.Errorf("task 1 = %+v", t1)
	}
	if len(t1.MRs) != 1 || t1.MRs[0].IID != 1 {
		t.Fatalf("task 1 MRs = %+v", t1.MRs)
	}
	m := t1.MRs[0]
	if m.Approvals() != 2 || strings.Join(m.ApprovedBy, ",") != "bob,eve" || len(m.Reviewers) != 1 ||
		m.Reviewers[0].State != domain.ReviewerApproved || len(m.Notes) != 1 || m.SourceBranch != "feature/abc-1-login" {
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
}

func TestCollectGitLabOnly(t *testing.T) {
	srv := fakeapi.New(t)
	srv.SetMine(glMR(1, "opened", "feature/abc-1", "x"))
	snap, err := newCollector(srv, gitlabOnly).Collect(context.Background())
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
	if _, err := newCollector(srv, gitlabOnly).Collect(context.Background()); err != nil {
		t.Fatal(err)
	}
	// Запросы идут параллельно — сверяем набор, а не порядок.
	var paths []string
	for _, r := range srv.Requests() {
		paths = append(paths, strings.SplitN(r, "?", 2)[0])
		switch {
		case strings.Contains(r, "author_username"):
			if !strings.Contains(r, "author_username=me") || !strings.Contains(r, "state=all") || !strings.Contains(r, "updated_after=") {
				t.Errorf("authored query = %s", r)
			}
		case strings.Contains(r, "reviewer_username"):
			if !strings.Contains(r, "reviewer_username=me") || !strings.Contains(r, "state=opened") {
				t.Errorf("review query = %s", r)
			}
		}
	}
	slices.Sort(paths)
	want := []string{
		"GET /api/v4/merge_requests",
		"GET /api/v4/merge_requests",
		"GET /api/v4/projects/7/merge_requests/1",
		"GET /api/v4/projects/7/merge_requests/1/approvals",
		"GET /api/v4/projects/7/merge_requests/1/notes",
		"GET /api/v4/projects/7/merge_requests/1/reviewers",
	}
	if strings.Join(paths, "\n") != strings.Join(want, "\n") {
		t.Errorf("requests:\n%s", strings.Join(paths, "\n"))
	}
}

func TestCollectMergedMR(t *testing.T) {
	merged := glMR(1, "merged", "feature/abc-1", "x")
	merged.MergeCommitSHA = "m1"
	merged.Branches = []string{"main", "release/1.0", "release/1.1", "feature/release/x"}
	merged.ApprovedBy = []string{"bob"}

	srv := fakeapi.New(t)
	srv.SetMine(merged)
	snap, err := newCollector(srv, gitlabOnly).Collect(context.Background())
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
	if _, err := newCollector(srv2, setup{gitlab: true}).Collect(context.Background()); err != nil {
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

	snap, err := newCollector(srv, gitlabOnly).Collect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.Orphans) != 1 || len(snap.Reviews) != 1 {
		t.Errorf("drafts shown by default: %+v", snap)
	}

	s := gitlabOnly
	s.opts.ExcludeDraft = true
	snap, err = newCollector(srv, s).Collect(context.Background())
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
	if _, err := newCollector(srv, both).Collect(context.Background()); err == nil || !strings.Contains(err.Error(), "403") {
		t.Errorf("err = %v", err)
	}
}

func TestCollectNothingConfigured(t *testing.T) {
	srv := fakeapi.New(t)
	snap, err := newCollector(srv, setup{}).Collect(context.Background())
	if err != nil || len(snap.AllMRs()) != 0 || len(srv.Requests()) != 0 {
		t.Errorf("snap = %+v, err = %v, requests = %v", snap, err, srv.Requests())
	}
}

func TestCollectLookback(t *testing.T) {
	src := &fakeMRs{}
	before := time.Now().Add(-48 * time.Hour)
	if _, err := collect.New(nil, src, collect.Options{Lookback: 48 * time.Hour}).Collect(context.Background()); err != nil {
		t.Fatal(err)
	}
	if src.since.Before(before) || src.since.After(time.Now().Add(-48*time.Hour)) {
		t.Errorf("since = %v, want now-48h", src.since)
	}
}

// --- подставные источники: проверяем сам сборщик, без HTTP ---

type fakeMRs struct {
	authored, review []*domain.MR
	loadErr          error
	since            time.Time

	mu       sync.Mutex
	inFlight atomic.Int32
	peak     int32
}

func (f *fakeMRs) Authored(_ context.Context, since time.Time) ([]*domain.MR, error) {
	f.since = since
	return f.authored, nil
}

func (f *fakeMRs) ReviewRequested(context.Context) ([]*domain.MR, error) { return f.review, nil }

func (f *fakeMRs) Load(_ context.Context, mr *domain.MR) (*domain.MR, error) {
	n := f.inFlight.Add(1)
	defer f.inFlight.Add(-1)
	f.mu.Lock()
	f.peak = max(f.peak, n)
	f.mu.Unlock()
	time.Sleep(5 * time.Millisecond)
	if f.loadErr != nil {
		return nil, f.loadErr
	}
	full := *mr
	full.Notes = []domain.Note{{ID: mr.ID}} // признак, что MR догружен
	return &full, nil
}

type fakeTasks struct{ tasks []*domain.Task }

func (f *fakeTasks) Tasks(context.Context) ([]*domain.Task, error) { return f.tasks, nil }
func (f *fakeTasks) Comments(_ context.Context, id string) ([]domain.Comment, error) {
	return []domain.Comment{{ID: "c-" + id}}, nil
}

func mrs(n int, branch func(i int) string) []*domain.MR {
	out := make([]*domain.MR, n)
	for i := range out {
		out[i] = &domain.MR{ID: i + 1, State: domain.MROpened, SourceBranch: branch(i)}
	}
	return out
}

func TestCollectKeepsOrderAndLimitsConcurrency(t *testing.T) {
	src := &fakeMRs{
		authored: mrs(20, func(i int) string { return "b" }),
		review:   mrs(5, func(i int) string { return "r" }),
	}
	snap, err := collect.New(nil, src, collect.Options{Concurrency: 3}).Collect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for i, mr := range snap.Orphans {
		if mr.ID != i+1 || len(mr.Notes) != 1 {
			t.Fatalf("orphan %d = %+v: order must be kept and details loaded", i, mr)
		}
	}
	if len(snap.Reviews) != 5 || len(snap.Reviews[4].Notes) != 1 {
		t.Errorf("reviews = %+v", snap.Reviews)
	}
	if src.peak > 3 || src.peak < 2 {
		t.Errorf("peak concurrency = %d, want parallel but at most 3", src.peak)
	}
}

func TestCollectLinksByIssueKeyAndLoadsComments(t *testing.T) {
	tasks := &fakeTasks{tasks: []*domain.Task{{ID: "ABC-1"}, {ID: "ABC-2"}}}
	src := &fakeMRs{authored: mrs(3, func(i int) string { return []string{"feature/abc-2-x", "abc-1", "misc"}[i] })}
	snap, err := collect.New(tasks, src, collect.Options{}).Collect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.Tasks[0].MRs) != 1 || snap.Tasks[0].MRs[0].ID != 2 || len(snap.Tasks[1].MRs) != 1 || snap.Tasks[1].MRs[0].ID != 1 {
		t.Errorf("tasks = %+v %+v", snap.Tasks[0], snap.Tasks[1])
	}
	if len(snap.Orphans) != 1 || snap.Orphans[0].ID != 3 {
		t.Errorf("orphans = %+v", snap.Orphans)
	}
	if snap.Tasks[1].Comments[0].ID != "c-ABC-2" {
		t.Errorf("comments = %+v", snap.Tasks[1].Comments)
	}
}

func TestCollectIsStateless(t *testing.T) {
	tasks := &fakeTasks{tasks: []*domain.Task{{ID: "ABC-1"}}}
	src := &fakeMRs{authored: mrs(1, func(int) string { return "abc-1" })}
	c := collect.New(tasks, src, collect.Options{})
	for i := 0; i < 2; i++ {
		tasks.tasks = []*domain.Task{{ID: "ABC-1"}} // каждый проход — свежие таски
		snap, err := c.Collect(context.Background())
		if err != nil || len(snap.Tasks[0].MRs) != 1 {
			t.Fatalf("pass %d: %+v, %v", i, snap, err)
		}
	}
}

func TestCollectLoadError(t *testing.T) {
	src := &fakeMRs{authored: mrs(4, func(int) string { return "x" }), loadErr: errors.New("boom")}
	if _, err := collect.New(nil, src, collect.Options{}).Collect(context.Background()); err == nil || err.Error() != "boom" {
		t.Errorf("err = %v", err)
	}
}
