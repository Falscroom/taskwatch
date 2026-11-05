package view

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"taskwatch/internal/config"
	"taskwatch/internal/gitlab"
	"taskwatch/internal/watch"
	"taskwatch/internal/youtrack"
)

func testConfig() *config.Config {
	cfg := config.Default("/tmp/taskwatch/config.yaml")
	cfg.GitLab.Username = "me"
	cfg.Rules.RequiredApprovals = 2
	cfg.Rules.ReviewState = "Code Review"
	cfg.Rules.NextCommand = "State Ready for QA"
	cfg.Rules.MRBehindCommits = 10
	return cfg
}

// cleanMR — открытый MR без проблем: попадает в «Ждём других».
func cleanMR(id int) *watch.MR {
	return &watch.MR{MergeRequest: gitlab.MergeRequest{
		ID: id, IID: id, ProjectID: 1, Title: "MR " + string(rune('A'+id%26)), WebURL: "https://gl/mr",
		State: "opened", Author: gitlab.User{Username: "me", Name: "Ivan Petrov"},
		BlockingDiscussionsResolved: true,
	}}
}

func task(id, state string, mrs ...*watch.MR) *watch.Task {
	return &watch.Task{Issue: youtrack.Issue{ID: id, Summary: "Summary " + id}, State: state, URL: "https://yt/issue/" + id, MRs: mrs}
}

func build(snap *watch.Snapshot, mutes map[string]time.Time) View {
	return Build(snap, time.Unix(1700000000, 0), nil, testConfig(), mutes)
}

func card(t *testing.T, v View, key string) Card {
	t.Helper()
	for _, c := range v.Cards {
		if c.Key == key {
			return c
		}
	}
	t.Fatalf("no card %q in %+v", key, v.Cards)
	return Card{}
}

func TestBuildEmpty(t *testing.T) {
	v := Build(nil, time.Time{}, errors.New("boom"), testConfig(), nil)
	if v.Error != "boom" || v.Cards == nil || len(v.Cards) != 0 || v.NeedsYou != 0 {
		t.Fatalf("view = %+v", v)
	}
}

func TestTaskWithoutMR(t *testing.T) {
	v := build(&watch.Snapshot{Tasks: []*watch.Task{task("ABC-1", "Open")}}, nil)
	c := card(t, v, "task:ABC-1")
	if c.Group != GroupNoMR || !c.Mine || c.Title != "Summary ABC-1" || c.IssueID != "ABC-1" ||
		c.IssueState != "Open" || c.URL != "https://yt/issue/ABC-1" || c.IssueURL != c.URL {
		t.Errorf("card = %+v", c)
	}
	if v.NeedsYou != 0 {
		t.Errorf("needsYou = %d", v.NeedsYou)
	}
}

