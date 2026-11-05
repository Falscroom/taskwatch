// Package rules превращает снапшот в события. Каждое событие имеет стабильный Key —
// по нему state.Store гарантирует, что уведомление придёт один раз.
package rules

import (
	"fmt"
	"strings"

	"taskwatch/internal/config"
	"taskwatch/internal/watch"
)

type Transition struct {
	IssueID string
	Command string
}

type Event struct {
	Key        string
	Title      string
	Message    string
	URL        string
	Transition *Transition
	// MRID — MR, породивший событие; 0 у событий тасок YouTrack. Нужен для заглушений.
	MRID int
}

func Evaluate(snap *watch.Snapshot, cfg *config.Config) []Event {
	var events []Event

	for _, task := range snap.Tasks {
		events = append(events, taskEvents(task, cfg)...)
		for _, mr := range task.MRs {
			events = append(events, mrEvents(task, mr, cfg)...)
		}
	}
	for _, mr := range snap.Orphans {
		events = append(events, mrEvents(nil, mr, cfg)...)
	}

	for _, mr := range snap.Reviews {
		if mr.IsApprovedBy(cfg.GitLab.Username) {
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

func taskEvents(task *watch.Task, cfg *config.Config) []Event {
	events := []Event{{
		Key:     fmt.Sprintf("yt-state:%s:%s", task.Issue.ID, task.State),
		Title:   task.Issue.ID,
		Message: "Статус: " + task.State,
		URL:     task.URL,
	}}

	for _, c := range task.Comments {
		if c.Author.Login == cfg.YouTrack.Login {
			continue
		}
		events = append(events, Event{
			Key:     "yt-comment:" + c.ID,
			Title:   fmt.Sprintf("Комментарий в %s от %s", task.Issue.ID, c.Author.Login),
			Message: short(c.Text),
			URL:     task.URL,
		})
	}
	return events
}

func mrEvents(task *watch.Task, mr *watch.MR, cfg *config.Config) []Event {
	var events []Event
	label := mr.Title
	if task != nil {
		label = task.Issue.ID
	}

	for _, n := range mr.Notes {
		if n.System || n.Author.Username == cfg.GitLab.Username {
			continue
		}
		events = append(events, Event{
			Key:     fmt.Sprintf("gl-note:%d", n.ID),
			Title:   fmt.Sprintf("Комментарий в MR %s от %s", label, n.Author.Username),
			Message: short(n.Body),
			URL:     mr.WebURL,
		})
	}

	if p := mr.HeadPipeline; p != nil && (p.Status == "failed" || p.Status == "canceled") {
		events = append(events, Event{
			Key:     fmt.Sprintf("pipeline:%d:%s", p.ID, p.Status),
			Title:   "🔴 Сборка " + label,
			Message: p.Status,
			URL:     p.WebURL,
		})
	}

	if mr.State == "opened" && mr.HasConflicts {
		events = append(events, Event{
			Key:     fmt.Sprintf("conflict:%d:%s", mr.ID, mr.SHA),
			Title:   "Конфликты в " + label,
			Message: mr.Title,
			URL:     mr.WebURL,
		})
	}

	if task != nil && mr.State == "opened" && mr.Approvals >= cfg.Rules.RequiredApprovals &&
		strings.EqualFold(task.State, cfg.Rules.ReviewState) {
		e := Event{
			Key:     fmt.Sprintf("approved:%d", mr.ID),
			Title:   fmt.Sprintf("✅ %s: %d апрува", task.Issue.ID, mr.Approvals),
			Message: "Пора переводить дальше",
			URL:     task.URL,
		}
		if cfg.Rules.NextCommand != "" {
			e.Transition = &Transition{IssueID: task.Issue.ID, Command: cfg.Rules.NextCommand}
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
