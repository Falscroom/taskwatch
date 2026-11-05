// Package view раскладывает снапшот по карточкам и группам для окна в меню-баре.
package view

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	"taskwatch/internal/config"
	"taskwatch/internal/state"
	"taskwatch/internal/watch"
)

// Группы карточек. Первые три — «нужно твоё действие».
const (
	GroupWork    = "work"    // твой MR требует работы
	GroupReady   = "ready"   // апрувы собраны, пора переводить
	GroupReview  = "review"  // ждёт твоего ревью
	GroupWaiting = "waiting" // ждём других
	GroupMerged  = "merged"  // смёржено
	GroupNoMR    = "nomr"    // таска в спринте без MR
	GroupMuted   = "muted"   // заглушено пользователем — видно только во вкладке «Все»
)

// Решение ревьюера — цвет кольца вокруг аватара.
const (
	DecisionApproved = "approved"
	DecisionChanges  = "changes"
	DecisionPending  = "pending"
)

type Person struct {
	Username string `json:"username"`
	Name     string `json:"name"`
	Initials string `json:"initials"`
	Decision string `json:"decision,omitempty"`
}

type Badge struct {
	Text string `json:"text"`
	Tone string `json:"tone"` // red, yellow, green, grey
}

type Action struct {
	Label   string `json:"label"`
	IssueID string `json:"issueId"`
}

type Card struct {
	Key         string   `json:"key"`
	Group       string   `json:"group"`
	Mine        bool     `json:"mine"`
	Review      bool     `json:"review"`
	Title       string   `json:"title"`
	URL         string   `json:"url"`
	IssueID     string   `json:"issueId,omitempty"`
	IssueURL    string   `json:"issueUrl,omitempty"`
	IssueState  string   `json:"issueState,omitempty"`
	Author      *Person  `json:"author,omitempty"`
	Reviewers   []Person `json:"reviewers"`
	Pipeline    string   `json:"pipeline,omitempty"` // green, yellow, red
	PipelineURL string   `json:"pipelineUrl,omitempty"`
	Behind      int      `json:"behind"`
	Comments    int      `json:"comments"`
	Release     []string `json:"release"`
	Badges      []Badge  `json:"badges"`
	Action      *Action  `json:"action,omitempty"`
	// Wait — сколько времени открыт MR, напр. "2д 5ч"; только для ждущих групп
	Wait string `json:"wait,omitempty"`
	// Muted — карточка заглушена; MutedUntil — до когда (RFC3339), пусто — навсегда.
	Muted      bool   `json:"muted,omitempty"`
	MutedUntil string `json:"mutedUntil,omitempty"`

	waitHours int // для сортировки, наружу не идёт
}

type View struct {
	UpdatedAt time.Time `json:"updatedAt"`
	Error     string    `json:"error,omitempty"`
	NeedsYou  int       `json:"needsYou"`
	Cards     []Card    `json:"cards"`
}

func Build(snap *watch.Snapshot, updated time.Time, err error, cfg *config.Config, mutes map[string]time.Time) View {
	v := View{UpdatedAt: updated, Cards: []Card{}}
	if err != nil {
		v.Error = err.Error()
	}
	if snap == nil {
		return v
	}

	for _, task := range snap.Tasks {
		if len(task.MRs) == 0 {
			v.Cards = append(v.Cards, Card{
				Key:        "task:" + task.Issue.ID,
				Group:      GroupNoMR,
				Mine:       true,
				Title:      task.Issue.Summary,
				URL:        task.URL,
				IssueID:    task.Issue.ID,
				IssueURL:   task.URL,
				IssueState: task.State,
				Reviewers:  []Person{},
				Release:    []string{},
				Badges:     []Badge{},
			})
			continue
		}
		for _, mr := range task.MRs {
			v.Cards = append(v.Cards, mineCard(task, mr, cfg))
		}
	}
	for _, mr := range snap.Orphans {
		v.Cards = append(v.Cards, mineCard(nil, mr, cfg))
	}
	for _, mr := range snap.Reviews {
		v.Cards = append(v.Cards, reviewCard(mr, cfg))
	}

	// Заглушенные MR уходят в отдельную группу: скрыты от рабочих вкладок,
	// но остаются видны во вкладке «Все», откуда их можно вернуть.
	// Глушат и личный ключ "mr:<id>", и правило по названию "title:<текст>".
	now := time.Now()
	for i := range v.Cards {
		c := &v.Cards[i]
		until, ok := mutes[c.Key]
		if !ok && strings.HasPrefix(c.Key, "mr:") {
			until, ok = mutes["title:"+c.Title]
		}
		if !ok || !state.Active(until, now) {
			continue
		}
		c.Muted = true
		c.Group = GroupMuted
		if !until.IsZero() {
			c.MutedUntil = until.Format(time.RFC3339)
		}
	}

	for _, c := range v.Cards {
		if c.Group == GroupWork || c.Group == GroupReady || c.Group == GroupReview {
			v.NeedsYou++
		}
	}

	// Внутри групп карточки с большим сроком ожидания поднимаются выше.
	// Для карточек из разных групп компаратор всегда false — стабильная сортировка
	// сохраняет исходный порядок между группами.
	sort.SliceStable(v.Cards, func(i, j int) bool {
		ci, cj := v.Cards[i], v.Cards[j]
		return ci.Group == cj.Group && ci.waitHours > cj.waitHours
	})
	return v
}

