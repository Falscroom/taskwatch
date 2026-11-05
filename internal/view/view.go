// Package view раскладывает доменный снапшот по карточкам и группам для окна в
// меню-баре. Бизнес-решения («требует работы», «готов к переводу») принимает
// domain.Policy; здесь — только как это показать.
package view

import (
	"cmp"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"

	"taskwatch/internal/domain"
)

// Группы карточек — это контракт с фронтендом.
const (
	GroupWork    = "work"    // твой MR требует работы
	GroupReady   = "ready"   // апрувы собраны, пора переводить
	GroupReview  = "review"  // ждёт твоего ревью
	GroupWaiting = "waiting" // ждём других
	GroupMerged  = "merged"  // смёржено
	GroupNoMR    = "nomr"    // таска в спринте без MR
	GroupMuted   = "muted"   // заглушено пользователем — видно только во вкладке «Все»
)

// groupOrder — порядок групп в списке карточек.
var groupOrder = []string{GroupWork, GroupReady, GroupReview, GroupWaiting, GroupNoMR, GroupMerged, GroupMuted}

var stageGroup = map[domain.Stage]string{
	domain.StageNeedsWork:   GroupWork,
	domain.StageReady:       GroupReady,
	domain.StageNeedsReview: GroupReview,
	domain.StageWaiting:     GroupWaiting,
	domain.StageMerged:      GroupMerged,
}

// Решение ревьюера — цвет кольца вокруг аватара.
const (
	DecisionApproved = "approved"
	DecisionChanges  = "changes"
	DecisionPending  = "pending"
)

// Тона бейджей и статуса пайплайна.
const (
	ToneRed    = "red"
	ToneYellow = "yellow"
	ToneGreen  = "green"
)

type Person struct {
	Username string `json:"username"`
	Name     string `json:"name"`
	Initials string `json:"initials"`
	Decision string `json:"decision,omitempty"`
}

type Badge struct {
	Text string `json:"text"`
	Tone string `json:"tone"`
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

	waitHours int  // для сортировки, наружу не идёт
	needsMe   bool // требует твоего действия и не заглушена
}

type View struct {
	UpdatedAt time.Time `json:"updatedAt"`
	Error     string    `json:"error,omitempty"`
	NeedsYou  int       `json:"needsYou"`
	Cards     []Card    `json:"cards"`
}

func Build(snap *domain.Snapshot, updated time.Time, err error, p domain.Policy, mutes domain.MuteRules) View {
	v := View{UpdatedAt: updated, Cards: []Card{}}
	if err != nil {
		v.Error = err.Error()
	}
	if snap == nil {
		return v
	}

	b := builder{policy: p, mutes: mutes, now: time.Now()}
	for _, task := range snap.Tasks {
		if len(task.MRs) == 0 {
			v.Cards = append(v.Cards, taskCard(task))
			continue
		}
		for _, mr := range task.MRs {
			v.Cards = append(v.Cards, b.own(task, mr))
		}
	}
	for _, mr := range snap.Orphans {
		v.Cards = append(v.Cards, b.own(nil, mr))
	}
	for _, mr := range snap.Reviews {
		v.Cards = append(v.Cards, b.review(mr))
	}

	for _, c := range v.Cards {
		if c.needsMe {
			v.NeedsYou++
		}
	}
	// Группы по порядку, внутри группы дольше ждущие — выше.
	slices.SortStableFunc(v.Cards, func(a, b Card) int {
		if a.Group != b.Group {
			return cmp.Compare(slices.Index(groupOrder, a.Group), slices.Index(groupOrder, b.Group))
		}
		return cmp.Compare(b.waitHours, a.waitHours)
	})
	return v
}

type builder struct {
	policy domain.Policy
	mutes  domain.MuteRules
	now    time.Time
}

func taskCard(task *domain.Task) Card {
	return Card{
		Key:        "task:" + task.ID,
		Group:      GroupNoMR,
		Mine:       true,
		Title:      task.Summary,
		URL:        task.URL,
		IssueID:    task.ID,
		IssueURL:   task.URL,
		IssueState: task.State,
		Reviewers:  []Person{},
		Release:    []string{},
		Badges:     []Badge{},
	}
}

