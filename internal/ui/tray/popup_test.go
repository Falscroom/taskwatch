package tray

import (
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type fakeWindow struct {
	visible bool
	closed  bool
}

func (w *fakeWindow) IsVisible() bool { return w.visible }
func (w *fakeWindow) Close()          { w.closed = true }

func TestPopup(t *testing.T) {
	var log []string
	var opened []*fakeWindow
	var flags []*atomic.Bool
	p := &popup{
		open: func() (window, *atomic.Bool) {
			w, f := &fakeWindow{}, new(atomic.Bool)
			opened, flags = append(opened, w), append(flags, f)
			log = append(log, "open")
			return w, f
		},
		attach: func(window) { log = append(log, "attach") },
		toggle: func() { log = append(log, "toggle") },
		show:   func() { log = append(log, "show") },
	}

	// Первый клик создаёт окно, второй — прячет его.
	p.click()
	p.click()
	if got := strings.Join(log, ","); got != "open,attach,show,toggle" {
		t.Fatalf("clicks: %s", got)
	}

	now := time.Now()
	opened[0].visible = true
	p.releaseIdle(now.Add(2 * releaseAfter)) // видно — не трогаем, только запоминаем
	opened[0].visible = false
	p.releaseIdle(now.Add(2*releaseAfter + releaseAfter/2)) // скрыто недавно
	if opened[0].closed {
		t.Fatal("window hidden for less than releaseAfter must stay")
	}

	p.releaseIdle(now.Add(4 * releaseAfter)) // скрыто дольше минуты
	if !opened[0].closed || !flags[0].Load() || p.current() != nil {
		t.Fatalf("idle window: closed = %v, allowed = %v", opened[0].closed, flags[0].Load())
	}

	// После закрытия клик снова создаёт окно.
	log = nil
	p.click()
	if got := strings.Join(log, ","); got != "open,attach,show" || len(opened) != 2 {
		t.Errorf("click after release: %s", got)
	}
	p.releaseIdle(time.Now().Add(releaseAfter / 2))
	if opened[1].closed {
		t.Error("a freshly opened window must not be closed")
	}
}
