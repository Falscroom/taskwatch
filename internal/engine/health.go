package engine

import (
	"fmt"
	"time"

	"taskwatch/internal/alerts"
	"taskwatch/internal/failure"
)

// pollRetry — через сколько перепроверить после первой ошибки опроса.
var pollRetry = time.Minute

// health — уведомления о том, что опрос сломался и снова работает. Одна ошибка
// — ещё не поломка: после сна сеть и VPN поднимаются не сразу. Поэтому первая
// ошибка подряд — только быстрый повтор через pollRetry, уведомление — со второй,
// и одно на всю серию. Живёт внутри Tick, как glitchGuard.
type health struct {
	failures int  // ошибок подряд
	notified bool // про эту серию уже сообщили
}

// failed — опрос не удался: событие для уведомления (или nil) и нужен ли
// быстрый повтор.
func (h *health) failed(err error) (ev *alerts.Event, retry bool) {
	h.failures++
	if h.failures == 1 {
		return nil, true
	}
	if h.notified {
		return nil, false
	}
	h.notified = true
	return &alerts.Event{
		Key:    fmt.Sprintf("poll-failed:%d", time.Now().UnixNano()),
		Kind:   alerts.KindPollFailed,
		Title:  "taskwatch не может обновить данные",
		Body:   failure.Reason(err),
		Thread: "taskwatch",
	}, false
}

// ok — опрос прошёл: если о поломке сообщали — событие «снова на связи».
func (h *health) ok() *alerts.Event {
	notified := h.notified
	h.failures, h.notified = 0, false
	if !notified {
		return nil
	}
	return &alerts.Event{
		Key:    fmt.Sprintf("poll-recovered:%d", time.Now().UnixNano()),
		Kind:   alerts.KindPollRecovered,
		Title:  "taskwatch снова на связи",
		Body:   "Данные обновляются",
		Thread: "taskwatch",
	}
}
