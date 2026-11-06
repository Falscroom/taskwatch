package engine

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"taskwatch/internal/alerts"
	"taskwatch/internal/collect"
	"taskwatch/internal/config"
	"taskwatch/internal/domain"
	"taskwatch/internal/integrations/gitlab"
	"taskwatch/internal/integrations/httpx"
	"taskwatch/internal/integrations/youtrack"
	"taskwatch/internal/state"
	"taskwatch/internal/testutil/fakeapi"
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

	sent       []sent // отправленные уведомления
	failNotify bool   // уведомления не отправляются
}

type sent struct{ title, message, url, key string }

func (h *harness) sentKeys() []string {
	var keys []string
	for _, s := range h.sent {
		keys = append(keys, s.key)
	}
	return keys
}

// newHarness собирает движок на поддельных GitLab/YouTrack с записывающим notifier.
func newHarness(t *testing.T, youtrackOn bool, mutate func(*config.Config)) *harness {
	t.Helper()
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
	h.eng = New(cfg, store, func(ev alerts.Event) error {
		if h.failNotify {
			return errors.New("notification center is unavailable")
		}
		h.sent = append(h.sent, sent{ev.Title, ev.Body, ev.URL, ev.Key})
		return nil
	}, testDeps)
	h.eng.Subscribe(func() { h.updates++ })
	return h
}