func mineCard(task *watch.Task, mr *watch.MR, cfg *config.Config) Card {
	c := baseCard(mr)
	c.Mine = true
	if task != nil {
		c.IssueID = task.Issue.ID
		c.IssueURL = task.URL
		c.IssueState = task.State
	}

	if mr.State == "merged" {
		c.Group = GroupMerged
		return c
	}

	if c.Pipeline == "red" {
		c.Badges = append(c.Badges, Badge{"Сборка упала", "red"})
	}
	for _, r := range c.Reviewers {
		if r.Decision == DecisionChanges {
			c.Badges = append(c.Badges, Badge{"Изменения запрошены", "red"})
			break
		}
	}
	if !mr.BlockingDiscussionsResolved {
		c.Badges = append(c.Badges, Badge{"Нерешённые треды", "yellow"})
	}
	if mr.HasConflicts {
		c.Badges = append(c.Badges, Badge{"Конфликты", "red"})
	}
	// Отставание от target — настройка: с какого порога считать MR проблемой.
	if n := cfg.Rules.MRBehindCommits; n > 0 && mr.DivergedCommitsCount >= n {
		c.Badges = append(c.Badges, Badge{"Отстаёт на " + pluralCommits(mr.DivergedCommitsCount), "yellow"})
	}

	switch {
	case len(c.Badges) > 0:
		c.Group = GroupWork
	case task != nil && mr.Approvals >= cfg.Rules.RequiredApprovals &&
		strings.EqualFold(task.State, cfg.Rules.ReviewState):
		c.Group = GroupReady
		if cfg.Rules.NextCommand != "" {
			c.Action = &Action{Label: actionLabel(cfg.Rules.NextCommand), IssueID: task.Issue.ID}
		}
	default:
		c.Group = GroupWaiting
		c.Wait, c.waitHours = wait(mr)
	}
	return c
}

func reviewCard(mr *watch.MR, cfg *config.Config) Card {
	c := baseCard(mr)
	c.Review = true
	c.Group = GroupReview
	c.Wait, c.waitHours = wait(mr)
	for _, r := range c.Reviewers {
		if r.Username == cfg.GitLab.Username && r.Decision != DecisionPending {
			c.Group = GroupWaiting
		}
	}
	return c
}

// pluralCommits — «1 коммит / 2 коммита / 17 коммитов» для бейджа отставания.
func pluralCommits(n int) string {
	switch {
	case n%10 == 1 && n%100 != 11:
		return strconv.Itoa(n) + " коммит"
	case n%10 >= 2 && n%10 <= 4 && (n%100 < 12 || n%100 > 14):
		return strconv.Itoa(n) + " коммита"
	default:
		return strconv.Itoa(n) + " коммитов"
	}
}

func baseCard(mr *watch.MR) Card {
	c := Card{
		Key:       "mr:" + strconv.Itoa(mr.ID),
		Title:     mr.Title,
		URL:       mr.WebURL,
		Author:    person(mr.Author.Username, mr.Author.Name),
		Reviewers: reviewers(mr),
		Behind:    mr.DivergedCommitsCount,
		Comments:  mr.UserNotesCount,
		Release:   append([]string{}, mr.ReleaseBranches...),
		Badges:    []Badge{},
	}
	if p := mr.HeadPipeline; p != nil {
		c.Pipeline = pipelineTone(p.Status)
		c.PipelineURL = p.WebURL
	}
	return c
}

func reviewers(mr *watch.MR) []Person {
	out := []Person{}
	seen := map[string]bool{}
	for _, r := range mr.Reviewers {
		p := person(r.User.Username, r.User.Name)
		switch {
		case mr.IsApprovedBy(r.User.Username) || r.State == "approved":
			p.Decision = DecisionApproved
		case r.State == "requested_changes":
			p.Decision = DecisionChanges
		default:
			p.Decision = DecisionPending
		}
		out = append(out, *p)
		seen[r.User.Username] = true
	}
	// Апрувнуть может и не назначенный ревьюер
	for _, u := range mr.ApprovedBy {
		if !seen[u] {
			p := person(u, "")
			p.Decision = DecisionApproved
			out = append(out, *p)
		}
	}
	return out
}

func person(username, name string) *Person {
	return &Person{Username: username, Name: name, Initials: initials(username, name)}
}

func initials(username, name string) string {
	var out []rune
	for _, w := range strings.Fields(name) {
		out = append(out, unicode.ToUpper([]rune(w)[0]))
		if len(out) == 2 {
			return string(out)
		}
	}
	if len(out) > 0 {
		return string(out)
	}
	r := []rune(strings.ToUpper(username))
	if len(r) > 2 {
		r = r[:2]
	}
	return string(r)
}

// wait — сколько времени открыт MR в формате "2д 5ч"; пусто, если он не открыт
// или открыт меньше часа — такое молодое ожидание ещё не заслуживает бейджа.
func wait(mr *watch.MR) (string, int) {
	if mr.State != "opened" || mr.CreatedAt.IsZero() {
		return "", 0
	}
	h := int(time.Since(mr.CreatedAt).Hours())
	switch {
	case h >= 24:
		return fmt.Sprintf("%dд %dч", h/24, h%24), h
	case h >= 1:
		return fmt.Sprintf("%dч", h), h
	}
	return "", 0
}

func pipelineTone(status string) string {
	switch status {
	case "success":
		return "green"
	case "failed", "canceled":
		return "red"
	case "skipped", "":
		return ""
	default:
		return "yellow"
	}
}

// actionLabel: "State Ready for QA" → "Ready for QA"
func actionLabel(command string) string {
	if rest, ok := strings.CutPrefix(command, "State "); ok {
		return rest
	}
	return command
}
