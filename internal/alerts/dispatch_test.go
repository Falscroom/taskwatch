package alerts

import (
	"context"
	"errors"
	"io"
	"log"
	"os"
	"strings"
	"testing"
)

func TestMain(m *testing.M) {
	log.SetOutput(io.Discard)
	os.Exit(m.Run())
}

type ledger map[string]int // ключ → сколько раз помечен

func (l ledger) Seen(key string) bool { return l[key] > 0 }
func (l ledger) Mark(key string)      { l[key]++ }

type recorder struct {
	sent []Event
	fail bool
}

func (r *recorder) notify(ev Event) error {
	if r.fail {
		return errors.New("unavailable")
	}
	r.sent = append(r.sent, ev)
	return nil
}

func keys(events []Event) string {
	var out []string
	for _, e := range events {
		out = append(out, e.Key)
	}
	return strings.Join(out, ",")
}

var events = []Event{
	{Key: "task", Title: "T", Body: "m", URL: "u"},
	{Key: "mr1", MRID: 1},
	{Key: "mr2", MRID: 2},
}

func TestDispatchSendsOnce(t *testing.T) {
	l, r := ledger{}, &recorder{}
	d := Dispatcher{Ledger: l, Notify: r.notify}
	d.Dispatch(context.Background(), events)
	d.Dispatch(context.Background(), events)
	if keys(r.sent) != "task,mr1,mr2" || l["task"] != 1 {
		t.Errorf("sent = %s, ledger = %v", keys(r.sent), l)
	}
	if s := r.sent[0]; s.Title != "T" || s.Body != "m" || s.URL != "u" {
		t.Errorf("notification = %+v", s)
	}
}

func TestDispatchSeedMarksSilently(t *testing.T) {
	l, r := ledger{}, &recorder{}
	Dispatcher{Ledger: l, Notify: r.notify, Seed: true}.Dispatch(context.Background(), events)
	if len(r.sent) != 0 || len(l) != 3 {
		t.Errorf("sent = %s, ledger = %v", keys(r.sent), l)
	}
}

func TestDispatchMuted(t *testing.T) {
	l, r := ledger{"mr1": 1}, &recorder{}
	d := Dispatcher{Ledger: l, Notify: r.notify, Muted: func(id int) bool { return id == 1 || id == 2 }}
	d.Dispatch(context.Background(), events)
	if keys(r.sent) != "task" {
		t.Errorf("sent = %s", keys(r.sent))
	}
	// Заглушённое помечается и повторно — чтобы не протухло по сроку хранения.
	if l["mr1"] != 2 || l["mr2"] != 1 {
		t.Errorf("ledger = %v", l)
	}
}

func TestDispatchFailedNotificationIsRetried(t *testing.T) {
	l, r := ledger{}, &recorder{fail: true}
	d := Dispatcher{Ledger: l, Notify: r.notify}
	d.Dispatch(context.Background(), events[:1])
	if l.Seen("task") {
		t.Fatal("failed notification marked as seen")
	}
	r.fail = false
	d.Dispatch(context.Background(), events[:1])
	if !l.Seen("task") || len(r.sent) != 1 {
		t.Errorf("retry: sent = %s", keys(r.sent))
	}
}

func TestDispatchAutoTransition(t *testing.T) {
	ev := []Event{{Key: "approved:1", MRID: 1, Body: "Пора переводить", Transition: &Transition{IssueID: "ABC-1", Command: "State QA"}}}

	var calls []string
	ok := func(ctx context.Context, issue, cmd string) error { calls = append(calls, issue+"/"+cmd); return nil }
	r := &recorder{}
	Dispatcher{Ledger: ledger{}, Notify: r.notify, AutoTransition: ok}.Dispatch(context.Background(), ev)
	// Переведено — кнопка «перевести» в уведомлении уже не нужна.
	if s := r.sent[0]; strings.Join(calls, ",") != "ABC-1/State QA" || s.Title != "Переведена в QA" || s.Transition != nil {
		t.Errorf("calls = %v, sent = %+v", calls, r.sent)
	}
	if ev[0].Transition == nil {
		t.Error("dispatch changed the caller's event")
	}

	failing := func(ctx context.Context, issue, cmd string) error { return errors.New("403") }
	r = &recorder{}
	Dispatcher{Ledger: ledger{}, Notify: r.notify, AutoTransition: failing}.Dispatch(context.Background(), ev)
	// Не вышло — кнопка остаётся: можно перевести руками из уведомления.
	if s := r.sent[0]; s.Body != "Не удалось перевести автоматически: 403" || s.Transition == nil {
		t.Errorf("sent = %+v", r.sent)
	}

	// Без авто-перевода, на первом запуске и для уже доставленного — не переводим.
	calls = nil
	Dispatcher{Ledger: ledger{}, Notify: r.notify}.Dispatch(context.Background(), ev)
	Dispatcher{Ledger: ledger{}, Notify: r.notify, AutoTransition: ok, Seed: true}.Dispatch(context.Background(), ev)
	Dispatcher{Ledger: ledger{"approved:1": 1}, Notify: r.notify, AutoTransition: ok}.Dispatch(context.Background(), ev)
	if len(calls) != 0 {
		t.Errorf("unexpected transitions: %v", calls)
	}
}
