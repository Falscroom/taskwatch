package alerts

import (
	"context"
	"log"

	"taskwatch/internal/domain"
)

// Notifier показывает уведомление о событии. Key события служит идентификатором
// уведомления: macOS заменяет уведомление с тем же ключом, а не дублирует.
type Notifier func(Event) error

// Transitioner применяет к таске команду YouTrack.
type Transitioner func(ctx context.Context, issueID, command string) error

// Ledger помнит, какие события уже доставлены.
type Ledger interface {
	Seen(key string) bool
	Mark(key string)
}

// Dispatcher доставляет события, каждое — один раз.
type Dispatcher struct {
	Ledger Ledger
	Notify Notifier
	// AutoTransition переводит таску, как только собраны апрувы; nil — не переводить.
	AutoTransition Transitioner
	// Seed — первый запуск: события запоминаются без отправки, иначе прилетит вся история.
	Seed bool
	// Muted — заглушён ли MR.
	Muted func(mrID int) bool
}

func (d Dispatcher) Dispatch(ctx context.Context, events []Event) {
	for _, ev := range events {
		switch {
		case ev.MRID != 0 && d.Muted != nil && d.Muted(ev.MRID):
			// Запоминаем без отправки, чтобы после снятия заглушения не
			// прилетела накопившаяся за время тишины старина.
			d.Ledger.Mark(ev.Key)
		case d.Ledger.Seen(ev.Key):
		case d.Seed:
			d.Ledger.Mark(ev.Key)
		default:
			d.transition(ctx, &ev)
			if err := d.Notify(ev); err != nil {
				log.Printf("notify %s: %v", ev.Key, err)
				continue // не помечаем — повторится на следующем тике
			}
			d.Ledger.Mark(ev.Key)
		}
	}
}

// transition выполняет авто-перевод события и пишет итог в уведомление. После
// перевода кнопка «перевести» не нужна — Transition снимаем.
func (d Dispatcher) transition(ctx context.Context, ev *Event) {
	if ev.Transition == nil || d.AutoTransition == nil {
		return
	}
	if err := d.AutoTransition(ctx, ev.Transition.IssueID, ev.Transition.Command); err != nil {
		log.Printf("transition %s: %v", ev.Transition.IssueID, err)
		ev.Body = "Не удалось перевести автоматически: " + err.Error()
		return
	}
	ev.Title = "Переведена в " + domain.TransitionLabel(ev.Transition.Command)
	ev.Body = "Апрувы собраны"
	ev.Transition = nil
}
