package view

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"taskwatch/internal/domain"
)

func testPolicy() domain.Policy {
	return domain.Policy{
		GitLabUser: "me", RequiredApprovals: 2, ReviewState: "Code Review",
		NextCommand: "State Ready for QA", BehindThreshold: 10,
	}
}

// cleanMR — открытый MR без проблем: попадает в «Ждём других».
func cleanMR(id int) *domain.MR {
	return &domain.MR{
		ID: id, IID: id, ProjectID: 1, Title: "MR " + string(rune('A'+id%26)), WebURL: "https://gl/mr",
		State: domain.MROpened, Author: domain.User{Username: "me", Name: "Ivan Petrov"},
		DiscussionsResolved: true,
	}
}

func task(id, state string, mrs ...*domain.MR) *domain.Task {
	return &domain.Task{ID: id, Summary: "Summary " + id, State: state, URL: "https://yt/issue/" + id, MRs: mrs}
}

func build(snap *domain.Snapshot, mutes domain.MuteRules) View {
	return Build(snap, time.Unix(1700000000, 0), nil, testPolicy(), mutes)
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
	v := Build(nil, time.Time{}, errors.New("boom"), testPolicy(), nil)
	if v.Error != "boom" || v.Cards == nil || len(v.Cards) != 0 || v.NeedsYou != 0 {
		t.Fatalf("view = %+v", v)
	}
}

