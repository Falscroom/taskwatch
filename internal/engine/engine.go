// Package engine крутит цикл опроса: собирает снапшот, шлёт одноразовые уведомления
// и держит последний снапшот для UI.
package engine

import (
	"context"
	"errors"
	"fmt"
	"log"
	"sync"
	"sync/atomic"
	"time"

	"taskwatch/internal/config"
	"taskwatch/internal/notify"
	"taskwatch/internal/rules"
	"taskwatch/internal/state"
	"taskwatch/internal/watch"
	"taskwatch/internal/youtrack"
)

// errNotConfigured — конфиг ещё не заполнен, движок ждёт настройки из UI.
var errNotConfigured = errors.New("настройки не заполнены — открой настройки и введи токен")

// deps — неизменяемый набор конфига и клиентов; Reload подменяет его целиком,
// поэтому Tick и Reload не мешают друг другу без блокировок.
type deps struct {
	cfg       *config.Config
	collector *watch.Collector
	yt        *youtrack.Client
}

type Engine struct {
	store   *state.Store
	refresh chan struct{}
	deps    atomic.Pointer[deps]

	// OnUpdate вызывается после каждого прохода опроса (успешного или нет)
	OnUpdate func()

	mu      sync.RWMutex
	snap    *watch.Snapshot
	updated time.Time
	lastErr error

	// recheck — прошлый тик выглядел подозрительно (пустота там, где были карточки):
	// следующий тик подтверждает пустоту или опровергает её.
	recheck bool
}

func New(cfg *config.Config, collector *watch.Collector, store *state.Store, yt *youtrack.Client) *Engine {
	e := &Engine{store: store, refresh: make(chan struct{}, 1)}
	e.deps.Store(&deps{cfg: cfg, collector: collector, yt: yt})
	return e
}

// Reload подменяет конфиг и клиентов — вызывается после сохранения настроек из UI.
func (e *Engine) Reload(cfg *config.Config, collector *watch.Collector, yt *youtrack.Client) {
	e.deps.Store(&deps{cfg: cfg, collector: collector, yt: yt})
	e.Refresh()
}

// Config возвращает текущий конфиг — для UI (view.Build, форма настроек).
func (e *Engine) Config() *config.Config { return e.deps.Load().cfg }

func (e *Engine) Run(ctx context.Context) {
	first := true
	for {
		started := time.Now()
		if err := e.Tick(ctx); err != nil {
			log.Printf("tick: %v", err)
		}
		if first {
			// Разовый ориентир, сколько занимает полный сбор — помогает
			// отличать «данные собираются» от «сбор не запускался».
			first = false
			snap, _, _ := e.Snapshot()
			if snap != nil {
				log.Printf("первый опрос завершён за %s: тасок %d, MR %d",
					time.Since(started).Round(time.Second), len(snap.Tasks), len(snap.AllMRs()))
			}
		}
		// Интервал перечитываем каждый цикл: новые настройки вступают в силу сразу.
		timer := time.NewTimer(e.Config().PollInterval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		case <-e.refresh:
			timer.Stop()
		}
	}
}

// Refresh просит Run сделать внеочередной проход.
func (e *Engine) Refresh() {
	select {
	case e.refresh <- struct{}{}:
	default:
	}
}

func (e *Engine) Snapshot() (*watch.Snapshot, time.Time, error) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.snap, e.updated, e.lastErr
}

// Tick не потокобезопасен относительно самого себя: вызывать из одного места (Run или once).
func (e *Engine) Tick(ctx context.Context) error {
	d := e.deps.Load()

	var snap *watch.Snapshot
	err := errNotConfigured
	if d.cfg.Configured() {
		snap, err = d.collector.Collect(ctx)
	}

	// GitLab иногда отдаёт пустой список там, где только что были карточки, и
	// ошибкой это не считается. Проглотить такой тик — значит на poll_interval
	// остаться без своих MR (ревью при этом могут остаться). Пустоту принимаем
	// только со второго подтверждения, а между тиками устраиваем перепроверку.
	if snap != nil && e.degraded(snap) && !e.recheck {
		e.recheck = true
		base, now := e.countsBaseline(), countsOf(snap)
		log.Printf("подозрительный тик: свои MR %d→%d, ревью %d→%d, таски %d→%d — держу прошлый вид, перепроверяю сразу",
			base.Own, now.Own, base.Reviews, now.Reviews, base.Tasks, now.Tasks)
		e.Refresh()
		if e.OnUpdate != nil {
			defer e.OnUpdate()
		}
		return nil
	}
	if snap != nil && e.degraded(snap) {
		log.Print("пустота подтвердилась повторным тиком — считаю правдой")
	}
	e.recheck = false

	e.setLast(snap, err)
	if snap != nil {
		e.store.SetCounts(countsOf(snap))
	}
	if e.OnUpdate != nil {
		defer e.OnUpdate()
	}
	if err != nil {
		return err
	}

	seed := e.store.Fresh()
	if seed {
		log.Print("first run: remembering current events without notifications")
	}

	mutes := e.store.Mutes()
	now := time.Now()
	// Заглушенность MR: личное правило "mr:<id>" или групповое "title:<название>".
	mutedIDs := make(map[int]bool)
	for _, mr := range snap.AllMRs() {
		if state.Active(mutes[fmt.Sprintf("mr:%d", mr.ID)], now) ||
			state.Active(mutes["title:"+mr.Title], now) {
			mutedIDs[mr.ID] = true
		}
	}
	for _, ev := range rules.Evaluate(snap, d.cfg) {
		// Заглушенный MR: событие запоминаем без отправки, чтобы после снятия заглушения
		// не прилетела накопившаяся за время тишины старуха.
		if ev.MRID != 0 && mutedIDs[ev.MRID] {
			e.store.Mark(ev.Key)
			continue
		}
		if e.store.Seen(ev.Key) {
			continue
		}
		if !seed {
			e.autoTransition(ctx, d, &ev)
			if err := notify.Send(ev.Title, ev.Message, ev.URL, ev.Key); err != nil {
				log.Printf("notify %s: %v", ev.Key, err)
				continue
			}
		}
		e.store.Mark(ev.Key)
	}
	return e.store.Save()
}

