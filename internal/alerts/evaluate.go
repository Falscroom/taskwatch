// Package alerts превращает снапшот в события и доставляет их уведомлениями.
// У каждого события стабильный Key — по нему Ledger гарантирует, что уведомление
// придёт один раз.
package alerts

import (
	"fmt"
	"strings"

	"taskwatch/internal/domain"
)

type Transition struct {
	IssueID string
	Command string
}

type Event struct {
	// Key — стабильный ключ события; уже отправленные лежат в state.json,
	// поэтому формат ключей менять нельзя.
	Key        string
	Title      string
	Message    string
	URL        string
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
			Key:     fmt.Sprintf("review:%d", mr.ID),
			Title:   "Нужно ревью",
			Message: mr.Title,
			URL:     mr.WebURL,
			MRID:    mr.ID,
		})
	}
	return events
}

func taskEvents(task *domain.Task, p domain.Policy) []Event {
	events := []Event{{
		Key:     fmt.Sprintf("yt-state:%s:%s", task.ID, task.State),
		Title:   task.ID,
		Message: "Статус: " + task.State,
		URL:     task.URL,
	}}

	for _, c := range task.Comments {
		if c.Author == p.YouTrackUser {
			continue
		}
		events = append(events, Event{
			Key:     "yt-comment:" + c.ID,
			Title:   fmt.Sprintf("Комментарий в %s от %s", task.ID, c.Author),
			Message: short(c.Text),
			URL:     task.URL,
		})
	}
	return events
}

func mrEvents(task *domain.Task, mr *domain.MR, p domain.Policy) []Event {
	var events []Event
	label := mr.Title
	if task != nil {
		label = task.ID
	}

	for _, n := range mr.Notes {
		if n.System || n.Author.Username == p.GitLabUser {
			continue
		}
		events = append(events, Event{
			Key:     fmt.Sprintf("gl-note:%d", n.ID),
			Title:   fmt.Sprintf("Комментарий в MR %s от %s", label, n.Author.Username),
			Message: short(n.Body),
			URL:     mr.WebURL,
		})
	}

	if pl := mr.Pipeline; pl.Failed() {
		events = append(events, Event{
			Key:     fmt.Sprintf("pipeline:%d:%s", pl.ID, pl.Status),
			Title:   "🔴 Сборка " + label,
			Message: string(pl.Status),
			URL:     pl.WebURL,
		})
	}

	if mr.Open() && mr.HasConflicts {
		events = append(events, Event{
			Key:     fmt.Sprintf("conflict:%d:%s", mr.ID, mr.SHA),
			Title:   "Конфликты в " + label,
			Message: mr.Title,
			URL:     mr.WebURL,
		})
	}

	if p.ReadyToTransition(task, mr) {
		e := Event{
			Key:     fmt.Sprintf("approved:%d", mr.ID),
			Title:   fmt.Sprintf("✅ %s: %d апрува", task.ID, mr.Approvals()),
			Message: "Пора переводить дальше",
			URL:     task.URL,
		}
		if p.CanTransition() {
			e.Transition = &Transition{IssueID: task.ID, Command: p.NextCommand}
		}
		events = append(events, e)
	}

	for _, b := range mr.ReleaseBranches {
		events = append(events, Event{
			Key:     fmt.Sprintf("release:%d:%s", mr.ID, b),
			Title:   fmt.Sprintf("🚀 %s в %s", label, b),
			Message: mr.Title,
			URL:     mr.WebURL,
		})
	}
	// Все события этого MR помечаем его id — по нему движок сверяет заглушения.
	for i := range events {
		events[i].MRID = mr.ID
	}
	return events
}

func short(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > 140 {
		return string(r[:140]) + "…"
	}
	return s
}
