package engine

import (
	"context"
	"errors"
	"io"
	"log"
	"os"
	"path/filepath"
	"testing"
	"time"

	"taskwatch/internal/config"
	"taskwatch/internal/gitlab"
	"taskwatch/internal/state"
	"taskwatch/internal/testutil/fakeapi"
	"taskwatch/internal/watch"
	"taskwatch/internal/youtrack"
)

func TestMain(m *testing.M) {
	log.SetOutput(io.Discard)
	os.Exit(m.Run())
}

type harness struct {
	srv     *fakeapi.Server
	cfg     *config.Config
	eng     *Engine
	store   *state.Store
	updates int
}

// newHarness собирает движок на поддельных GitLab/YouTrack. PATH пустой, поэтому
// notify.Send не находит ни terminal-notifier, ни osascript и падает: настоящих
// уведомлений тесты не шлют, а «попытку отправить» видно по тому, что событие
// осталось непомеченным.
func newHarness(t *testing.T, youtrackOn bool, mutate func(*config.Config)) *harness {
	t.Helper()
	t.Setenv("PATH", "")
	srv := fakeapi.New(t)
	cfg := config.Default(filepath.Join(t.TempDir(), "config.yaml"))
	cfg.GitLab = config.GitLab{URL: srv.URL, Token: "gl", Username: "me"}
	if youtrackOn {
		cfg.YouTrack = config.YouTrack{URL: srv.URL, Token: "yt", Login: "me", Query: "for: me"}
	}
	if mutate != nil {
		mutate(cfg)
	}
	store, err := state.Open(cfg.StatePath)
	if err != nil {
		t.Fatal(err)
	}
	return newHarnessWith(t, srv, cfg, store)
}

func newHarnessWith(t *testing.T, srv *fakeapi.Server, cfg *config.Config, store *state.Store) *harness {
	h := &harness{srv: srv, cfg: cfg, store: store}
	gl := gitlab.New(cfg.GitLab.URL, cfg.GitLab.Token)
	yt := youtrack.New(cfg.YouTrack.URL, cfg.YouTrack.Token)
	h.eng = New(cfg, watch.NewCollector(cfg, gl, yt), store, yt)
	h.eng.OnUpdate = func() { h.updates++ }
	return h
}

func (h *harness) tick(t *testing.T) {
	t.Helper()
	if err := h.eng.Tick(context.Background()); err != nil {
		t.Fatalf("tick: %v", err)
	}
}

func (h *harness) refreshPending() bool {
	select {
	case <-h.eng.refresh:
		return true
	default:
		return false
	}
}

func mr(id int, title string) fakeapi.MR {
	return fakeapi.MR{MergeRequest: gitlab.MergeRequest{
		ID: 100 + id, IID: id, ProjectID: 1, State: "opened", Title: title, SourceBranch: "b" + title,
		BlockingDiscussionsResolved: true,
	}}
}

func note(id int, author string) gitlab.Note {
	return gitlab.Note{ID: id, Body: "text", Author: gitlab.User{Username: author}}
}

func TestNotConfigured(t *testing.T) {
	h := newHarness(t, false, func(c *config.Config) { c.GitLab = config.GitLab{} })
	err := h.eng.Tick(context.Background())
	if !errors.Is(err, errNotConfigured) {
		t.Fatalf("err = %v", err)
	}
	snap, _, lastErr := h.eng.Snapshot()
	if snap != nil || !errors.Is(lastErr, errNotConfigured) || h.updates != 1 {
		t.Errorf("snap = %v, lastErr = %v, updates = %d", snap, lastErr, h.updates)
	}
	if len(h.srv.Requests()) != 0 {
		t.Errorf("requests without config: %v", h.srv.Requests())
	}
}