func (e *Engine) setLast(snap *watch.Snapshot, err error) {
	e.mu.Lock()
	e.lastErr = err
	if err == nil {
		e.snap = snap
		e.updated = time.Now()
	}
	e.mu.Unlock()
}

// Transition переводит таску командой из конфига по запросу из UI.
func (e *Engine) Transition(ctx context.Context, issueID string) error {
	d := e.deps.Load()
	if !d.cfg.YouTrackOK() {
		return errors.New("youtrack is not configured")
	}
	if err := d.yt.Command(ctx, issueID, d.cfg.Rules.NextCommand); err != nil {
		return err
	}
	e.Refresh()
	return nil
}

// Mute глушит MR (ключ карточки "mr:<id>") до until; zero — навсегда: карточка
// прячется в группу «Заглушено», уведомления по нему прекращаются. Непустой title
// добавляет правило по точному названию — под него попадают и будущие MR с ним.
func (e *Engine) Mute(key, title string, until time.Time) error {
	e.store.Mute(key, until)
	if title != "" {
		e.store.Mute("title:"+title, until)
	}
	if err := e.store.Save(); err != nil {
		return err
	}
	// Вид пересобирается при следующем /api/view, а бейджу нужен толчок сейчас.
	if e.OnUpdate != nil {
		e.OnUpdate()
	}
	return nil
}

// Unmute возвращает MR: снапшот всегда содержит все MR, карточка появится сразу.
// Снимает и правило по названию, чтобы вся группа вернулась вместе с карточкой.
func (e *Engine) Unmute(key, title string) error {
	e.store.Unmute(key)
	if title != "" {
		e.store.Unmute("title:" + title)
	}
	if err := e.store.Save(); err != nil {
		return err
	}
	if e.OnUpdate != nil {
		e.OnUpdate()
	}
	return nil
}

// Mutes — текущие заглушения (ключ → до когда; zero = навсегда) для view.Build.
func (e *Engine) Mutes() map[string]time.Time { return e.store.Mutes() }

// countsOf — размеры снапшота по разделам, для сравнения тиков между собой.
func countsOf(s *watch.Snapshot) state.Counts {
	c := state.Counts{Reviews: len(s.Reviews), Tasks: len(s.Tasks)}
	for _, t := range s.Tasks {
		c.Own += len(t.MRs)
	}
	c.Own += len(s.Orphans)
	return c
}

// countsBaseline — с чем сравнивать: прошлый снапшот в памяти, а после рестарта —
// счётчики из state.json.
func (e *Engine) countsBaseline() state.Counts {
	if prev, _, _ := e.Snapshot(); prev != nil {
		return countsOf(prev)
	}
	return e.store.Counts()
}

// degraded — тик обнулил разделы, которые были непустыми. Честная ошибка так
// не выглядит (ошибки возвращаются с err), это похоже на сбой выдачи GitLab.
func (e *Engine) degraded(snap *watch.Snapshot) bool {
	base, now := e.countsBaseline(), countsOf(snap)
	return (now.Own == 0 && base.Own > 0) ||
		(now.Reviews == 0 && base.Reviews > 0) ||
		(now.Tasks == 0 && base.Tasks > 0)
}

func (e *Engine) autoTransition(ctx context.Context, d *deps, ev *rules.Event) {
	if ev.Transition == nil || !d.cfg.Rules.AutoTransition {
		return
	}
	if err := d.yt.Command(ctx, ev.Transition.IssueID, ev.Transition.Command); err != nil {
		log.Printf("transition %s: %v", ev.Transition.IssueID, err)
		ev.Message = "Не удалось перевести автоматически: " + err.Error()
		return
	}
	ev.Message = "Переведена: " + ev.Transition.Command
}