func TestTaskWithoutMR(t *testing.T) {
	v := build(&domain.Snapshot{Tasks: []*domain.Task{task("ABC-1", "Open")}}, nil)
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
		mutate     func(*domain.MR)
		wantGroup  string
		wantBadges []string
	}{
		{"waiting by default", "In Progress", func(*domain.MR) {}, GroupWaiting, nil},
		{"merged", "In Progress", func(m *domain.MR) { m.State = domain.MRMerged; m.HasConflicts = true }, GroupMerged, nil},
		{"failed pipeline", "In Progress", func(m *domain.MR) { m.Pipeline = &domain.Pipeline{Status: "failed"} }, GroupWork, []string{"Сборка упала"}},
		{"canceled pipeline", "In Progress", func(m *domain.MR) { m.Pipeline = &domain.Pipeline{Status: "canceled"} }, GroupWork, []string{"Сборка упала"}},
		{"changes requested", "In Progress", func(m *domain.MR) {
			m.Reviewers = []domain.Reviewer{{User: domain.User{Username: "bob"}, State: domain.ReviewerRequestedChanges}}
		}, GroupWork, []string{"Изменения запрошены"}},
		{"unresolved threads", "In Progress", func(m *domain.MR) { m.DiscussionsResolved = false }, GroupWork, []string{"Нерешённые треды"}},
		{"conflicts", "In Progress", func(m *domain.MR) { m.HasConflicts = true }, GroupWork, []string{"Конфликты"}},
		{"behind threshold", "In Progress", func(m *domain.MR) { m.Behind = 12 }, GroupWork, []string{"Отстаёт на 12 коммитов"}},
		{"behind below threshold", "In Progress", func(m *domain.MR) { m.Behind = 9 }, GroupWaiting, nil},
		{"ready to transition", "code review", func(m *domain.MR) { m.ApprovedBy = []string{"a", "b"} }, GroupReady, nil},
		{"problems beat ready", "Code Review", func(m *domain.MR) { m.ApprovedBy = []string{"a", "b"}; m.HasConflicts = true }, GroupWork, []string{"Конфликты"}},
		{"badge order", "In Progress", func(m *domain.MR) {
			m.HasConflicts = true
			m.DiscussionsResolved = false
			m.Pipeline = &domain.Pipeline{Status: "failed"}
		}, GroupWork, []string{"Сборка упала", "Нерешённые треды", "Конфликты"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mr := cleanMR(1)
			tc.mutate(mr)
			c := card(t, build(&domain.Snapshot{Tasks: []*domain.Task{task("ABC-1", tc.taskState, mr)}}, nil), "mr:1")
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

func TestBadgeTones(t *testing.T) {
	mr := cleanMR(1)
	mr.Pipeline = &domain.Pipeline{Status: "failed"}
	mr.DiscussionsResolved = false
	mr.Behind = 21
	c := card(t, build(&domain.Snapshot{Orphans: []*domain.MR{mr}}, nil), "mr:1")
	want := []Badge{{"Сборка упала", ToneRed}, {"Нерешённые треды", ToneYellow}, {"Отстаёт на 21 коммит", ToneYellow}}
	if len(c.Badges) != len(want) {
		t.Fatalf("badges = %+v", c.Badges)
	}
	for i := range want {
		if c.Badges[i] != want[i] {
			t.Errorf("badge %d = %+v, want %+v", i, c.Badges[i], want[i])
		}
	}
}

func TestReadyAction(t *testing.T) {
	mr := cleanMR(1)
	mr.ApprovedBy = []string{"a", "b"}
	c := card(t, build(&domain.Snapshot{Tasks: []*domain.Task{task("ABC-1", "Code Review", mr)}}, nil), "mr:1")
	if c.Action == nil || c.Action.Label != "Ready for QA" || c.Action.IssueID != "ABC-1" {
		t.Errorf("action = %+v", c.Action)
	}

	// Без команды перевода — та же группа, но без кнопки.
	p := testPolicy()
	p.NextCommand = ""
	v := Build(&domain.Snapshot{Tasks: []*domain.Task{task("ABC-1", "Code Review", mr)}}, time.Now(), nil, p, nil)
	if c := card(t, v, "mr:1"); c.Group != GroupReady || c.Action != nil {
		t.Errorf("no command: %+v", c)
	}

	// Сирота не бывает «готов к переводу»: не к чему применять команду.
	orphan := card(t, build(&domain.Snapshot{Orphans: []*domain.MR{mr}}, nil), "mr:1")
	if orphan.Group != GroupWaiting || orphan.Action != nil || orphan.IssueID != "" {
		t.Errorf("orphan = %+v", orphan)
	}
}

func TestWait(t *testing.T) {
	mr := cleanMR(1)
	mr.CreatedAt = time.Now().Add(-50*time.Hour - 30*time.Minute)
	c := card(t, build(&domain.Snapshot{Orphans: []*domain.MR{mr}}, nil), "mr:1")
	if c.Wait != "2д 2ч" || c.waitHours != 50 {
		t.Errorf("wait = %q (%d)", c.Wait, c.waitHours)
	}

	mr.CreatedAt = time.Now().Add(-5*time.Hour - 30*time.Minute)
	if c := card(t, build(&domain.Snapshot{Orphans: []*domain.MR{mr}}, nil), "mr:1"); c.Wait != "5ч" {
		t.Errorf("wait = %q", c.Wait)
	}
	mr.CreatedAt = time.Now().Add(-30 * time.Minute)
	if c := card(t, build(&domain.Snapshot{Orphans: []*domain.MR{mr}}, nil), "mr:1"); c.Wait != "" {
		t.Errorf("young MR wait = %q", c.Wait)
	}
	// У проблемного MR срок ожидания не показываем.
	mr.CreatedAt = time.Now().Add(-50 * time.Hour)
	mr.HasConflicts = true
	if c := card(t, build(&domain.Snapshot{Orphans: []*domain.MR{mr}}, nil), "mr:1"); c.Wait != "" {
		t.Errorf("work MR wait = %q", c.Wait)
	}
}

func TestReviewCard(t *testing.T) {
	mr := cleanMR(5)
	mr.Author = domain.User{Username: "bob", Name: "Bob"}
	mr.CreatedAt = time.Now().Add(-3*time.Hour - time.Minute)
	mr.Reviewers = []domain.Reviewer{{User: domain.User{Username: "me"}, State: domain.ReviewerUnreviewed}}
	c := card(t, build(&domain.Snapshot{Reviews: []*domain.MR{mr}}, nil), "mr:5")
	if c.Group != GroupReview || !c.Review || c.Mine || c.Wait != "3ч" {
		t.Errorf("card = %+v", c)
	}

	// Апрув и запрос изменений уводят карточку в «Ждём других»; «reviewed»
	// (оставил ревью без решения) считается ещё не решённым.
	for state, want := range map[domain.ReviewerState]string{
		domain.ReviewerApproved: GroupWaiting, domain.ReviewerRequestedChanges: GroupWaiting, domain.ReviewerReviewed: GroupReview,
	} {
		mr.Reviewers[0].State = state
		if c := card(t, build(&domain.Snapshot{Reviews: []*domain.MR{mr}}, nil), "mr:5"); c.Group != want {
			t.Errorf("reviewer state %s: group = %s, want %s", state, c.Group, want)
		}
	}

	// Апрув засчитывается, даже если ревьюером тебя не назначали.
	mr.Reviewers = nil
	mr.ApprovedBy = []string{"me"}
	if c := card(t, build(&domain.Snapshot{Reviews: []*domain.MR{mr}}, nil), "mr:5"); c.Group != GroupWaiting {
		t.Errorf("approved without being a reviewer: group = %s", c.Group)
	}
}

func TestReviewers(t *testing.T) {
	mr := cleanMR(1)
	mr.Reviewers = []domain.Reviewer{
		{User: domain.User{Username: "a", Name: "Anna Smirnova"}, State: domain.ReviewerUnreviewed},
		{User: domain.User{Username: "b"}, State: domain.ReviewerApproved},
		{User: domain.User{Username: "c"}, State: domain.ReviewerRequestedChanges},
		{User: domain.User{Username: "d"}, State: domain.ReviewerUnreviewed},
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
	c := card(t, build(&domain.Snapshot{Orphans: []*domain.MR{mr}}, nil), "mr:1")
	if c.Author == nil || *c.Author != (Person{Username: "me", Name: "Ivan Petrov", Initials: "IP"}) {
		t.Errorf("author = %+v (no decision for the author)", c.Author)
	}
}

func TestPipelineTone(t *testing.T) {
	cases := map[domain.PipelineStatus]string{
		"success": ToneGreen, "failed": ToneRed, "canceled": ToneRed, "skipped": "", "": "",
		"running": ToneYellow, "pending": ToneYellow, "manual": ToneYellow,
	}
	for status, want := range cases {
		if got := pipelineTone(&domain.Pipeline{Status: status}); got != want {
			t.Errorf("pipelineTone(%q) = %q, want %q", status, got, want)
		}
	}
	if pipelineTone(nil) != "" {
		t.Error("no pipeline")
	}
	mr := cleanMR(1)
	mr.Pipeline = &domain.Pipeline{Status: "running", WebURL: "https://gl/p"}
	if c := card(t, build(&domain.Snapshot{Orphans: []*domain.MR{mr}}, nil), "mr:1"); c.Pipeline != ToneYellow || c.PipelineURL != "https://gl/p" {
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
	review := cleanMR(5) // ждёт ревью — тоже считался бы
	snap := &domain.Snapshot{Orphans: []*domain.MR{a, b, c, d}, Reviews: []*domain.MR{review}}
	mutes := domain.MuteRules{
		domain.MRKey(1):              {},                    // навсегда
		domain.TitleKey("Bump deps"): later,                 // группа по названию
		domain.MRKey(4):              now.Add(-time.Minute), // истекло
		domain.MRKey(5):              {},
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
	if x := card(t, v, "mr:5"); x.Group != GroupMuted || !x.Review {
		t.Errorf("muted review: %+v", x)
	}
	if v.NeedsYou != 0 {
		t.Errorf("needsYou = %d, muted cards must not count", v.NeedsYou)
	}
}

func TestNeedsYou(t *testing.T) {
	work := cleanMR(1)
	work.HasConflicts = true
	ready := cleanMR(2)
	ready.ApprovedBy = []string{"a", "b"}
	review := cleanMR(3)
	waiting := cleanMR(4)
	merged := cleanMR(5)
	merged.State = domain.MRMerged
	snap := &domain.Snapshot{
		Tasks:   []*domain.Task{task("ABC-1", "Code Review", work, ready), task("ABC-2", "Open")},
		Orphans: []*domain.MR{waiting, merged},
		Reviews: []*domain.MR{review},
	}
	if v := build(snap, nil); v.NeedsYou != 3 {
		t.Errorf("needsYou = %d, want 3 (work, ready, review)", v.NeedsYou)
	}
}

func TestSortGroupsAndWait(t *testing.T) {
	young, old := cleanMR(1), cleanMR(2)
	young.CreatedAt = time.Now().Add(-5 * time.Hour)
	old.CreatedAt = time.Now().Add(-30 * time.Hour)
	work := cleanMR(3)
	work.HasConflicts = true
	merged := cleanMR(4)
	merged.State = domain.MRMerged
	// Карточка другой группы между ждущими раньше блокировала сортировку.
	v := build(&domain.Snapshot{
		Tasks:   []*domain.Task{task("ABC-1", "Open")},
		Orphans: []*domain.MR{merged, young, work, old},
	}, nil)
	var order []string
	for _, c := range v.Cards {
		order = append(order, c.Key)
	}
	if strings.Join(order, ",") != "mr:3,mr:2,mr:1,task:ABC-1,mr:4" {
		t.Errorf("order = %v, want groups in order and older waiting first", order)
	}
}

// Фронт полагается на то, что списки приходят массивами, а не null.
func TestJSONArraysNeverNull(t *testing.T) {
	data, err := json.Marshal(build(&domain.Snapshot{
		Tasks:   []*domain.Task{task("ABC-1", "Open")},
		Orphans: []*domain.MR{cleanMR(1)},
	}, nil))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "null") {
		t.Errorf("view JSON contains null: %s", data)
	}
	if strings.Contains(string(data), "waitHours") || strings.Contains(string(data), "needsMe") {
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

// Релизная ветка на карточке — со статусом сборки релиза, если она уже есть.
func TestReleaseChips(t *testing.T) {
	merged := cleanMR(5)
	merged.State = domain.MRMerged
	merged.Releases = []domain.Release{
		{Branch: "release/1.0", Build: &domain.Pipeline{ID: 90, Status: "failed", WebURL: "https://gl/p/90"}},
		{Branch: "release/1.1"},
	}
	c := card(t, build(&domain.Snapshot{Orphans: []*domain.MR{merged}}, nil), "mr:5")
	want := []Release{{Branch: "release/1.0", Pipeline: ToneRed, URL: "https://gl/p/90"}, {Branch: "release/1.1"}}
	if len(c.Release) != 2 || c.Release[0] != want[0] || c.Release[1] != want[1] {
		t.Errorf("release = %+v", c.Release)
	}
}