func TestFirstRunRemembersWithoutNotifying(t *testing.T) {
	h := newHarness(t, false, nil)
	m := mr(1, "a")
	m.Notes = []gitlab.Note{note(1, "bob")}
	h.srv.SetMine(m)

	if !h.store.Fresh() {
		t.Fatal("store must start fresh")
	}
	h.tick(t)
	// notify.Send упал бы (PATH пуст) и событие осталось бы непомеченным —
	// значит, на первом запуске отправки не было.
	if !h.store.Seen("gl-note:1") {
		t.Error("first run must mark events without sending")
	}
	if h.store.Fresh() {
		t.Error("store still fresh after first tick")
	}
	snap, updated, err := h.eng.Snapshot()
	if err != nil || snap == nil || len(snap.Orphans) != 1 || updated.IsZero() || h.updates != 1 {
		t.Errorf("snap = %+v, updated = %v, err = %v, updates = %d", snap, updated, err, h.updates)
	}
	if got := h.store.Counts(); got != (state.Counts{Own: 1}) {
		t.Errorf("counts = %+v", got)
	}
}

// Новое событие пытается уйти в уведомление и, не отправившись, не помечается —
// повторится на следующем тике.
func TestFailedNotificationIsRetried(t *testing.T) {
	h := newHarness(t, false, nil)
	m := mr(1, "a")
	h.srv.SetMine(m)
	h.tick(t) // seed
	m.Notes = []gitlab.Note{note(2, "bob")}
	h.srv.SetMine(m)
	h.tick(t)
	if h.store.Seen("gl-note:2") {
		t.Error("failed notification must not be marked as seen")
	}
}

func TestTaskEventsAreNotified(t *testing.T) {
	h := newHarness(t, true, nil)
	h.srv.SetIssues(fakeapi.Issue{Issue: youtrack.Issue{ID: "ABC-1", CustomFields: []youtrack.CustomField{fakeapi.State("Open")}}})
	h.tick(t) // seed
	h.srv.SetIssues(fakeapi.Issue{Issue: youtrack.Issue{ID: "ABC-1", CustomFields: []youtrack.CustomField{fakeapi.State("Done")}}})
	h.tick(t)
	if !h.store.Seen("yt-state:ABC-1:Open") || h.store.Seen("yt-state:ABC-1:Done") {
		t.Error("task state change must be sent (and stay unmarked when sending fails)")
	}
}

