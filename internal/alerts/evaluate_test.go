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
	if st.Title != "Статус: In Progress" || st.Subtitle != "ABC-1 · Summary ABC-1" || st.URL != tk.URL ||
		st.MRID != 0 || st.Kind != KindTaskState || st.Thread != "task:ABC-1" {
		t.Errorf("state event = %+v", st)
	}
	c := got["yt-comment:c1"]
	if c.Title != "Комментарий в таске" || c.Body != "alice: please check" || c.MRID != 0 ||
		c.Kind != KindComment || c.Thread != "task:ABC-1" {
		t.Errorf("comment event = %+v", c)
	}
}

func TestMREvents(t *testing.T) {
	mr := openMR(10)
	mr.TargetBranch = "main"
	mr.SourceBranch = "feature/abc-1"
	mr.Notes = []domain.Note{
		{ID: 1, Body: "looks good", Author: domain.User{Username: "bob", Name: "Bob Smith"}},
		{ID: 2, Body: "approved this", System: true, Author: domain.User{Username: "bob"}},
		{ID: 3, Body: "self note", Author: domain.User{Username: "me"}},
	}
	mr.Pipeline = &domain.Pipeline{ID: 77, Status: "failed", WebURL: "https://gl/p/77"}
	mr.HasConflicts = true
	mr.Releases = []domain.Release{{Branch: "release/1.0"}, {Branch: "release/1.1"}}

	t.Run("linked to task", func(t *testing.T) {
		got := byKey(t, Evaluate(&domain.Snapshot{Tasks: []*domain.Task{task("ABC-1", "In Progress", mr)}}, testPolicy()))
		want := map[string]string{
			"gl-note:1":                  "Комментарий в MR",
			"pipeline:77:failed":         "Сборка упала",
			"conflict:10:abc":            "Конфликт с main",
			"release:10:release/1.0":     "Попало в release/1.0",
			"release:10:release/1.1":     "Попало в release/1.1",
			"yt-state:ABC-1:In Progress": "Статус: In Progress",
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
			if e.Subtitle != "ABC-1 · Summary ABC-1" {
				t.Errorf("%s subtitle = %q", key, e.Subtitle)
			}
			wantMRID, wantThread := 10, "mr:10"
			if strings.HasPrefix(key, "yt-") {
				wantMRID, wantThread = 0, "task:ABC-1"
			}
			if e.MRID != wantMRID || e.Thread != wantThread {
				t.Errorf("%s MRID = %d, thread = %q", key, e.MRID, e.Thread)
			}
		}
		if e := got["pipeline:77:failed"]; e.URL != "https://gl/p/77" || e.Body != "Ветка feature/abc-1" || e.Kind != KindPipeline {
			t.Errorf("pipeline event = %+v", e)
		}
		if e := got["gl-note:1"]; e.Body != "Bob Smith: looks good" {
			t.Errorf("note body = %q", e.Body)
		}
		if e := got["conflict:10:abc"]; e.Body != "Нужен rebase или merge main" {
			t.Errorf("conflict body = %q", e.Body)
		}
	})

	t.Run("orphan uses MR title as subject", func(t *testing.T) {
		mr.ApprovedBy = approvers(5)
		got := byKey(t, Evaluate(&domain.Snapshot{Orphans: []*domain.MR{mr}}, testPolicy()))
		if e := got["gl-note:1"]; e.Subtitle != "!10 · Fix login" {
			t.Errorf("note subtitle = %q", e.Subtitle)
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

func TestPipelineCanceledTitle(t *testing.T) {
	mr := openMR(1)
	mr.Pipeline = &domain.Pipeline{ID: 5, Status: "canceled"}
	got := byKey(t, Evaluate(&domain.Snapshot{Orphans: []*domain.MR{mr}}, testPolicy()))
	if e := got["pipeline:5:canceled"]; e.Title != "Сборка отменена" {
		t.Errorf("title = %q", e.Title)
	}
}

// Без целевой ветки в ответе GitLab заголовок не ломается.
func TestConflictWithoutTargetBranch(t *testing.T) {
	mr := openMR(1)
	mr.HasConflicts = true
	got := byKey(t, Evaluate(&domain.Snapshot{Orphans: []*domain.MR{mr}}, testPolicy()))
	if e := got["conflict:1:abc"]; e.Title != "Конфликт в MR" || e.Body != "Нужен rebase" || e.Kind != KindConflict {
		t.Errorf("conflict event = %+v", e)
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
			if e.Title != "Апрувы собраны · "+string(rune('0'+tc.approvals))+" из 2" || e.Kind != KindApproved {
				t.Errorf("title = %q", e.Title)
			}
			wantBody := strings.Join(approvers(tc.approvals), ", ") + ". Пора переводить"
			if tc.wantTransi {
				wantBody += " в Ready for QA"
			}
			if e.Body != wantBody {
				t.Errorf("body = %q, want %q", e.Body, wantBody)
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
	waiting.Author = domain.User{Username: "anna"}
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
	if e.Title != "Нужно твоё ревью" || e.Subtitle != "!20 · Add cache" || e.Body != "anna" ||
		e.URL != "https://gl/mr" || e.MRID != 20 || e.Kind != KindReview || e.Thread != "mr:20" {
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

// Имена апрувнувших берём у ревьюеров, логин — если имени нет.
func TestApproverNames(t *testing.T) {
	mr := openMR(1)
	mr.ApprovedBy = []string{"anna", "oleg"}
	mr.Reviewers = []domain.Reviewer{{User: domain.User{Username: "anna", Name: "Анна Смирнова"}}}
	if got := approverNames(mr); got != "Анна Смирнова, oleg" {
		t.Errorf("approverNames = %q", got)
	}
}

func TestKindLoud(t *testing.T) {
	for _, k := range []Kind{KindReview, KindPipeline, KindConflict, KindApproved, KindReleaseFailed, KindChangesRequested, KindPollFailed} {
		if !k.Loud() {
			t.Errorf("kind %d must be loud", k)
		}
	}
	for _, k := range []Kind{KindComment, KindTaskState, KindRelease, KindReleaseBuilt, KindThreads, KindPollRecovered} {
		if k.Loud() {
			t.Errorf("kind %d must be quiet", k)
		}
	}
}

func TestReleaseBuildEvents(t *testing.T) {
	cases := []struct {
		status    domain.PipelineStatus
		key       string
		title     string
		body      string
		kind      Kind
		wantEvent bool
	}{
		{"failed", "release-build:10:90:failed", "Сборка релиза упала", "release/1.0 · пайплайн #90", KindReleaseFailed, true},
		{"canceled", "release-build:10:90:canceled", "Сборка релиза отменена", "release/1.0 · пайплайн #90", KindReleaseFailed, true},
		{"success", "release-build:10:90:success", "Релиз собрался", "release/1.0 · пайплайн #90", KindReleaseBuilt, true},
		{"manual", "release-build:10:90:manual", "Релиз собрался", "release/1.0 · пайплайн #90 · ждёт ручного шага", KindReleaseBuilt, true},
		{"running", "", "", "", 0, false},
	}
	for _, tc := range cases {
		t.Run(string(tc.status), func(t *testing.T) {
			mr := openMR(10)
			mr.State = domain.MRMerged
			mr.Releases = []domain.Release{{Branch: "release/1.0", Build: &domain.Pipeline{ID: 90, Status: tc.status, WebURL: "https://gl/p/90"}}}
			got := byKey(t, Evaluate(&domain.Snapshot{Tasks: []*domain.Task{task("ABC-1", "Done", mr)}}, testPolicy()))
			var builds []Event
			for k, e := range got {
				if strings.HasPrefix(k, "release-build:") {
					builds = append(builds, e)
				}
			}
			if !tc.wantEvent {
				if len(builds) != 0 {
					t.Errorf("running build must stay silent: %+v", builds)
				}
				return
			}
			e, ok := got[tc.key]
			if !ok || len(builds) != 1 {
				t.Fatalf("events = %v", got)
			}
			if e.Title != tc.title || e.Body != tc.body || e.Kind != tc.kind || e.Subtitle != "ABC-1 · Summary ABC-1" ||
				e.URL != "https://gl/p/90" || e.MRID != 10 || e.Thread != "mr:10" {
				t.Errorf("event = %+v", e)
			}
		})
	}

	// Сборки с этим MR ещё не было — только «Попало в …».
	mr := openMR(11)
	mr.State = domain.MRMerged
	mr.Releases = []domain.Release{{Branch: "release/1.0"}}
	got := byKey(t, Evaluate(&domain.Snapshot{Orphans: []*domain.MR{mr}}, testPolicy()))
	if len(got) != 1 || got["release:11:release/1.0"].Title != "Попало в release/1.0" {
		t.Errorf("events = %v", got)
	}
	if !KindReleaseFailed.Loud() || KindReleaseBuilt.Loud() {
		t.Error("a failed release build is loud, a successful one is quiet")
	}
}

// Запрошенные изменения — по уведомлению на ревьюера; нерешённые треды — одно на
// MR. Только у открытого MR.
func TestChangesAndThreadsEvents(t *testing.T) {
	mr := openMR(10)
	mr.DiscussionsResolved = false
	mr.Reviewers = []domain.Reviewer{
		{User: domain.User{Username: "anna", Name: "Анна Смирнова"}, State: domain.ReviewerRequestedChanges},
		{User: domain.User{Username: "oleg"}, State: domain.ReviewerRequestedChanges},
		{User: domain.User{Username: "eve"}, State: domain.ReviewerApproved},
	}
	got := byKey(t, Evaluate(&domain.Snapshot{Tasks: []*domain.Task{task("ABC-1", "In Progress", mr)}}, testPolicy()))
	for key, body := range map[string]string{"changes:10:anna": "Анна Смирнова", "changes:10:oleg": "oleg"} {
		e, ok := got[key]
		if !ok || e.Title != "Запрошены изменения" || e.Body != body || e.Kind != KindChangesRequested ||
			e.Subtitle != "ABC-1 · Summary ABC-1" || e.MRID != 10 || e.URL != "https://gl/mr" {
			t.Errorf("%s = %+v", key, e)
		}
	}
	if _, ok := got["changes:10:eve"]; ok {
		t.Error("an approval is not a change request")
	}
	if e := got["threads:10"]; e.Title != "Нерешённые треды" || e.Kind != KindThreads || e.MRID != 10 {
		t.Errorf("threads = %+v", e)
	}

	mr.State = domain.MRMerged
	for key := range byKey(t, Evaluate(&domain.Snapshot{Orphans: []*domain.MR{mr}}, testPolicy())) {
		if strings.HasPrefix(key, "changes:") || strings.HasPrefix(key, "threads:") {
			t.Errorf("merged MR: %s", key)
		}
	}
}
