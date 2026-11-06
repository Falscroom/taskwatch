package tray

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
)

// Окно, скрытое дольше releaseAfter, закрывается; проверка — раз в releaseCheck.
var (
	releaseAfter = time.Minute
	releaseCheck = 15 * time.Second
)

// window — то, что popup нужно от окна Wails; в тестах — подделка.
type window interface {
	IsVisible() bool
	Close()
}

// popup — окно у иконки в меню-баре. WebView держит около 200 МБ во
// вспомогательных процессах WebKit даже скрытым, поэтому окно, скрытое дольше
// releaseAfter, закрывается целиком и создаётся заново при следующем клике по
// иконке. Пока не прошла минута, повторное открытие мгновенное.
type popup struct {
	open   func() (window, *atomic.Bool) // создать окно; флаг разрешает ему закрыться
	attach func(window)                  // привязать окно к иконке
	toggle func()                        // показать или спрятать привязанное окно
	show   func()                        // показать привязанное окно у иконки

	mu        sync.Mutex
	win       window
	releasing *atomic.Bool
	seen      time.Time // когда окно последний раз было видно
}

// click — клик по иконке: есть окно — показать или спрятать, нет — создать.
func (p *popup) click() {
	if p.current() != nil {
		p.toggle()
		return
	}
	p.reveal()
}

// reveal показывает окно, создавая его при необходимости: повторный запуск
// приложения и клик по иконке без окна.
func (p *popup) reveal() {
	p.mu.Lock()
	if p.win == nil {
		p.win, p.releasing = p.open()
		p.attach(p.win)
	}
	p.seen = time.Now()
	p.mu.Unlock()
	p.show()
}

func (p *popup) current() window {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.win
}

// releaseIdle закрывает окно, если оно скрыто дольше releaseAfter. Методы окна
// вызываются без блокировки: они ждут главного потока, а он может в это время
// разбирать клик по иконке, которому нужна та же блокировка.
func (p *popup) releaseIdle(now time.Time) {
	w := p.current()
	if w == nil {
		return
	}
	visible := w.IsVisible()
	p.mu.Lock()
	if p.win != w {
		p.mu.Unlock()
		return
	}
	if visible || now.Sub(p.seen) < releaseAfter {
		if visible {
			p.seen = now
		}
		p.mu.Unlock()
		return
	}
	releasing := p.releasing
	p.win, p.releasing = nil, nil
	p.mu.Unlock()
	releasing.Store(true)
	w.Close()
}

// watch проверяет окно раз в releaseCheck, пока не отменят ctx.
func (p *popup) watch(ctx context.Context) {
	t := time.NewTicker(releaseCheck)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-t.C:
			p.releaseIdle(now)
		}
	}
}

// newWindow — окно taskwatch, скрытое до показа у иконки. Закрыть его может
// только popup (флаг releasing): обычное закрытие, например ⌘W, лишь прячет.
func newWindow(app *application.App) (window, *atomic.Bool) {
	w := app.Window.NewWithOptions(application.WebviewWindowOptions{
		Name:            "taskwatch",
		Width:           520,
		Height:          640,
		Frameless:       true,
		AlwaysOnTop:     true,
		Hidden:          true,
		DisableResize:   true,
		HideOnEscape:    true,
		HideOnFocusLost: true,
		BackgroundType:  application.BackgroundTypeTransparent,
		Mac: application.MacWindow{
			Backdrop: application.MacBackdropTransparent,
			// Совпадает с border-radius панели в CSS: иначе AppKit скругляет окно
			// на свои ~10pt и в углах виден зазор между двумя радиусами.
			CornerRadius: 20,
		},
	})
	releasing := new(atomic.Bool)
	w.RegisterHook(events.Common.WindowClosing, func(e *application.WindowEvent) {
		if releasing.Load() {
			return
		}
		w.Hide()
		e.Cancel()
	})
	return w, releasing
}
