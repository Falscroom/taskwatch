// Package engine крутит цикл опроса: собирает снапшот, доставляет уведомления
// и держит последний снапшот для окна. Клиентов движок получает через Factory,
// а UI узнаёт о новостях через Subscribe и Version/WaitUpdate — ни GitLab, ни
// окна движок не знает.
package engine

import (
	"context"
	"errors"
	"log"
	"sync"
	"sync/atomic"
	"time"

	"taskwatch/internal/alerts"
	"taskwatch/internal/config"
	"taskwatch/internal/domain"
	"taskwatch/internal/state"
)

// errNotConfigured — конфиг ещё не заполнен, движок ждёт настройки из UI.
var errNotConfigured = errors.New("настройки не заполнены — открой настройки и введи токен")

// Collector — откуда движок берёт снапшот.
type Collector interface {
	Collect(ctx context.Context) (*domain.Snapshot, error)
}

// Deps — то, что пересобирается при смене настроек.
type Deps struct {
	Collector Collector
	// Transition применяет команду YouTrack к таске; nil — YouTrack не настроен.
	Transition alerts.Transitioner
}

// Factory собирает Deps под конфиг.
type Factory func(cfg *config.Config) Deps

// current — конфиг и собранные под него зависимости. Reload подменяет их
// целиком, поэтому Tick и Reload не мешают друг другу без блокировок.
type current struct {
	cfg  *config.Config
	deps Deps
}

type Engine struct {
	store   *state.Store
	notify  alerts.Notifier
	build   Factory
	refresh chan struct{}
	current atomic.Pointer[current]

	mu      sync.RWMutex
	snap    *domain.Snapshot
	updated time.Time
	lastErr error

	subsMu  sync.Mutex
	subs    map[int]func()
	nextSub int
	version atomic.Uint64 // растёт с каждым publish

	guard glitchGuard // только из Tick
}

func New(cfg *config.Config, store *state.Store, notify alerts.Notifier, build Factory) *Engine {
	e := &Engine{store: store, notify: notify, build: build, refresh: make(chan struct{}, 1), subs: map[int]func(){}}
	e.current.Store(&current{cfg: cfg, deps: build(cfg)})
	return e
}

// Reload пересобирает зависимости под новый конфиг — после сохранения настроек из UI.
func (e *Engine) Reload(cfg *config.Config) {
	e.current.Store(&current{cfg: cfg, deps: e.build(cfg)})
	e.Refresh()
}

// Config возвращает текущий конфиг — для UI (view.Build, форма настроек).
func (e *Engine) Config() *config.Config { return e.current.Load().cfg }

// Subscribe регистрирует fn: он вызывается после каждого прохода опроса
// (успешного или нет) и после смены заглушений. Возвращает отписку.
func (e *Engine) Subscribe(fn func()) (unsubscribe func()) {
	e.subsMu.Lock()
	defer e.subsMu.Unlock()
	id := e.nextSub
	e.nextSub++
	e.subs[id] = fn
	return func() {
		e.subsMu.Lock()
		defer e.subsMu.Unlock()
		delete(e.subs, id)
	}
}

// Version — номер последнего обновления: растёт после каждого прохода опроса
// и смены заглушений. По нему окно понимает, что вид устарел.
func (e *Engine) Version() uint64 { return e.version.Load() }

// WaitUpdate ждёт, пока версия станет отличной от seen, или пока не кончится ctx;
// возвращает текущую версию. Отличной, а не большей: после рестарта счёт идёт
// заново, и клиент со старой версией не должен ждать.
func (e *Engine) WaitUpdate(ctx context.Context, seen uint64) uint64 {
	if v := e.version.Load(); v != seen {
		return v
	}
	updated := make(chan struct{}, 1)
	unsubscribe := e.Subscribe(func() {
		select {
		case updated <- struct{}{}:
		default:
		}
	})
	defer unsubscribe()
	// Обновление могло проскочить между первой проверкой и подпиской.
	if v := e.version.Load(); v != seen {
		return v
	}
	select {
	case <-updated:
	case <-ctx.Done():
	}
	return e.version.Load()
}

func (e *Engine) publish() {
	e.version.Add(1)
	e.subsMu.Lock()
	fns := make([]func(), 0, len(e.subs))
	for _, fn := range e.subs {
		fns = append(fns, fn)
	}
	e.subsMu.Unlock()
	for _, fn := range fns {
		fn()
	}
}

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
			if snap, _, _ := e.Snapshot(); snap != nil {
				log.Printf("первый опрос завершён за %s: тасок %d, MR %d",
					time.Since(started).Round(time.Second), len(snap.Tasks), len(snap.AllMRs()))
			}
		}
		// Интервал перечитываем каждый цикл: новые настройки вступают в силу сразу.
		timer := time.NewTimer(pollInterval(e.Config()))
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