// own — карточка твоего MR; task — его таска или nil для сироты.
func (b builder) own(task *domain.Task, mr *domain.MR) Card {
	c := baseCard(mr)
	c.Mine = true
	if task != nil {
		c.IssueID = task.ID
		c.IssueURL = task.URL
		c.IssueState = task.State
	}

	stage := b.policy.OwnStage(task, mr)
	switch stage {
	case domain.StageNeedsWork:
		for _, pr := range b.policy.Problems(mr) {
			c.Badges = append(c.Badges, problemBadge(pr))
		}
	case domain.StageReady:
		if b.policy.CanTransition() {
			c.Action = &Action{Label: actionLabel(b.policy.NextCommand), IssueID: task.ID}
		}
	case domain.StageWaiting:
		c.Wait, c.waitHours = wait(mr, b.now)
	}
	b.place(&c, mr, stage)
	return c
}

// review — карточка чужого MR, где ты ревьюер.
func (b builder) review(mr *domain.MR) Card {
	c := baseCard(mr)
	c.Review = true
	c.Wait, c.waitHours = wait(mr, b.now)
	b.place(&c, mr, b.policy.ReviewStage(mr))
	return c
}

// place кладёт карточку в группу её стадии, а заглушенную — в «Заглушено»:
// она скрыта от рабочих вкладок, но видна во вкладке «Все», откуда её можно вернуть.
func (b builder) place(c *Card, mr *domain.MR, stage domain.Stage) {
	until, muted := b.mutes.Match(mr, b.now)
	if !muted {
		c.Group = stageGroup[stage]
		c.needsMe = stage.NeedsMe()
		return
	}
	c.Group = GroupMuted
	c.Muted = true
	if !until.IsZero() {
		c.MutedUntil = until.Format(time.RFC3339)
	}
}

func baseCard(mr *domain.MR) Card {
	c := Card{
		Key:       domain.MRKey(mr.ID),
		Title:     mr.Title,
		URL:       mr.WebURL,
		Author:    person(mr.Author),
		Reviewers: reviewers(mr),
		Behind:    mr.Behind,
		Comments:  mr.Comments,
		Release:   append([]string{}, mr.ReleaseBranches...),
		Badges:    []Badge{},
		Pipeline:  pipelineTone(mr.Pipeline),
	}
	if mr.Pipeline != nil {
		c.PipelineURL = mr.Pipeline.WebURL
	}
	return c
}

func problemBadge(pr domain.Problem) Badge {
	switch pr.Kind {
	case domain.ProblemPipelineFailed:
		return Badge{"Сборка упала", ToneRed}
	case domain.ProblemChangesRequested:
		return Badge{"Изменения запрошены", ToneRed}
	case domain.ProblemUnresolvedThreads:
		return Badge{"Нерешённые треды", ToneYellow}
	case domain.ProblemConflicts:
		return Badge{"Конфликты", ToneRed}
	case domain.ProblemBehind:
		return Badge{"Отстаёт на " + pluralCommits(pr.Behind), ToneYellow}
	}
	return Badge{"Проблема", ToneYellow}
}

func reviewers(mr *domain.MR) []Person {
	out := []Person{}
	seen := map[string]bool{}
	for _, r := range mr.Reviewers {
		p := person(r.User)
		p.Decision = decisionName(mr.DecisionOf(r.User.Username))
		out = append(out, *p)
		seen[r.User.Username] = true
	}
	// Апрувнуть может и не назначенный ревьюер
	for _, u := range mr.ApprovedBy {
		if !seen[u] {
			p := person(domain.User{Username: u})
			p.Decision = DecisionApproved
			out = append(out, *p)
		}
	}
	return out
}

func person(u domain.User) *Person {
	return &Person{Username: u.Username, Name: u.Name, Initials: initials(u.Username, u.Name)}
}

func decisionName(d domain.ReviewDecision) string {
	switch d {
	case domain.DecisionApproved:
		return DecisionApproved
	case domain.DecisionChanges:
		return DecisionChanges
	default:
		return DecisionPending
	}
}

func pipelineTone(p *domain.Pipeline) string {
	switch p.Outcome() {
	case domain.OutcomeSuccess:
		return ToneGreen
	case domain.OutcomeFailed:
		return ToneRed
	case domain.OutcomeRunning:
		return ToneYellow
	default:
		return ""
	}
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
func wait(mr *domain.MR, now time.Time) (string, int) {
	if !mr.Open() || mr.CreatedAt.IsZero() {
		return "", 0
	}
	h := int(now.Sub(mr.CreatedAt).Hours())
	switch {
	case h >= 24:
		return fmt.Sprintf("%dд %dч", h/24, h%24), h
	case h >= 1:
		return fmt.Sprintf("%dч", h), h
	}
	return "", 0
}

// actionLabel: "State Ready for QA" → "Ready for QA"
func actionLabel(command string) string {
	if rest, ok := strings.CutPrefix(command, "State "); ok {
		return rest
	}
	return command
}
