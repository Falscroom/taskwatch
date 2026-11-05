package alerts

import (
	"strings"
	"testing"

	"taskwatch/internal/domain"
)

func testPolicy() domain.Policy {
	return domain.Policy{
		GitLabUser: "me", YouTrackUser: "me",
		RequiredApprovals: 2, ReviewState: "Code Review", NextCommand: "State Ready for QA",
	}
}

func openMR(id int) *domain.MR {
	return &domain.MR{
		ID: id, IID: id, ProjectID: 1, Title: "Fix login", WebURL: "https://gl/mr", State: domain.MROpened, SHA: "abc",
		DiscussionsResolved: true,
	}
}

func task(id, state string, mrs ...*domain.MR) *domain.Task {
	return &domain.Task{ID: id, Summary: "Summary " + id, State: state, URL: "https://yt/issue/" + id, MRs: mrs}
}

func approvers(n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = string(rune('a' + i))
	}
	return out
}

// byKey — события по ключам; заодно проверяет, что ключи уникальны.
func byKey(t *testing.T, events []Event) map[string]Event {
	t.Helper()
	out := make(map[string]Event, len(events))
	for _, e := range events {
		if _, dup := out[e.Key]; dup {
			t.Fatalf("duplicate event key %q", e.Key)
		}
		out[e.Key] = e
	}
	return out
}

func TestTaskEvents(t *testing.T) {
	tk := task("ABC-1", "In Progress")
	tk.Comments = []domain.Comment{
		{ID: "c1", Text: "  please\n\tcheck  ", Author: "alice"},
		{ID: "c2", Text: "my own", Author: "me"},
	}
	got := byKey(t, Evaluate(&domain.Snapshot{Tasks: []*domain.Task{tk}}, testPolicy()))

	if len(got) != 2 {
		t.Fatalf("events = %v, want state + one foreign comment", got)
	}
	st := got["yt-state:ABC-1:In Progress"]
	if st.Title != "ABC-1" || st.Message != "Статус: In Progress" || st.URL != tk.URL || st.MRID != 0 {
		t.Errorf("state event = %+v", st)
	}
	c := got["yt-comment:c1"]
	if c.Title != "Комментарий в ABC-1 от alice" || c.Message != "please check" || c.MRID != 0 {
		t.Errorf("comment event = %+v", c)
	}
}

func TestMREvents(t *testing.T) {
	mr := openMR(10)
	mr.Notes = []domain.Note{
		{ID: 1, Body: "looks good", Author: domain.User{Username: "bob"}},
		{ID: 2, Body: "approved this", System: true, Author: domain.User{Username: "bob"}},
		{ID: 3, Body: "self note", Author: domain.User{Username: "me"}},
	}
	mr.Pipeline = &domain.Pipeline{ID: 77, Status: "failed", WebURL: "https://gl/p/77"}
	mr.HasConflicts = true
	mr.ReleaseBranches = []string{"release/1.0", "release/1.1"}

	t.Run("linked to task", func(t *testing.T) {
		got := byKey(t, Evaluate(&domain.Snapshot{Tasks: []*domain.Task{task("ABC-1", "In Progress", mr)}}, testPolicy()))
		want := map[string]string{
			"gl-note:1":                  "Комментарий в MR ABC-1 от bob",
			"pipeline:77:failed":         "🔴 Сборка ABC-1",
			"conflict:10:abc":            "Конфликты в ABC-1",
			"release:10:release/1.0":     "🚀 ABC-1 в release/1.0",
			"release:10:release/1.1":     "🚀 ABC-1 в release/1.1",
			"yt-state:ABC-1:In Progress": "ABC-1",
		}
		if len(got) != len(want) {
			t.Fatalf("got %d events %v, want %d", len(got), got, len(want))
		}
		for key, title := range want {
			e, ok := got[key]
			if !ok {
				t.Errorf("missing %s", key)
				continue
			}
			if e.Title != title {
				t.Errorf("%s title = %q, want %q", key, e.Title, title)
			}
			wantMRID := 10
			if strings.HasPrefix(key, "yt-") {
				wantMRID = 0
			}
			if e.MRID != wantMRID {
				t.Errorf("%s MRID = %d, want %d", key, e.MRID, wantMRID)
			}
		}
		if got["pipeline:77:failed"].URL != "https://gl/p/77" || got["pipeline:77:failed"].Message != "failed" {
			t.Errorf("pipeline event = %+v", got["pipeline:77:failed"])
		}
	})

	t.Run("orphan uses MR title as label", func(t *testing.T) {
		mr.ApprovedBy = approvers(5)
		got := byKey(t, Evaluate(&domain.Snapshot{Orphans: []*domain.MR{mr}}, testPolicy()))
		if e := got["gl-note:1"]; e.Title != "Комментарий в MR Fix login от bob" {
			t.Errorf("note title = %q", e.Title)
		}
		if _, ok := got["approved:10"]; ok {
			t.Error("orphan MR must not produce approved event")
		}
	})
}

