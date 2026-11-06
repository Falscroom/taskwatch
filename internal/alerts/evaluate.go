// Package alerts превращает снапшот в события и доставляет их уведомлениями.
// У каждого события стабильный Key — по нему Ledger гарантирует, что уведомление
// придёт один раз.
package alerts

import (
	"fmt"
	"strconv"
	"strings"

	"taskwatch/internal/domain"
)

type Transition struct {
	IssueID string
	Command string
}

// Kind — что случилось; от него зависят звук и кнопки уведомления.
type Kind int

const (
	KindReview           Kind = iota // тебя позвали на ревью
	KindPipeline                     // сборка упала
	KindConflict                     // конфликт с целевой веткой
	KindApproved                     // апрувы собраны
	KindComment                      // новый комментарий
	KindTaskState                    // таска сменила статус
	KindRelease                      // MR попал в релизную ветку
	KindReleaseFailed                // сборка релиза с твоим MR упала
	KindReleaseBuilt                 // релиз с твоим MR собрался
	KindChangesRequested             // в твоём MR запросили изменения
	KindThreads                      // в твоём MR появились нерешённые треды
	KindPollFailed                   // опрос не может обновить данные
	KindPollRecovered                // опрос снова работает
)

// Loud — событие требует действия, уведомлению положен звук; остальные приходят тихо.
func (k Kind) Loud() bool {
	switch k {
	case KindReview, KindPipeline, KindConflict, KindApproved, KindReleaseFailed, KindChangesRequested, KindPollFailed:
		return true
	}
	return false
}

type Event struct {
	// Key — стабильный ключ события; уже отправленные лежат в state.json,
	// поэтому формат ключей менять нельзя.
	Key  string
	Kind Kind
	// Title — что случилось, Subtitle — с чем (таска или MR), Body — подробности.
	Title    string
	Subtitle string
	Body     string
	URL      string
	// Thread — события одной таски или MR: Центр уведомлений складывает их в стопку.
	Thread     string
	Transition *Transition
	// MRID — MR, породивший событие; 0 у событий тасок YouTrack. Нужен для заглушений.
	MRID int
}

func Evaluate(snap *domain.Snapshot, p domain.Policy) []Event {
	var events []Event

	for _, task := range snap.Tasks {
		events = append(events, taskEvents(task, p)...)
		for _, mr := range task.MRs {
			events = append(events, mrEvents(task, mr, p)...)
		}
	}
	for _, mr := range snap.Orphans {
		events = append(events, mrEvents(nil, mr, p)...)
	}

	for _, mr := range snap.Reviews {
		if !p.AwaitsMyReview(mr) {
			continue
		}
		events = append(events, Event{
			Key:      fmt.Sprintf("review:%d", mr.ID),
			Kind:     KindReview,
			Title:    "Нужно твоё ревью",
			Subtitle: subject(nil, mr),
			Body:     displayName(mr.Author),
			URL:      mr.WebURL,
			Thread:   mrThread(mr),
			MRID:     mr.ID,
		})
	}
	return events
}

func taskEvents(task *domain.Task, p domain.Policy) []Event {
	thread := "task:" + task.ID
	events := []Event{{
		Key:      fmt.Sprintf("yt-state:%s:%s", task.ID, task.State),
		Kind:     KindTaskState,
		Title:    "Статус: " + task.State,
		Subtitle: subject(task, nil),
		URL:      task.URL,
		Thread:   thread,
	}}

	for _, c := range task.Comments {
		if c.Author == p.YouTrackUser {
			continue
		}
		events = append(events, Event{
			Key:      "yt-comment:" + c.ID,
			Kind:     KindComment,
			Title:    "Комментарий в таске",
			Subtitle: subject(task, nil),
			Body:     c.Author + ": " + short(c.Text),
			URL:      task.URL,
			Thread:   thread,
		})
	}
	return events
}

