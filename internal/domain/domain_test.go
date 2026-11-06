package domain

import (
	"testing"
	"time"
)

func TestDecisionOf(t *testing.T) {
	mr := &MR{
		ApprovedBy: []string{"a", "x"},
		Reviewers: []Reviewer{
			{User: User{Username: "a"}, State: ReviewerRequestedChanges}, // апрув важнее устаревшего состояния
			{User: User{Username: "b"}, State: ReviewerApproved},
			{User: User{Username: "c"}, State: ReviewerRequestedChanges},
			{User: User{Username: "d"}, State: ReviewerReviewed},
			{User: User{Username: "e"}, State: ReviewerUnreviewed},
		},
	}
	cases := map[string]ReviewDecision{
		"a": DecisionApproved, "b": DecisionApproved, "c": DecisionChanges, "d": DecisionPending,
		"e": DecisionPending, "x": DecisionApproved, "nobody": DecisionPending,
	}
	for user, want := range cases {
		if got := mr.DecisionOf(user); got != want {
			t.Errorf("DecisionOf(%q) = %v, want %v", user, got, want)
		}
	}
	if !mr.ChangesRequested() {
		t.Error("ChangesRequested: c requested changes")
	}
	if (&MR{ApprovedBy: []string{"c"}, Reviewers: mr.Reviewers[2:3]}).ChangesRequested() {
		t.Error("ChangesRequested: approval overrides")
	}
	if mr.Approvals() != 2 || !mr.IsApprovedBy("x") || mr.IsApprovedBy("b") {
		t.Error("approvals")
	}
}

func TestPipelineOutcome(t *testing.T) {
	cases := map[PipelineStatus]Outcome{
		"success": OutcomeSuccess, "failed": OutcomeFailed, "canceled": OutcomeFailed,
		"skipped": OutcomeNone, "": OutcomeNone,
		"running": OutcomeRunning, "pending": OutcomeRunning, "manual": OutcomeRunning, "brand_new": OutcomeRunning,
	}
	for s, want := range cases {
		if got := s.Outcome(); got != want {
			t.Errorf("%q.Outcome() = %v, want %v", s, got, want)
		}
	}
	var none *Pipeline
	if none.Outcome() != OutcomeNone || none.Failed() {
		t.Error("nil pipeline")
	}
	if !(&Pipeline{Status: "canceled"}).Failed() {
		t.Error("canceled is failed")
	}
}

func TestIssueKey(t *testing.T) {
	cases := []struct{ branch, title, want string }{
		{"feature/app-1234-login", "whatever", "APP-1234"},
		{"ABC-1", "XYZ-2 title", "ABC-1"},
		{"hotfix", "ABC-77: crash", "ABC-77"},
		{"hotfix", "no key here", ""},
		{"v2-release", "", ""}, // после дефиса не число
		{"a-1", "", ""},        // проект из одной буквы не считается ключом
		{"x2-15", "", "X2-15"},
	}
	for _, tc := range cases {
		if got := IssueKey(tc.branch, tc.title); got != tc.want {
			t.Errorf("IssueKey(%q, %q) = %q, want %q", tc.branch, tc.title, got, tc.want)
		}
	}
}

func TestSnapshot(t *testing.T) {
	s := &Snapshot{
		Tasks:   []*Task{{MRs: []*MR{{ID: 1}, {ID: 2}}}, {}},
		Orphans: []*MR{{ID: 3}},
		Reviews: []*MR{{ID: 4}, {ID: 5}, {ID: 6}},
	}
	if got := s.Counts(); got != (Counts{Own: 3, Reviews: 3, Tasks: 2}) {
		t.Errorf("counts = %+v", got)
	}
	var ids []int
	for _, mr := range s.AllMRs() {
		ids = append(ids, mr.ID)
	}
	if len(ids) != 6 || ids[0] != 1 || ids[2] != 3 || ids[5] != 6 {
		t.Errorf("AllMRs = %v", ids)
	}
}