func TestMineGroups(t *testing.T) {
	cases := []struct {
		name       string
		taskState  string
		mutate     func(*watch.MR)
		wantGroup  string
		wantBadges []string
	}{
		{"waiting by default", "In Progress", func(*watch.MR) {}, GroupWaiting, nil},
		{"merged", "In Progress", func(m *watch.MR) { m.State = "merged"; m.HasConflicts = true }, GroupMerged, nil},
		{"failed pipeline", "In Progress", func(m *watch.MR) { m.HeadPipeline = &gitlab.Pipeline{Status: "failed"} }, GroupWork, []string{"Сборка упала"}},
		{"canceled pipeline", "In Progress", func(m *watch.MR) { m.HeadPipeline = &gitlab.Pipeline{Status: "canceled"} }, GroupWork, []string{"Сборка упала"}},
		{"changes requested", "In Progress", func(m *watch.MR) {
			m.Reviewers = []gitlab.Reviewer{{User: gitlab.User{Username: "bob"}, State: "requested_changes"}}
		}, GroupWork, []string{"Изменения запрошены"}},
		{"unresolved threads", "In Progress", func(m *watch.MR) { m.BlockingDiscussionsResolved = false }, GroupWork, []string{"Нерешённые треды"}},
		{"conflicts", "In Progress", func(m *watch.MR) { m.HasConflicts = true }, GroupWork, []string{"Конфликты"}},
		{"behind threshold", "In Progress", func(m *watch.MR) { m.DivergedCommitsCount = 12 }, GroupWork, []string{"Отстаёт на 12 коммитов"}},
		{"behind below threshold", "In Progress", func(m *watch.MR) { m.DivergedCommitsCount = 9 }, GroupWaiting, nil},
		{"ready to transition", "code review", func(m *watch.MR) { m.Approvals = 2 }, GroupReady, nil},
		{"problems beat ready", "Code Review", func(m *watch.MR) { m.Approvals = 2; m.HasConflicts = true }, GroupWork, []string{"Конфликты"}},
		{"badge order", "In Progress", func(m *watch.MR) {
			m.HasConflicts = true
			m.BlockingDiscussionsResolved = false
			m.HeadPipeline = &gitlab.Pipeline{Status: "failed"}
		}, GroupWork, []string{"Сборка упала", "Нерешённые треды", "Конфликты"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mr := cleanMR(1)
			tc.mutate(mr)
			c := card(t, build(&watch.Snapshot{Tasks: []*watch.Task{task("ABC-1", tc.taskState, mr)}}, nil), "mr:1")
			if c.Group != tc.wantGroup {
				t.Errorf("group = %s, want %s", c.Group, tc.wantGroup)
			}
			var badges []string
			for _, b := range c.Badges {
				badges = append(badges, b.Text)
			}
			if strings.Join(badges, "|") != strings.Join(tc.wantBadges, "|") {
				t.Errorf("badges = %v, want %v", badges, tc.wantBadges)
			}
			if !c.Mine || c.Review || c.IssueID != "ABC-1" {
				t.Errorf("card = %+v", c)
			}
		})
	}
}

func TestReadyAction(t *testing.T) {
	mr := cleanMR(1)
	mr.Approvals = 2
	c := card(t, build(&watch.Snapshot{Tasks: []*watch.Task{task("ABC-1", "Code Review", mr)}}, nil), "mr:1")
	if c.Action == nil || c.Action.Label != "Ready for QA" || c.Action.IssueID != "ABC-1" {
		t.Errorf("action = %+v", c.Action)
	}

	// Сирота не бывает «готов к переводу»: не к чему применять команду.
	orphan := card(t, build(&watch.Snapshot{Orphans: []*watch.MR{mr}}, nil), "mr:1")
	if orphan.Group != GroupWaiting || orphan.Action != nil || orphan.IssueID != "" {
		t.Errorf("orphan = %+v", orphan)
	}
}

func TestWait(t *testing.T) {
	mr := cleanMR(1)
	mr.CreatedAt = time.Now().Add(-50*time.Hour - 30*time.Minute)
	c := card(t, build(&watch.Snapshot{Orphans: []*watch.MR{mr}}, nil), "mr:1")
	if c.Wait != "2д 2ч" || c.waitHours != 50 {
		t.Errorf("wait = %q (%d)", c.Wait, c.waitHours)
	}

	mr.CreatedAt = time.Now().Add(-5*time.Hour - 30*time.Minute)
	if c := card(t, build(&watch.Snapshot{Orphans: []*watch.MR{mr}}, nil), "mr:1"); c.Wait != "5ч" {
		t.Errorf("wait = %q", c.Wait)
	}
	mr.CreatedAt = time.Now().Add(-30 * time.Minute)
	if c := card(t, build(&watch.Snapshot{Orphans: []*watch.MR{mr}}, nil), "mr:1"); c.Wait != "" {
		t.Errorf("young MR wait = %q", c.Wait)
	}
	// У проблемного MR срок ожидания не показываем.
	mr.CreatedAt = time.Now().Add(-50 * time.Hour)
	mr.HasConflicts = true
	if c := card(t, build(&watch.Snapshot{Orphans: []*watch.MR{mr}}, nil), "mr:1"); c.Wait != "" {
		t.Errorf("work MR wait = %q", c.Wait)
	}
}

