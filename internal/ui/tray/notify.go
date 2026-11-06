package tray

import (
	"context"
	"fmt"
	"log"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/services/notifications"

	"taskwatch/internal/alerts"
	"taskwatch/internal/domain"
	"taskwatch/internal/engine"
)

// Кнопки уведомлений.
const (
	actionMute       = "mute"
	actionTransition = "transition"
	categoryMR       = "mr"
)

// Notifier — уведомления taskwatch. Внутри taskwatch.app они идут через Центр
// уведомлений macOS: своя иконка, подзаголовок, клик открывает MR или таску,
// кнопки «Заглушить на час» и «→ статус». Вне бандла (run, once, web, демо)
// и до запуска окна — через fallback.
type Notifier struct {
	fallback alerts.Notifier
	svc      *notifications.NotificationService
	native   atomic.Bool // Центр уведомлений запущен — мы внутри бандла
	warned   atomic.Bool // про выключенные уведомления уже написали в лог

	mu         sync.Mutex
	categories map[string]bool // уже зарегистрированные наборы кнопок
}

func NewNotifier(fallback alerts.Notifier) *Notifier {
	return &Notifier{fallback: fallback, svc: notifications.New(), categories: map[string]bool{}}
}

// Notify — alerts.Notifier.
func (n *Notifier) Notify(ev alerts.Event) error {
	if !n.native.Load() {
		return n.fallback(ev)
	}
	return n.refused(n.send(ev))
}

// send — уведомление в Центр уведомлений, с кнопками, если они есть.
func (n *Notifier) send(ev alerts.Event) error {
	opts := options(ev)
	if opts.CategoryID == "" {
		return n.svc.SendNotification(opts)
	}
	if err := n.register(opts.CategoryID, ev); err != nil {
		return err
	}
	return n.svc.SendNotificationWithActions(opts)
}

// refused разбирает ответ на отправку. Разрешение заранее не спрашиваем: статус
// уведомлений у такого приложения бывает не «разрешено» даже тогда, когда они
// показываются, — верим только отказу в самой отправке. Отказали — уведомления
// выключены: не обходим запрет через osascript, событие откладываем до
// следующего опроса и пишем об этом в лог один раз.
func (n *Notifier) refused(err error) error {
	if err == nil {
		n.warned.Store(false)
		return nil
	}
	if !notAllowed(err) {
		return err
	}
	if !n.warned.Swap(true) {
		log.Printf("уведомления taskwatch выключены — включить: Системные настройки → Уведомления → taskwatch")
	}
	return alerts.ErrRetryLater
}

// notAllowed — macOS отклонила уведомление: оно выключено в настройках.
func notAllowed(err error) bool {
	return strings.Contains(strings.ToLower(err.Error()), "not allowed")
}

// register регистрирует набор кнопок один раз: у перевода кнопка зависит от статуса.
func (n *Notifier) register(id string, ev alerts.Event) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.categories[id] {
		return nil
	}
	actions := []notifications.NotificationAction{{ID: actionMute, Title: "Заглушить на час"}}
	if ev.Transition != nil {
		label := "→ " + domain.TransitionLabel(ev.Transition.Command)
		actions = append([]notifications.NotificationAction{{ID: actionTransition, Title: label}}, actions...)
	}
	if err := n.svc.RegisterNotificationCategory(notifications.NotificationCategory{ID: id, Actions: actions}); err != nil {
		return err
	}
	n.categories[id] = true
	return nil
}

// start — запуск вместе с окном. Вне бандла Центр уведомлений недоступен:
// остаёмся на fallback, а не роняем приложение.
func (n *Notifier) start(ctx context.Context, opts application.ServiceOptions) {
	if err := n.svc.ServiceStartup(ctx, opts); err != nil {
		log.Printf("уведомления: %v — показываю через osascript", err)
		return
	}
	n.native.Store(true)
	// Спрашиваем разрешение, чтобы macOS показала запрос при первом запуске.
	// Ответу не верим — он бывает «не разрешено» и при включённых
	// уведомлениях; итог решает сама отправка (refused).
	go func() { _, _ = n.svc.RequestNotificationAuthorization() }()
}