func mrEvents(task *domain.Task, mr *domain.MR, p domain.Policy) []Event {
	var events []Event
	about := subject(task, mr)

	for _, n := range mr.Notes {
		if n.System || n.Author.Username == p.GitLabUser {
			continue
		}
		events = append(events, Event{
			Key:      fmt.Sprintf("gl-note:%d", n.ID),
			Kind:     KindComment,
			Title:    "Комментарий в MR",
			Subtitle: about,
			Body:     displayName(n.Author) + ": " + short(n.Body),
			URL:      mr.WebURL,
		})
	}

	if pl := mr.Pipeline; pl.Failed() {
		title := "Сборка упала"
		if pl.Status == "canceled" {
			title = "Сборка отменена"
		}
		events = append(events, Event{
			Key:      fmt.Sprintf("pipeline:%d:%s", pl.ID, pl.Status),
			Kind:     KindPipeline,
			Title:    title,
			Subtitle: about,
			Body:     "Ветка " + mr.SourceBranch,
			URL:      pl.WebURL,
		})
	}

	if mr.Open() {
		for _, r := range mr.Reviewers {
			if mr.DecisionOf(r.User.Username) == domain.DecisionChanges {
				events = append(events, Event{
					Key:      fmt.Sprintf("changes:%d:%s", mr.ID, r.User.Username),
					Kind:     KindChangesRequested,
					Title:    "Запрошены изменения",
					Subtitle: about,
					Body:     displayName(r.User),
					URL:      mr.WebURL,
				})
			}
		}
		// Один раз на MR: каждый новый тред и так приходит комментарием.
		if !mr.DiscussionsResolved {
			events = append(events, Event{
				Key:      fmt.Sprintf("threads:%d", mr.ID),
				Kind:     KindThreads,
				Title:    "Нерешённые треды",
				Subtitle: about,
				Body:     "Перед мержем их нужно закрыть",
				URL:      mr.WebURL,
			})
		}
	}

	if mr.Open() && mr.HasConflicts {
		e := Event{
			Key:      fmt.Sprintf("conflict:%d:%s", mr.ID, mr.SHA),
			Kind:     KindConflict,
			Title:    "Конфликт в MR",
			Subtitle: about,
			Body:     "Нужен rebase",
			URL:      mr.WebURL,
		}
		if mr.TargetBranch != "" {
			e.Title = "Конфликт с " + mr.TargetBranch
			e.Body = "Нужен rebase или merge " + mr.TargetBranch
		}
		events = append(events, e)
	}

	if p.ReadyToTransition(task, mr) {
		e := Event{
			Key:      fmt.Sprintf("approved:%d", mr.ID),
			Kind:     KindApproved,
			Title:    fmt.Sprintf("Апрувы собраны · %d из %d", mr.Approvals(), p.RequiredApprovals),
			Subtitle: about,
			Body:     approverNames(mr) + ". Пора переводить",
			URL:      task.URL,
		}
		if p.CanTransition() {
			e.Body += " в " + domain.TransitionLabel(p.NextCommand)
			e.Transition = &Transition{IssueID: task.ID, Command: p.NextCommand}
		}
		events = append(events, e)
	}

	for _, rel := range mr.Releases {
		events = append(events, Event{
			Key:      fmt.Sprintf("release:%d:%s", mr.ID, rel.Branch),
			Kind:     KindRelease,
			Title:    "Попало в " + rel.Branch,
			Subtitle: about,
			URL:      mr.WebURL,
		})
		if e, ok := releaseBuildEvent(mr, rel, about); ok {
			events = append(events, e)
		}
	}
	// Все события этого MR помечаем его id — по нему движок сверяет заглушения —
	// и складываем в одну стопку.
	for i := range events {
		events[i].MRID = mr.ID
		events[i].Thread = mrThread(mr)
	}
	return events
}

// releaseBuildEvent — итог сборки релиза, в которой уже есть твой MR: упала —
// громко, собралась или дошла до ручного шага — тихо, пока идёт — молчим.
// Ключ с номером и статусом пайплайна: новая сборка — новое уведомление.
func releaseBuildEvent(mr *domain.MR, rel domain.Release, about string) (Event, bool) {
	b := rel.Build
	if b == nil {
		return Event{}, false
	}
	e := Event{
		Key:      fmt.Sprintf("release-build:%d:%d:%s", mr.ID, b.ID, b.Status),
		Subtitle: about,
		Body:     fmt.Sprintf("%s · пайплайн #%d", rel.Branch, b.ID),
		URL:      b.WebURL,
	}
	switch {
	case b.Status == "canceled":
		e.Kind, e.Title = KindReleaseFailed, "Сборка релиза отменена"
	case b.Failed():
		e.Kind, e.Title = KindReleaseFailed, "Сборка релиза упала"
	case b.Status == "success":
		e.Kind, e.Title = KindReleaseBuilt, "Релиз собрался"
	case b.Status == "manual":
		e.Kind, e.Title = KindReleaseBuilt, "Релиз собрался"
		e.Body += " · ждёт ручного шага"
	default:
		return Event{}, false
	}
	return e, true
}

// subject — с чем событие: таска, а если её нет — MR.
func subject(task *domain.Task, mr *domain.MR) string {
	if task != nil {
		return task.ID + " · " + task.Summary
	}
	return "!" + strconv.Itoa(mr.IID) + " · " + mr.Title
}

func mrThread(mr *domain.MR) string { return domain.MRKey(mr.ID) }

func displayName(u domain.User) string {
	if u.Name != "" {
		return u.Name
	}
	return u.Username
}

// approverNames — кто апрувнул; имена берём у ревьюеров, где они есть.
func approverNames(mr *domain.MR) string {
	names := make([]string, 0, len(mr.ApprovedBy))
	for _, login := range mr.ApprovedBy {
		name := login
		for _, r := range mr.Reviewers {
			if r.User.Username == login && r.User.Name != "" {
				name = r.User.Name
			}
		}
		names = append(names, name)
	}
	return strings.Join(names, ", ")
}

func short(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > 140 {
		return string(r[:140]) + "…"
	}
	return s
}