func policy() Policy {
	return Policy{GitLabUser: "me", RequiredApprovals: 2, ReviewState: "Code Review", NextCommand: "State QA", BehindThreshold: 10}
}

func cleanMR() *MR { return &MR{State: MROpened, DiscussionsResolved: true} }

func TestProblems(t *testing.T) {
	mr := cleanMR()
	if got := policy().Problems(mr); len(got) != 0 {
		t.Errorf("clean MR problems = %v", got)
	}
	mr.Pipeline = &Pipeline{Status: "failed"}
	mr.Reviewers = []Reviewer{{User: User{Username: "bob"}, State: ReviewerRequestedChanges}}
	mr.DiscussionsResolved = false
	mr.HasConflicts = true
	mr.Behind = 12
	want := []Problem{
		{Kind: ProblemPipelineFailed}, {Kind: ProblemChangesRequested}, {Kind: ProblemUnresolvedThreads},
		{Kind: ProblemConflicts}, {Kind: ProblemBehind, Behind: 12},
	}
	got := policy().Problems(mr)
	if len(got) != len(want) {
		t.Fatalf("problems = %v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("problem %d = %+v, want %+v", i, got[i], want[i])
		}
	}

	mr = cleanMR()
	mr.Behind = 9
	if len(policy().Problems(mr)) != 0 {
		t.Error("behind below threshold")
	}
	p := policy()
	p.BehindThreshold = 0
	mr.Behind = 500
	if len(p.Problems(mr)) != 0 {
		t.Error("zero threshold disables behind")
	}
}

func TestReadyToTransition(t *testing.T) {
	task := &Task{State: "code review"}
	mr := cleanMR()
	mr.ApprovedBy = []string{"a", "b"}
	p := policy()
	if !p.ReadyToTransition(task, mr) {
		t.Error("ready")
	}
	if p.ReadyToTransition(nil, mr) {
		t.Error("orphan is never ready")
	}
	if p.ReadyToTransition(&Task{State: "Open"}, mr) {
		t.Error("other task state")
	}
	mr.ApprovedBy = mr.ApprovedBy[:1]
	if p.ReadyToTransition(task, mr) {
		t.Error("not enough approvals")
	}
	mr.ApprovedBy = []string{"a", "b"}
	mr.State = MRMerged
	if p.ReadyToTransition(task, mr) {
		t.Error("merged MR")
	}
	if !p.CanTransition() || (Policy{}).CanTransition() {
		t.Error("CanTransition")
	}
}

func TestTransitionLabel(t *testing.T) {
	if got := TransitionLabel("State Ready for QA"); got != "Ready for QA" {
		t.Errorf("got %q", got)
	}
	if got := TransitionLabel("tag ready"); got != "tag ready" {
		t.Errorf("got %q", got)
	}
}

func TestStages(t *testing.T) {
	p := policy()
	task := &Task{State: "Code Review"}

	mr := cleanMR()
	if s := p.OwnStage(task, mr); s != StageWaiting || s.NeedsMe() {
		t.Errorf("clean = %v", s)
	}
	mr.ApprovedBy = []string{"a", "b"}
	if s := p.OwnStage(task, mr); s != StageReady || !s.NeedsMe() {
		t.Errorf("approved = %v", s)
	}
	mr.HasConflicts = true
	if s := p.OwnStage(task, mr); s != StageNeedsWork || !s.NeedsMe() {
		t.Errorf("problems beat ready: %v", s)
	}
	mr.State = MRMerged
	if s := p.OwnStage(task, mr); s != StageMerged || s.NeedsMe() {
		t.Errorf("merged = %v", s)
	}

	review := cleanMR()
	if s := p.ReviewStage(review); s != StageNeedsReview || !s.NeedsMe() || !p.AwaitsMyReview(review) {
		t.Errorf("review = %v", s)
	}
	for _, mutate := range []func(*MR){
		func(m *MR) { m.ApprovedBy = []string{"me"} },
		func(m *MR) { m.Reviewers = []Reviewer{{User: User{Username: "me"}, State: ReviewerRequestedChanges}} },
	} {
		r := cleanMR()
		mutate(r)
		if s := p.ReviewStage(r); s != StageWaiting {
			t.Errorf("reviewed by me = %v", s)
		}
	}
}