func TestPipelineEvents(t *testing.T) {
	for status, want := range map[domain.PipelineStatus]bool{
		"failed": true, "canceled": true, "success": false, "running": false, "skipped": false,
	} {
		mr := openMR(1)
		mr.Pipeline = &domain.Pipeline{ID: 5, Status: status}
		got := byKey(t, Evaluate(&domain.Snapshot{Orphans: []*domain.MR{mr}}, testPolicy()))
		if _, ok := got["pipeline:5:"+string(status)]; ok != want {
			t.Errorf("pipeline %s: event = %v, want %v", status, ok, want)
		}
	}
}

func TestConflictOnlyForOpened(t *testing.T) {
	mr := openMR(1)
	mr.HasConflicts = true
	mr.State = domain.MRMerged
	if got := byKey(t, Evaluate(&domain.Snapshot{Orphans: []*domain.MR{mr}}, testPolicy())); len(got) != 0 {
		t.Errorf("merged MR with conflicts: %v", got)
	}
}

func TestApprovedEvent(t *testing.T) {
	cases := []struct {
		name       string
		state      string // статус таски
		mrState    domain.MRState
		approvals  int
		command    string
		wantEvent  bool
		wantTransi bool
	}{
		{"ready", "Code Review", domain.MROpened, 2, "State Ready for QA", true, true},
		{"state compared case-insensitively", "code review", domain.MROpened, 3, "State Ready for QA", true, true},
		{"no command — no transition", "Code Review", domain.MROpened, 2, "", true, false},
		{"not enough approvals", "Code Review", domain.MROpened, 1, "State Ready for QA", false, false},
		{"other task state", "In Progress", domain.MROpened, 2, "State Ready for QA", false, false},
		{"merged MR", "Code Review", domain.MRMerged, 2, "State Ready for QA", false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := testPolicy()
			p.NextCommand = tc.command
			mr := openMR(10)
			mr.State = tc.mrState
			mr.ApprovedBy = approvers(tc.approvals)
			got := byKey(t, Evaluate(&domain.Snapshot{Tasks: []*domain.Task{task("ABC-1", tc.state, mr)}}, p))
			e, ok := got["approved:10"]
			if ok != tc.wantEvent {
				t.Fatalf("approved event = %v, want %v", ok, tc.wantEvent)
			}
			if !ok {
				return
			}
			if e.Title != "✅ ABC-1: "+string(rune('0'+tc.approvals))+" апрува" {
				t.Errorf("title = %q", e.Title)
			}
			if (e.Transition != nil) != tc.wantTransi {
				t.Fatalf("transition = %+v, want %v", e.Transition, tc.wantTransi)
			}
			if e.Transition != nil && (e.Transition.IssueID != "ABC-1" || e.Transition.Command != tc.command) {
				t.Errorf("transition = %+v", e.Transition)
			}
			if e.URL != "https://yt/issue/ABC-1" || e.MRID != 10 {
				t.Errorf("event = %+v", e)
			}
		})
	}
}

func TestReviewEvents(t *testing.T) {
	waiting := openMR(20)
	waiting.Title = "Add cache"
	approved := openMR(21)
	approved.ApprovedBy = []string{"me"}
	// Запросил изменения — тоже своё слово сказал: «Нужно ревью» уже не нужно.
	changes := openMR(22)
	changes.Reviewers = []domain.Reviewer{{User: domain.User{Username: "me"}, State: domain.ReviewerRequestedChanges}}
	// Оставил ревью без решения — всё ещё ждут.
	commented := openMR(23)
	commented.Reviewers = []domain.Reviewer{{User: domain.User{Username: "me"}, State: domain.ReviewerReviewed}}

	got := byKey(t, Evaluate(&domain.Snapshot{Reviews: []*domain.MR{waiting, approved, changes, commented}}, testPolicy()))
	if len(got) != 2 {
		t.Fatalf("events = %v, want review:20 and review:23", got)
	}
	e := got["review:20"]
	if e.Title != "Нужно ревью" || e.Message != "Add cache" || e.URL != "https://gl/mr" || e.MRID != 20 {
		t.Errorf("review event = %+v", e)
	}
	if _, ok := got["review:23"]; !ok {
		t.Error("review without decision must still produce an event")
	}
}

func TestShort(t *testing.T) {
	long := strings.Repeat("я", 141)
	cases := map[string]string{
		"\ta \n b\t c\n":         "a b c",
		strings.Repeat("я", 140): strings.Repeat("я", 140),
		long:                     strings.Repeat("я", 140) + "…",
		"":                       "",
	}
	for in, want := range cases {
		if got := short(in); got != want {
			t.Errorf("short(%q) = %q, want %q", in, got, want)
		}
	}
}