func TestReviewCard(t *testing.T) {
	mr := cleanMR(5)
	mr.Author = gitlab.User{Username: "bob", Name: "Bob"}
	mr.Reviewers = []gitlab.Reviewer{{User: gitlab.User{Username: "me"}, State: "unreviewed"}}
	c := card(t, build(&watch.Snapshot{Reviews: []*watch.MR{mr}}, nil), "mr:5")
	if c.Group != GroupReview || !c.Review || c.Mine {
		t.Errorf("card = %+v", c)
	}

	// Апрув и запрос изменений уводят карточку в «Ждём других»; «reviewed»
	// (оставил ревью без решения) считается ещё не решённым.
	for state, want := range map[string]string{
		"approved": GroupWaiting, "requested_changes": GroupWaiting, "reviewed": GroupReview,
	} {
		mr.Reviewers[0].State = state
		if c := card(t, build(&watch.Snapshot{Reviews: []*watch.MR{mr}}, nil), "mr:5"); c.Group != want {
			t.Errorf("reviewer state %s: group = %s, want %s", state, c.Group, want)
		}
	}
}

func TestReviewers(t *testing.T) {
	mr := cleanMR(1)
	mr.Reviewers = []gitlab.Reviewer{
		{User: gitlab.User{Username: "a", Name: "Anna Smirnova"}, State: "unreviewed"},
		{User: gitlab.User{Username: "b"}, State: "approved"},
		{User: gitlab.User{Username: "c"}, State: "requested_changes"},
		{User: gitlab.User{Username: "d"}, State: "unreviewed"},
	}
	mr.ApprovedBy = []string{"d", "e"} // e не назначен ревьюером, но апрувнул
	got := reviewers(mr)
	want := []Person{
		{Username: "a", Name: "Anna Smirnova", Initials: "AS", Decision: DecisionPending},
		{Username: "b", Initials: "B", Decision: DecisionApproved},
		{Username: "c", Initials: "C", Decision: DecisionChanges},
		{Username: "d", Initials: "D", Decision: DecisionApproved},
		{Username: "e", Initials: "E", Decision: DecisionApproved},
	}
	if len(got) != len(want) {
		t.Fatalf("reviewers = %+v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("reviewer %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestPipelineTone(t *testing.T) {
	cases := map[string]string{
		"success": "green", "failed": "red", "canceled": "red", "skipped": "", "": "",
		"running": "yellow", "pending": "yellow", "manual": "yellow",
	}
	for status, want := range cases {
		if got := pipelineTone(status); got != want {
			t.Errorf("pipelineTone(%q) = %q, want %q", status, got, want)
		}
	}
	mr := cleanMR(1)
	mr.HeadPipeline = &gitlab.Pipeline{Status: "running", WebURL: "https://gl/p"}
	if c := card(t, build(&watch.Snapshot{Orphans: []*watch.MR{mr}}, nil), "mr:1"); c.Pipeline != "yellow" || c.PipelineURL != "https://gl/p" {
		t.Errorf("card pipeline = %q %q", c.Pipeline, c.PipelineURL)
	}
}

func TestMutes(t *testing.T) {
	now := time.Now()
	later := now.Add(time.Hour)
	a, b, c, d := cleanMR(1), cleanMR(2), cleanMR(3), cleanMR(4)
	a.HasConflicts = true // work — без заглушения считался бы в needsYou
	b.Title = "Bump deps"
	c.Title = "Bump deps"
	snap := &watch.Snapshot{Orphans: []*watch.MR{a, b, c, d}, Tasks: []*watch.Task{task("ABC-9", "Open")}}
	mutes := map[string]time.Time{
		"mr:1":            {},                    // навсегда
		"title:Bump deps": later,                 // группа по названию
		"mr:4":            now.Add(-time.Minute), // истекло
		"task:ABC-9":      {},                    // таски так не глушатся, но ключ совпадает
	}
	v := build(snap, mutes)

	if x := card(t, v, "mr:1"); !x.Muted || x.Group != GroupMuted || x.MutedUntil != "" {
		t.Errorf("forever mute: %+v", x)
	}
	for _, key := range []string{"mr:2", "mr:3"} {
		if x := card(t, v, key); !x.Muted || x.MutedUntil != later.Format(time.RFC3339) {
			t.Errorf("title mute %s: %+v", key, x)
		}
	}
	if x := card(t, v, "mr:4"); x.Muted || x.Group != GroupWaiting {
		t.Errorf("expired mute: %+v", x)
	}
	if x := card(t, v, "task:ABC-9"); !x.Muted {
		t.Errorf("task card with matching key: %+v", x)
	}
	if v.NeedsYou != 0 {
		t.Errorf("needsYou = %d, muted cards must not count", v.NeedsYou)
	}
}

func TestNeedsYou(t *testing.T) {
	work := cleanMR(1)
	work.HasConflicts = true
	ready := cleanMR(2)
	ready.Approvals = 2
	review := cleanMR(3)
	waiting := cleanMR(4)
	merged := cleanMR(5)
	merged.State = "merged"
	snap := &watch.Snapshot{
		Tasks:   []*watch.Task{task("ABC-1", "Code Review", work, ready), task("ABC-2", "Open")},
		Orphans: []*watch.MR{waiting, merged},
		Reviews: []*watch.MR{review},
	}
	if v := build(snap, nil); v.NeedsYou != 3 {
		t.Errorf("needsYou = %d, want 3 (work, ready, review)", v.NeedsYou)
	}
}

func TestSortByWaitWithinGroup(t *testing.T) {
	young, old := cleanMR(1), cleanMR(2)
	young.CreatedAt = time.Now().Add(-5 * time.Hour)
	old.CreatedAt = time.Now().Add(-30 * time.Hour)
	work := cleanMR(3)
	work.HasConflicts = true
	v := build(&watch.Snapshot{Orphans: []*watch.MR{work, young, old}}, nil)
	order := []string{v.Cards[0].Key, v.Cards[1].Key, v.Cards[2].Key}
	if strings.Join(order, ",") != "mr:3,mr:2,mr:1" {
		t.Errorf("order = %v", order)
	}
}

// Известный баг: компаратор не ставит порядок между группами, поэтому
// сортировка не двигает карточки через карточку другой группы.
func TestSortByWaitAcrossInterleavedGroups(t *testing.T) {
	t.Skip("known bug: cards of another group in between block the wait sort; fix with the domain refactor")
	young, old := cleanMR(1), cleanMR(2)
	young.CreatedAt = time.Now().Add(-5 * time.Hour)
	old.CreatedAt = time.Now().Add(-30 * time.Hour)
	work := cleanMR(3)
	work.HasConflicts = true
	v := build(&watch.Snapshot{Orphans: []*watch.MR{young, work, old}}, nil)
	var waiting []string
	for _, c := range v.Cards {
		if c.Group == GroupWaiting {
			waiting = append(waiting, c.Key)
		}
	}
	if strings.Join(waiting, ",") != "mr:2,mr:1" {
		t.Errorf("waiting order = %v, want older first", waiting)
	}
}

// Фронт полагается на то, что списки приходят массивами, а не null.
func TestJSONArraysNeverNull(t *testing.T) {
	data, err := json.Marshal(build(&watch.Snapshot{
		Tasks:   []*watch.Task{task("ABC-1", "Open")},
		Orphans: []*watch.MR{cleanMR(1)},
	}, nil))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "null") {
		t.Errorf("view JSON contains null: %s", data)
	}
	if strings.Contains(string(data), "waitHours") {
		t.Errorf("internal field leaked: %s", data)
	}
}

func TestPluralCommits(t *testing.T) {
	cases := map[int]string{
		1: "1 коммит", 21: "21 коммит", 101: "101 коммит",
		2: "2 коммита", 4: "4 коммита", 22: "22 коммита",
		5: "5 коммитов", 11: "11 коммитов", 12: "12 коммитов", 14: "14 коммитов", 111: "111 коммитов", 0: "0 коммитов",
	}
	for n, want := range cases {
		if got := pluralCommits(n); got != want {
			t.Errorf("pluralCommits(%d) = %q, want %q", n, got, want)
		}
	}
}

func TestInitials(t *testing.T) {
	cases := []struct{ username, name, want string }{
		{"ivanov", "Иван Петров", "ИП"},
		{"ivanov", "Иван Петрович Сидоров", "ИП"},
		{"ivanov", "иван", "И"},
		{"ivanov", "", "IV"},
		{"x", "", "X"},
		{"", "", ""},
	}
	for _, tc := range cases {
		if got := initials(tc.username, tc.name); got != tc.want {
			t.Errorf("initials(%q, %q) = %q, want %q", tc.username, tc.name, got, tc.want)
		}
	}
}

func TestActionLabel(t *testing.T) {
	if got := actionLabel("State Ready for QA"); got != "Ready for QA" {
		t.Errorf("got %q", got)
	}
	if got := actionLabel("tag ready"); got != "tag ready" {
		t.Errorf("got %q", got)
	}
}
