package rules

import (
	"strings"
	"testing"

	"taskwatch/internal/config"
	"taskwatch/internal/gitlab"
	"taskwatch/internal/watch"
	"taskwatch/internal/youtrack"
)

func testConfig() *config.Config {
	cfg := config.Default("/tmp/taskwatch/config.yaml")
	cfg.GitLab.Username = "me"
	cfg.YouTrack.Login = "me"
	cfg.Rules.RequiredApprovals = 2
	cfg.Rules.ReviewState = "Code Review"
	cfg.Rules.NextCommand = "State Ready for QA"
	return cfg
}

func openMR(id int) *watch.MR {
	return &watch.MR{MergeRequest: gitlab.MergeRequest{
		ID: id, IID: id, ProjectID: 1, Title: "Fix login", WebURL: "https://gl/mr", State: "opened", SHA: "abc",
		BlockingDiscussionsResolved: true,
	}}
}

func task(id, state string, mrs ...*watch.MR) *watch.Task {
	return &watch.Task{Issue: youtrack.Issue{ID: id, Summary: "Summary " + id}, State: state, URL: "https://yt/issue/" + id, MRs: mrs}
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
	tk.Comments = []youtrack.Comment{
		{ID: "c1", Text: "  please\n\tcheck  ", Author: struct {
			Login string `json:"login"`
		}{"alice"}},
		{ID: "c2", Text: "my own", Author: struct {
			Login string `json:"login"`
		}{"me"}},
	}
	got := byKey(t, Evaluate(&watch.Snapshot{Tasks: []*watch.Task{tk}}, testConfig()))

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
	mr.Notes = []gitlab.Note{
		{ID: 1, Body: "looks good", Author: gitlab.User{Username: "bob"}},
		{ID: 2, Body: "approved this", System: true, Author: gitlab.User{Username: "bob"}},
		{ID: 3, Body: "self note", Author: gitlab.User{Username: "me"}},
	}
	mr.HeadPipeline = &gitlab.Pipeline{ID: 77, Status: "failed", WebURL: "https://gl/p/77"}
	mr.HasConflicts = true
	mr.ReleaseBranches = []string{"release/1.0", "release/1.1"}

	t.Run("linked to task", func(t *testing.T) {
		got := byKey(t, Evaluate(&watch.Snapshot{Tasks: []*watch.Task{task("ABC-1", "In Progress", mr)}}, testConfig()))
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
		got := byKey(t, Evaluate(&watch.Snapshot{Orphans: []*watch.MR{mr}}, testConfig()))
		if e := got["gl-note:1"]; e.Title != "Комментарий в MR Fix login от bob" {
			t.Errorf("note title = %q", e.Title)
		}
		if _, ok := got["approved:10"]; ok {
			t.Error("orphan MR must not produce approved event")
		}
	})
}

func TestPipelineEvents(t *testing.T) {
	for status, want := range map[string]bool{
		"failed": true, "canceled": true, "success": false, "running": false, "skipped": false,
	} {
		mr := openMR(1)
		mr.HeadPipeline = &gitlab.Pipeline{ID: 5, Status: status}
		got := byKey(t, Evaluate(&watch.Snapshot{Orphans: []*watch.MR{mr}}, testConfig()))
		if _, ok := got["pipeline:5:"+status]; ok != want {
			t.Errorf("pipeline %s: event = %v, want %v", status, ok, want)
		}
	}
}

func TestConflictOnlyForOpened(t *testing.T) {
	mr := openMR(1)
	mr.HasConflicts = true
	mr.State = "merged"
	if got := byKey(t, Evaluate(&watch.Snapshot{Orphans: []*watch.MR{mr}}, testConfig())); len(got) != 0 {
		t.Errorf("merged MR with conflicts: %v", got)
	}
}

func TestApprovedEvent(t *testing.T) {
	cases := []struct {
		name       string
		state      string // статус таски
		mrState    string
		approvals  int
		command    string
		wantEvent  bool
		wantTransi bool
	}{
		{"ready", "Code Review", "opened", 2, "State Ready for QA", true, true},
		{"state compared case-insensitively", "code review", "opened", 3, "State Ready for QA", true, true},
		{"no command — no transition", "Code Review", "opened", 2, "", true, false},
		{"not enough approvals", "Code Review", "opened", 1, "State Ready for QA", false, false},
		{"other task state", "In Progress", "opened", 2, "State Ready for QA", false, false},
		{"merged MR", "Code Review", "merged", 2, "State Ready for QA", false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := testConfig()
			cfg.Rules.NextCommand = tc.command
			mr := openMR(10)
			mr.State = tc.mrState
			mr.Approvals = tc.approvals
			got := byKey(t, Evaluate(&watch.Snapshot{Tasks: []*watch.Task{task("ABC-1", tc.state, mr)}}, cfg))
			e, ok := got["approved:10"]
			if ok != tc.wantEvent {
				t.Fatalf("approved event = %v, want %v", ok, tc.wantEvent)
			}
			if !ok {
				return
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
	// Запрошенные изменения не считаются «уже отревьюил»: событие всё равно есть.
	changes := openMR(22)
	changes.Reviewers = []gitlab.Reviewer{{User: gitlab.User{Username: "me"}, State: "requested_changes"}}

	got := byKey(t, Evaluate(&watch.Snapshot{Reviews: []*watch.MR{waiting, approved, changes}}, testConfig()))
	if len(got) != 2 {
		t.Fatalf("events = %v, want review:20 and review:22", got)
	}
	e := got["review:20"]
	if e.Title != "Нужно ревью" || e.Message != "Add cache" || e.URL != "https://gl/mr" || e.MRID != 20 {
		t.Errorf("review event = %+v", e)
	}
	if _, ok := got["review:22"]; !ok {
		t.Error("review with requested changes must still produce an event")
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