func TestMuteRules(t *testing.T) {
	now := time.Now()
	later := now.Add(time.Hour)
	rules := MuteRules{
		MRKey(1):         {},
		TitleKey("Bump"): later,
		MRKey(3):         now.Add(-time.Minute),
		TitleKey("Old"):  now.Add(-time.Minute),
	}
	cases := []struct {
		mr        *MR
		wantMuted bool
		wantUntil time.Time
	}{
		{&MR{ID: 1, Title: "x"}, true, time.Time{}},
		{&MR{ID: 2, Title: "Bump"}, true, later},
		{&MR{ID: 3, Title: "x"}, false, time.Time{}},
		{&MR{ID: 3, Title: "Bump"}, true, later}, // личное истекло, групповое действует
		{&MR{ID: 4, Title: "Old"}, false, time.Time{}},
		{&MR{ID: 5, Title: "y"}, false, time.Time{}},
	}
	for _, tc := range cases {
		until, muted := rules.Match(tc.mr, now)
		if muted != tc.wantMuted || !until.Equal(tc.wantUntil) {
			t.Errorf("Match(%d %q) = %v %v, want %v %v", tc.mr.ID, tc.mr.Title, until, muted, tc.wantUntil, tc.wantMuted)
		}
	}
	if _, muted := MuteRules(nil).Match(&MR{ID: 1}, now); muted {
		t.Error("nil rules")
	}
	if MRKey(42) != "mr:42" || TitleKey("T") != "title:T" || !IsMRKey("mr:1") || IsMRKey("task:1") {
		t.Error("key format must stay compatible with state.json")
	}
	if !MuteActive(time.Time{}, now) || MuteActive(now, now) || !MuteActive(later, now) {
		t.Error("MuteActive")
	}
}

func TestWorkHours(t *testing.T) {
	w := WorkHours{Start: 10 * 60, End: 19 * 60}
	for _, d := range []time.Weekday{time.Monday, time.Tuesday, time.Wednesday, time.Thursday, time.Friday} {
		w.Days[d] = true
	}
	at := func(day, hour, minute int) time.Time { return time.Date(2026, 11, day, hour, minute, 0, 0, time.Local) }
	// 6 ноября 2026 — пятница, 7–8 — выходные, 9 — понедельник.
	cases := []struct {
		t      time.Time
		active bool
		next   time.Time
	}{
		{at(6, 9, 59), false, at(6, 10, 0)},
		{at(6, 10, 0), true, at(9, 10, 0)},
		{at(6, 18, 59), true, at(9, 10, 0)},
		{at(6, 19, 0), false, at(9, 10, 0)},
		{at(7, 12, 0), false, at(9, 10, 0)},
		{at(9, 8, 0), false, at(9, 10, 0)},
	}
	for _, tc := range cases {
		if got := w.Active(tc.t); got != tc.active {
			t.Errorf("Active(%s) = %v", tc.t.Format("Mon 15:04"), got)
		}
		if got := w.NextStart(tc.t); !got.Equal(tc.next) {
			t.Errorf("NextStart(%s) = %s, want %s", tc.t.Format("Mon 15:04"), got.Format("Mon 02 15:04"), tc.next.Format("Mon 02 15:04"))
		}
	}
	if !(WorkHours{Start: 600, End: 1140}).NextStart(at(6, 12, 0)).IsZero() {
		t.Error("no working days — no next start")
	}
}