func TestMutedMREventsAreMarkedSilently(t *testing.T) {
	h := newHarness(t, false, nil)
	muted, titled, loud := mr(1, "a"), mr(2, "Bump deps"), mr(3, "c")
	h.srv.SetMine(muted, titled, loud)
	h.tick(t) // seed

	if err := h.eng.Mute("mr:101", "", time.Time{}); err != nil {
		t.Fatal(err)
	}
	if err := h.eng.Mute("mr:999", "Bump deps", time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	muted.Notes = []gitlab.Note{note(1, "bob")}
	titled.Notes = []gitlab.Note{note(2, "bob")}
	loud.Notes = []gitlab.Note{note(3, "bob")}
	h.srv.SetMine(muted, titled, loud)
	h.tick(t)

	if !h.store.Seen("gl-note:1") || !h.store.Seen("gl-note:2") {
		t.Error("events of muted MRs must be marked without sending")
	}
	if h.store.Seen("gl-note:3") {
		t.Error("event of a loud MR was not sent (would be marked only after sending)")
	}
}

func TestMuteUnmute(t *testing.T) {
	h := newHarness(t, false, nil)
	until := time.Now().Add(time.Hour)
	if err := h.eng.Mute("mr:1", "Title", until); err != nil {
		t.Fatal(err)
	}
	m := h.eng.Mutes()
	if !m["mr:1"].Equal(until) || !m["title:Title"].Equal(until) || h.updates != 1 {
		t.Errorf("mutes = %v, updates = %d", m, h.updates)
	}
	reopened, _ := state.Open(h.cfg.StatePath)
	if len(reopened.Mutes()) != 2 {
		t.Error("mute not persisted")
	}

	if err := h.eng.Mute("mr:2", "", time.Time{}); err != nil {
		t.Fatal(err)
	}
	if len(h.eng.Mutes()) != 3 {
		t.Errorf("empty title must not add a title rule: %v", h.eng.Mutes())
	}

	if err := h.eng.Unmute("mr:1", "Title"); err != nil {
		t.Fatal(err)
	}
	if m := h.eng.Mutes(); len(m) != 1 || h.updates != 3 {
		t.Errorf("after unmute: %v, updates = %d", m, h.updates)
	}
}

func TestDegradedTickIsRechecked(t *testing.T) {
	h := newHarness(t, false, nil)
	h.srv.SetMine(mr(1, "a"), mr(2, "b"))
	h.srv.SetReviews(mr(3, "c"))
	h.tick(t)
	h.refreshPending() // очистить

	// GitLab внезапно вернул пустоту по своим MR — держим прошлый вид и перепроверяем.
	h.srv.SetMine()
	h.tick(t)
	snap, _, _ := h.eng.Snapshot()
	if len(snap.Orphans) != 2 {
		t.Errorf("suspicious tick replaced the snapshot: %+v", snap)
	}
	if !h.refreshPending() {
		t.Error("suspicious tick must request an immediate recheck")
	}
	if h.updates != 2 {
		t.Errorf("updates = %d", h.updates)
	}

	// Повторная пустота — правда.
	h.tick(t)
	snap, _, _ = h.eng.Snapshot()
	if len(snap.Orphans) != 0 || len(snap.Reviews) != 1 {
		t.Errorf("confirmed empty tick not accepted: %+v", snap)
	}
	if got := h.store.Counts(); got != (state.Counts{Reviews: 1}) {
		t.Errorf("counts = %+v", got)
	}
}

func TestDegradedTickRecovers(t *testing.T) {
	h := newHarness(t, false, nil)
	h.srv.SetReviews(mr(3, "c"))
	h.tick(t)
	h.srv.SetReviews()
	h.tick(t) // подозрительный
	h.srv.SetReviews(mr(3, "c"), mr(4, "d"))
	h.tick(t) // перепроверка вернула данные
	snap, _, _ := h.eng.Snapshot()
	if len(snap.Reviews) != 2 {
		t.Errorf("snap = %+v", snap)
	}
	// Флаг перепроверки сброшен: новая пустота снова подозрительна.
	h.srv.SetReviews()
	h.tick(t)
	if snap, _, _ := h.eng.Snapshot(); len(snap.Reviews) != 2 {
		t.Errorf("recheck flag not reset: %+v", snap)
	}
}

func TestDegradedBaselineSurvivesRestart(t *testing.T) {
	h := newHarness(t, false, nil)
	h.srv.SetMine(mr(1, "a"))
	h.tick(t)

	store, err := state.Open(h.cfg.StatePath)
	if err != nil {
		t.Fatal(err)
	}
	restarted := newHarnessWith(t, h.srv, h.cfg, store)
	h.srv.SetMine()
	restarted.tick(t)
	if snap, _, _ := restarted.eng.Snapshot(); snap != nil {
		t.Errorf("first tick after restart trusted an empty result: %+v", snap)
	}
}

func TestFetchErrorKeepsPreviousSnapshot(t *testing.T) {
	h := newHarness(t, false, nil)
	h.srv.SetMine(mr(1, "a"))
	h.tick(t)
	h.srv.FailWith(500)
	if err := h.eng.Tick(context.Background()); err == nil {
		t.Fatal("want error")
	}
	snap, _, err := h.eng.Snapshot()
	if err == nil || snap == nil || len(snap.Orphans) != 1 {
		t.Errorf("snap = %+v, err = %v", snap, err)
	}
}

func taskWithApprovedMR(srv *fakeapi.Server, approvals ...string) {
	srv.SetIssues(fakeapi.Issue{Issue: youtrack.Issue{ID: "ABC-1", CustomFields: []youtrack.CustomField{fakeapi.State("Code Review")}}})
	m := mr(1, "x")
	m.SourceBranch = "feature/abc-1"
	m.ApprovedBy = approvals
	srv.SetMine(m)
}

func TestAutoTransition(t *testing.T) {
	h := newHarness(t, true, func(c *config.Config) {
		c.Rules.ReviewState = "Code Review"
		c.Rules.NextCommand = "State Ready for QA"
		c.Rules.AutoTransition = true
	})
	taskWithApprovedMR(h.srv)
	h.tick(t) // seed: апрувов ещё нет
	taskWithApprovedMR(h.srv, "bob", "eve")
	h.tick(t)

	cmds := h.srv.Commands()
	if len(cmds) != 1 || cmds[0] != (fakeapi.Command{Query: "State Ready for QA", IssueID: "ABC-1"}) {
		t.Errorf("commands = %+v", cmds)
	}
}

func TestNoAutoTransitionOnSeedOrWhenDisabled(t *testing.T) {
	for _, auto := range []bool{true, false} {
		h := newHarness(t, true, func(c *config.Config) {
			c.Rules.ReviewState = "Code Review"
			c.Rules.NextCommand = "State Ready for QA"
			c.Rules.AutoTransition = auto
		})
		taskWithApprovedMR(h.srv, "bob", "eve")
		h.tick(t) // seed с уже готовым MR
		h.tick(t)
		if !auto {
			taskWithApprovedMR(h.srv, "bob", "eve", "zed")
			h.tick(t)
		}
		if cmds := h.srv.Commands(); len(cmds) != 0 {
			t.Errorf("auto=%v: commands = %+v", auto, cmds)
		}
	}
}

func TestTransition(t *testing.T) {
	h := newHarness(t, false, nil)
	if err := h.eng.Transition(context.Background(), "ABC-1"); err == nil {
		t.Error("transition without YouTrack must fail")
	}

	h = newHarness(t, true, func(c *config.Config) { c.Rules.NextCommand = "State Done" })
	if err := h.eng.Transition(context.Background(), "ABC-1"); err != nil {
		t.Fatal(err)
	}
	if cmds := h.srv.Commands(); len(cmds) != 1 || cmds[0].Query != "State Done" || cmds[0].IssueID != "ABC-1" {
		t.Errorf("commands = %+v", cmds)
	}
	if !h.refreshPending() {
		t.Error("transition must request a refresh")
	}
}

func TestReload(t *testing.T) {
	h := newHarness(t, false, nil)
	cfg := config.Default(h.cfg.StatePath)
	cfg.PollInterval = time.Hour
	h.eng.Reload(cfg, watch.NewCollector(cfg, nil, nil), nil)
	if h.eng.Config() != cfg || !h.refreshPending() {
		t.Error("reload must swap config and request a refresh")
	}
}

func TestRunStopsOnCancel(t *testing.T) {
	h := newHarness(t, false, func(c *config.Config) { c.PollInterval = time.Hour })
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { h.eng.Run(ctx); close(done) }()

	deadline := time.After(5 * time.Second)
	for len(h.srv.Requests()) == 0 {
		select {
		case <-deadline:
			t.Fatal("Run did not tick")
		case <-time.After(10 * time.Millisecond):
		}
	}
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not stop")
	}
}

func TestCountsOf(t *testing.T) {
	snap := &watch.Snapshot{
		Tasks:   []*watch.Task{{MRs: []*watch.MR{{}, {}}}, {}},
		Orphans: []*watch.MR{{}},
		Reviews: []*watch.MR{{}, {}, {}},
	}
	if got := countsOf(snap); got != (state.Counts{Own: 3, Reviews: 3, Tasks: 2}) {
		t.Errorf("counts = %+v", got)
	}
}