// service — Notifier в виде сервиса Wails: только запуск, без методов для JS.
type service struct{ n *Notifier }

func (s service) ServiceStartup(ctx context.Context, opts application.ServiceOptions) error {
	s.n.start(ctx, opts)
	return nil
}

// options — уведомление Центра уведомлений для события. Ключ события —
// идентификатор: повтор того же события заменяет уведомление, а не дублирует.
func options(ev alerts.Event) notifications.NotificationOptions {
	o := notifications.NotificationOptions{
		ID:       ev.Key,
		Title:    ev.Title,
		Subtitle: ev.Subtitle,
		Body:     ev.Body,
		ThreadID: ev.Thread,
		Data:     map[string]any{"url": ev.URL},
	}
	if !ev.Kind.Loud() {
		o.Sound = &notifications.NotificationSound{Silent: true}
	}
	if ev.MRID != 0 {
		o.Data["mr"] = domain.MRKey(ev.MRID)
		o.CategoryID = categoryMR
	}
	if ev.Transition != nil {
		o.Data["issue"] = ev.Transition.IssueID
		o.CategoryID = actionTransition + ":" + ev.Transition.Command
	}
	return o
}

// responder — что умеет приложение в ответ на уведомление.
type responder struct {
	open       func(url string)
	mute       func(mrKey string)
	transition func(issueID string)
}

// respond — что сделать по клику на уведомление или его кнопке.
func respond(r notifications.NotificationResponse, do responder) {
	str := func(key string) string { s, _ := r.UserInfo[key].(string); return s }
	switch r.ActionIdentifier {
	case actionMute:
		if key := str("mr"); key != "" {
			do.mute(key)
		}
	case actionTransition:
		if id := str("issue"); id != "" {
			do.transition(id)
		}
	case notifications.DefaultActionIdentifier: // клик по самому уведомлению
		if url := str("url"); url != "" {
			do.open(url)
		}
	}
}

// listen отвечает на клики по уведомлениям: открыть, заглушить на час,
// перевести таску — с ответным уведомлением о том, как прошёл перевод.
func (n *Notifier) listen(eng *engine.Engine, openURL func(string) error) {
	n.svc.OnNotificationResponse(func(res notifications.NotificationResult) {
		if res.Error != nil {
			log.Printf("уведомление: %v", res.Error)
			return
		}
		respond(res.Response, responder{
			open: func(url string) {
				if err := openURL(url); err != nil {
					log.Printf("открыть %s: %v", url, err)
				}
			},
			mute: func(key string) {
				if err := eng.Mute(key, "", time.Now().Add(time.Hour)); err != nil {
					log.Printf("заглушить %s: %v", key, err)
				}
			},
			transition: func(issueID string) {
				ev := alerts.Event{
					Key:      fmt.Sprintf("transitioned:%s:%d", issueID, time.Now().UnixNano()),
					Kind:     alerts.KindApproved,
					Title:    "Переведена в " + domain.TransitionLabel(eng.Config().Rules.NextCommand),
					Subtitle: issueID,
				}
				if err := eng.Transition(context.Background(), issueID); err != nil {
					ev.Title, ev.Body = "Не удалось перевести", err.Error()
				}
				if err := n.Notify(ev); err != nil {
					log.Printf("уведомление о переводе %s: %v", issueID, err)
				}
			},
		})
	})
}

// sample показывает пример уведомления — пункт меню иконки. Кнопка в нём для
// вида: заглушать нечего.
func (n *Notifier) sample() {
	ev := alerts.Event{
		Key:      fmt.Sprintf("sample:%d", time.Now().UnixNano()),
		Kind:     alerts.KindReview,
		Title:    "Так выглядят уведомления",
		Subtitle: "APP-1 · Пример таски",
		Body:     "Клик по настоящему уведомлению открывает MR или таску",
	}
	if !n.native.Load() {
		if err := n.fallback(ev); err != nil {
			log.Printf("пример уведомления: %v", err)
		}
		return
	}
	opts := options(ev)
	opts.CategoryID = categoryMR
	err := n.register(categoryMR, ev)
	if err == nil {
		err = n.svc.SendNotificationWithActions(opts)
	}
	if err != nil {
		log.Printf("пример уведомления: %v", err)
	}
}
