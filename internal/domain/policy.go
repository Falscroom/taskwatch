package domain

import "strings"

// Policy — правила команды и то, кто здесь «я». Единственное место, где решается,
// что MR требует работы, готов к переводу или ждёт твоего ревью: уведомления и
// карточки окна спрашивают об этом её.
type Policy struct {
	GitLabUser   string // твой логин в GitLab
	YouTrackUser string // твой логин в YouTrack

	RequiredApprovals int
	// ReviewState — статус таски, в котором она ждёт апрувов (без учёта регистра).
	ReviewState string
	// NextCommand — команда YouTrack для перевода после апрувов; пусто — не переводим.
	NextCommand string
	// BehindThreshold — с какого отставания от целевой ветки MR считается
	// проблемным; 0 — отставание не проблема.
	BehindThreshold int
}

// ProblemKind — почему твой MR требует работы.
type ProblemKind int

const (
	ProblemPipelineFailed ProblemKind = iota + 1
	ProblemChangesRequested
	ProblemUnresolvedThreads
	ProblemConflicts
	ProblemBehind
)

type Problem struct {
	Kind   ProblemKind
	Behind int // для ProblemBehind — на сколько коммитов
}

// Problems — что мешает открытому MR, в порядке важности для показа.
func (p Policy) Problems(mr *MR) []Problem {
	var out []Problem
	if mr.Pipeline.Failed() {
		out = append(out, Problem{Kind: ProblemPipelineFailed})
	}
	if mr.ChangesRequested() {
		out = append(out, Problem{Kind: ProblemChangesRequested})
	}
	if !mr.DiscussionsResolved {
		out = append(out, Problem{Kind: ProblemUnresolvedThreads})
	}
	if mr.HasConflicts {
		out = append(out, Problem{Kind: ProblemConflicts})
	}
	if p.BehindThreshold > 0 && mr.Behind >= p.BehindThreshold {
		out = append(out, Problem{Kind: ProblemBehind, Behind: mr.Behind})
	}
	return out
}

// ReadyToTransition — апрувы собраны, таска в статусе ревью: пора переводить.
// Без таски переводить нечего.
func (p Policy) ReadyToTransition(task *Task, mr *MR) bool {
	return task != nil && mr.Open() && mr.Approvals() >= p.RequiredApprovals &&
		strings.EqualFold(task.State, p.ReviewState)
}

// CanTransition — для готовой таски задана команда перевода.
func (p Policy) CanTransition() bool { return p.NextCommand != "" }

// TransitionLabel — команда перевода для кнопки: "State Ready for QA" → "Ready for QA".
func TransitionLabel(command string) string {
	if rest, ok := strings.CutPrefix(command, "State "); ok {
		return rest
	}
	return command
}

// AwaitsMyReview — чужой MR ждёт твоего решения: ты его ещё не апрувнул и не
// запросил изменения.
func (p Policy) AwaitsMyReview(mr *MR) bool {
	return mr.DecisionOf(p.GitLabUser) == DecisionPending
}

// Stage — где MR с твоей точки зрения.
type Stage int

const (
	StageWaiting     Stage = iota // ждём других
	StageNeedsWork                // твой MR требует работы
	StageReady                    // апрувы собраны, пора переводить
	StageNeedsReview              // чужой MR ждёт твоего ревью
	StageMerged
)

// NeedsMe — стадия требует твоего действия.
func (s Stage) NeedsMe() bool {
	return s == StageNeedsWork || s == StageReady || s == StageNeedsReview
}

// OwnStage — стадия твоего MR; task — его таска или nil для сироты.
func (p Policy) OwnStage(task *Task, mr *MR) Stage {
	switch {
	case mr.Merged():
		return StageMerged
	case len(p.Problems(mr)) > 0:
		return StageNeedsWork
	case p.ReadyToTransition(task, mr):
		return StageReady
	default:
		return StageWaiting
	}
}

// ReviewStage — стадия чужого MR, где ты ревьюер.
func (p Policy) ReviewStage(mr *MR) Stage {
	if p.AwaitsMyReview(mr) {
		return StageNeedsReview
	}
	return StageWaiting
}