// pollInterval — пауза между тиками. Неположительный интервал из битого yaml
// превратил бы цикл в долбёжку API, поэтому берём дефолт.
func pollInterval(cfg *config.Config) time.Duration {
	if cfg.PollInterval <= 0 {
		return config.DefaultPollInterval
	}
	return cfg.PollInterval
}

// Refresh просит Run сделать внеочередной проход.
func (e *Engine) Refresh() {
	select {
	case e.refresh <- struct{}{}:
	default:
	}
}

// Snapshot — последний принятый снапшот, когда он собран, и ошибка последнего тика.
func (e *Engine) Snapshot() (*domain.Snapshot, time.Time, error) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.snap, e.updated, e.lastErr
}

// Tick — один проход: собрать, проверить на сбой выдачи, запомнить, доставить
// уведомления. Не потокобезопасен относительно самого себя: вызывать из одного
// места (Run или once).
func (e *Engine) Tick(ctx context.Context) error {
	defer e.publish()
	cur := e.current.Load()

	if !cur.cfg.Configured() {
		e.fail(errNotConfigured)
		return errNotConfigured
	}
	snap, err := cur.deps.Collector.Collect(ctx)
	if err != nil {
		e.fail(err)
		return err
	}
	if !e.guard.accept(e.baseline(), snap) {
		e.Refresh() // перепроверить сразу, прошлый вид пока остаётся
		return nil
	}

	e.mu.Lock()
	e.snap, e.updated, e.lastErr = snap, time.Now(), nil
	e.mu.Unlock()
	e.store.SetCounts(state.Counts(snap.Counts()))

	e.deliver(ctx, cur, snap)
	return e.store.Save()
}

// fail запоминает ошибку тика; прошлый снапшот остаётся на экране.
func (e *Engine) fail(err error) {
	e.guard.reset()
	e.mu.Lock()
	e.lastErr = err
	e.mu.Unlock()
}

// baseline — с чем сравнивать новый снапшот: прошлый в памяти, а после
// рестарта — счётчики из state.json.
func (e *Engine) baseline() domain.Counts {
	if prev, _, _ := e.Snapshot(); prev != nil {
		return prev.Counts()
	}
	return domain.Counts(e.store.Counts())
}

func (e *Engine) deliver(ctx context.Context, cur *current, snap *domain.Snapshot) {
	seed := e.store.Fresh()
	if seed {
		log.Print("first run: remembering current events without notifications")
	}
	mutes, now := e.Mutes(), time.Now()
	muted := make(map[int]bool)
	for _, mr := range snap.AllMRs() {
		if _, ok := mutes.Match(mr, now); ok {
			muted[mr.ID] = true
		}
	}
	d := alerts.Dispatcher{
		Ledger: e.store,
		Notify: e.notify,
		Seed:   seed,
		Muted:  func(id int) bool { return muted[id] },
	}
	if cur.cfg.Rules.AutoTransition {
		d.AutoTransition = cur.deps.Transition
	}
	d.Dispatch(ctx, alerts.Evaluate(snap, cur.cfg.Policy()))
}

// Transition переводит таску командой из конфига по запросу из UI.
func (e *Engine) Transition(ctx context.Context, issueID string) error {
	cur := e.current.Load()
	if cur.deps.Transition == nil {
		return errors.New("youtrack is not configured")
	}
	if err := cur.deps.Transition(ctx, issueID, cur.cfg.Rules.NextCommand); err != nil {
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
		e.store.Mute(domain.TitleKey(title), until)
	}
	return e.saveMutes()
}

// Unmute возвращает MR: снапшот всегда содержит все MR, карточка появится сразу.
// Снимает и правило по названию, чтобы вся группа вернулась вместе с карточкой.
func (e *Engine) Unmute(key, title string) error {
	e.store.Unmute(key)
	if title != "" {
		e.store.Unmute(domain.TitleKey(title))
	}
	return e.saveMutes()
}

func (e *Engine) saveMutes() error {
	if err := e.store.Save(); err != nil {
		return err
	}
	// Вид пересобирается при следующем /api/view, а бейджу нужен толчок сейчас.
	e.publish()
	return nil
}

// Mutes — текущие заглушения для view.Build и сверки событий.
func (e *Engine) Mutes() domain.MuteRules { return e.store.Mutes() }