// testDeps — как app.Deps (его тут не импортировать: app зависит от engine),
// но без пауз между повторами запросов.
func testDeps(cfg *config.Config) Deps {
	fast := httpx.WithRetryDelays()
	var tasks collect.TaskSource
	var mrs collect.MRSource
	var transition alerts.Transitioner
	if cfg.YouTrackOK() {
		yt := youtrack.New(cfg.YouTrack.URL, cfg.YouTrack.Token, fast)
		tasks, transition = youtrack.NewSource(yt, cfg.YouTrack.Query), yt.Command
	}
	if cfg.GitLabOK() {
		mrs = gitlab.NewSource(gitlab.New(cfg.GitLab.URL, cfg.GitLab.Token, fast), cfg.GitLab.Username, cfg.Rules.ReleaseBranchPrefix)
	}
	return Deps{
		Collector:  collect.New(tasks, mrs, collect.Options{Lookback: cfg.Rules.MRLookback, ExcludeDraft: cfg.Rules.ExcludeDraft}),
		Transition: transition,
	}
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
	if !h.store.Seen("gl-note:1") || len(h.sent) != 0 {
		t.Errorf("first run must mark events without sending; sent = %v", h.sent)
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
	h.failNotify = true
	h.tick(t)
	if h.store.Seen("gl-note:2") {
		t.Error("failed notification must not be marked as seen")
	}
	h.failNotify = false
	h.tick(t)
	if !h.store.Seen("gl-note:2") || len(h.sent) != 1 {
		t.Errorf("retry: sent = %v", h.sent)
	}
	if s := h.sent[0]; s.title != "Комментарий в MR" || s.message != "bob: text" || s.url != "" || s.key != "gl-note:2" {
		t.Errorf("notification = %+v", s)
	}
	h.tick(t)
	if len(h.sent) != 1 {
		t.Errorf("event sent twice: %v", h.sent)
	}
}

func TestTaskEventsAreNotified(t *testing.T) {
	h := newHarness(t, true, nil)
	h.srv.SetIssues(fakeapi.Issue{Issue: youtrack.Issue{ID: "ABC-1", CustomFields: []youtrack.CustomField{fakeapi.State("Open")}}})
	h.tick(t) // seed
	h.srv.SetIssues(fakeapi.Issue{Issue: youtrack.Issue{ID: "ABC-1", CustomFields: []youtrack.CustomField{fakeapi.State("Done")}}})
	h.tick(t)
	if keys := h.sentKeys(); len(keys) != 1 || keys[0] != "yt-state:ABC-1:Done" {
		t.Errorf("sent = %v", keys)
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
	if keys := h.sentKeys(); len(keys) != 1 || keys[0] != "gl-note:3" {
		t.Errorf("sent = %v, want only the loud MR", keys)
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
	if len(h.sent) != 1 || h.sent[0].key != "approved:101" || h.sent[0].title != "Переведена в Ready for QA" {
		t.Errorf("sent = %+v", h.sent)
	}
}

func TestAutoTransitionFailureIsReported(t *testing.T) {
	h := newHarness(t, true, func(c *config.Config) {
		c.Rules.ReviewState = "Code Review"
		c.Rules.NextCommand = "State Ready for QA"
		c.Rules.AutoTransition = true
	})
	taskWithApprovedMR(h.srv)
	h.tick(t)
	taskWithApprovedMR(h.srv, "bob", "eve")
	h.srv.FailCommands(http.StatusForbidden)
	h.tick(t)
	if len(h.sent) != 1 || !strings.HasPrefix(h.sent[0].message, "Не удалось перевести автоматически:") {
		t.Errorf("sent = %+v", h.sent)
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
	h.eng.Reload(cfg)
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

func TestPollInterval(t *testing.T) {
	cfg := config.Default("/x/config.yaml")
	cfg.PollInterval = 5 * time.Minute
	if got := pollInterval(cfg); got != 5*time.Minute {
		t.Errorf("pollInterval = %v", got)
	}
	for _, bad := range []time.Duration{0, -time.Second} {
		cfg.PollInterval = bad
		if got := pollInterval(cfg); got != config.DefaultPollInterval {
			t.Errorf("pollInterval(%v) = %v, want default", bad, got)
		}
	}
}

func TestSubscribe(t *testing.T) {
	h := newHarness(t, false, nil)
	var calls int
	unsubscribe := h.eng.Subscribe(func() { calls++ })
	h.tick(t)
	if err := h.eng.Mute("mr:1", "", time.Time{}); err != nil {
		t.Fatal(err)
	}
	if calls != 2 || h.updates != 2 {
		t.Errorf("calls = %d, updates = %d: tick and mute must notify every subscriber", calls, h.updates)
	}
	unsubscribe()
	h.tick(t)
	if calls != 2 || h.updates != 3 {
		t.Errorf("after unsubscribe: calls = %d, updates = %d", calls, h.updates)
	}
}

func TestTransitionUsesReloadedConfig(t *testing.T) {
	h := newHarness(t, true, func(c *config.Config) { c.Rules.NextCommand = "State A" })
	cfg := *h.eng.Config()
	cfg.Rules.NextCommand = "State B"
	h.eng.Reload(&cfg)
	if err := h.eng.Transition(context.Background(), "ABC-1"); err != nil {
		t.Fatal(err)
	}
	if cmds := h.srv.Commands(); len(cmds) != 1 || cmds[0].Query != "State B" {
		t.Errorf("commands = %+v", cmds)
	}
}

func counts(own, reviews, tasks int) *domain.Snapshot {
	s := &domain.Snapshot{}
	for range own {
		s.Orphans = append(s.Orphans, &domain.MR{})
	}
	for range reviews {
		s.Reviews = append(s.Reviews, &domain.MR{})
	}
	for range tasks {
		s.Tasks = append(s.Tasks, &domain.Task{})
	}
	return s
}

func TestGlitchGuard(t *testing.T) {
	var g glitchGuard
	base := domain.Counts{Own: 2, Reviews: 1, Tasks: 3}

	if !g.accept(base, counts(2, 1, 3)) || !g.accept(base, counts(1, 1, 1)) {
		t.Error("non-empty sections are trusted")
	}
	if !g.accept(domain.Counts{}, counts(0, 0, 0)) {
		t.Error("empty after empty is trusted")
	}
	for _, snap := range []*domain.Snapshot{counts(0, 1, 3), counts(2, 0, 3), counts(2, 1, 0)} {
		var g glitchGuard
		if g.accept(base, snap) {
			t.Errorf("emptied section %+v trusted on first sight", snap.Counts())
		}
		if !g.accept(base, snap) {
			t.Errorf("emptied section %+v not trusted after confirmation", snap.Counts())
		}
	}

	g = glitchGuard{}
	g.accept(base, counts(0, 1, 3))
	g.reset()
	if g.accept(base, counts(0, 1, 3)) {
		t.Error("reset must drop the pending confirmation")
	}
}

func TestWaitUpdate(t *testing.T) {
	h := newHarness(t, false, nil)
	v0 := h.eng.Version()

	// Версия уже другая — без ожидания.
	if got := h.eng.WaitUpdate(context.Background(), v0+100); got != v0 {
		t.Errorf("stale client: got %d, want %d immediately", got, v0)
	}

	done := make(chan uint64)
	go func() { done <- h.eng.WaitUpdate(context.Background(), v0) }()
	select {
	case <-done:
		t.Fatal("returned before any update")
	case <-time.After(20 * time.Millisecond):
	}
	if err := h.eng.Mute("mr:1", "", time.Time{}); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-done:
		if got != v0+1 {
			t.Errorf("version = %d, want %d", got, v0+1)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("update did not wake the waiter")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if got := h.eng.WaitUpdate(ctx, h.eng.Version()); got != v0+1 {
		t.Errorf("timeout: got %d", got)
	}
	h.tick(t)
	if h.eng.Version() != v0+2 {
		t.Errorf("tick must bump the version: %d", h.eng.Version())
	}
}

// Опрос сломался: первая ошибка — только быстрый повтор, со второй —
// одно уведомление на серию; заработал — «снова на связи».
func TestPollHealthNotifications(t *testing.T) {
	defer func(d time.Duration) { pollRetry = d }(pollRetry)
	pollRetry = time.Millisecond
	h := newHarness(t, false, nil)
	h.tick(t) // первый запуск: запоминаем события, ничего не шлём
	ctx := context.Background()

	h.srv.FailWith(http.StatusUnauthorized)
	if err := h.eng.Tick(ctx); err == nil || len(h.sent) != 0 {
		t.Fatalf("first failure: err = %v, sent = %v", err, h.sent)
	}
	select {
	case <-h.eng.refresh:
	case <-time.After(time.Second):
		t.Fatal("first failure must schedule a quick retry")
	}

	_ = h.eng.Tick(ctx)
	_ = h.eng.Tick(ctx)
	if len(h.sent) != 1 || h.sent[0].title != "taskwatch не может обновить данные" ||
		h.sent[0].message != "Токен не подходит или истёк — обнови его в настройках" {
		t.Fatalf("failures: sent = %+v", h.sent)
	}

	h.srv.FailWith(0)
	h.tick(t)
	h.tick(t)
	if len(h.sent) != 2 || h.sent[1].title != "taskwatch снова на связи" {
		t.Errorf("recovery: sent = %+v", h.sent)
	}
}

func TestPollProblem(t *testing.T) {
	cases := map[string]error{
		"Нет связи с сервером — проверь VPN или сеть": &net.DNSError{Err: "no such host", Name: "gitlab.example.com"},
		"collect: gitlab: boom": errors.New("gitlab:\n boom"),
	}
	for want, err := range cases {
		if got := pollProblem(fmt.Errorf("collect: %w", err)); got != want {
			t.Errorf("pollProblem(%v) = %q, want %q", err, got, want)
		}
	}
}

func TestPlanWorkHours(t *testing.T) {
	cfg := config.Default("/x/config.yaml")
	cfg.PollInterval = 30 * time.Minute
	at := func(day, hour, minute int) time.Time { return time.Date(2026, 11, day, hour, minute, 0, 0, time.Local) }

	// Рабочее время выключено — опрос круглосуточно.
	if tick, wait := plan(at(7, 3, 0), cfg, false); !tick || wait != 30*time.Minute {
		t.Errorf("off: tick = %v, wait = %s", tick, wait)
	}

	cfg.WorkHours.Enabled = true // пн–пт, 10:00–19:00; 6 ноября 2026 — пятница
	cases := []struct {
		name   string
		now    time.Time
		forced bool
		tick   bool
		wait   time.Duration
	}{
		{"working hours", at(6, 12, 0), false, true, 30 * time.Minute},
		{"evening sleeps", at(6, 20, 0), false, false, offHoursCheck},
		{"«Обновить» works off hours", at(6, 20, 0), true, true, offHoursCheck},
		{"weekend sleeps", at(7, 12, 0), false, false, offHoursCheck},
		{"wakes right at the start", at(9, 9, 59), false, false, time.Minute},
		{"just before the start", at(9, 9, 59).Add(30 * time.Second), false, false, 30 * time.Second},
	}
	for _, tc := range cases {
		tick, wait := plan(tc.now, cfg, tc.forced)
		if tick != tc.tick || wait != tc.wait {
			t.Errorf("%s: tick = %v, wait = %s; want %v, %s", tc.name, tick, wait, tc.tick, tc.wait)
		}
	}
	// В начале рабочего дня — сразу опрос.
	if tick, _ := plan(at(9, 10, 0), cfg, false); !tick {
		t.Error("the working day starts with a poll")
	}
}
