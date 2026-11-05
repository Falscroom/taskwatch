// Package domain — модель предметной области: таски, merge request'ы, снапшот и
// правила, по которым MR «требует работы», «готов к переводу» или «ждёт твоего
// ревью». Ничего не импортирует: интеграции переводят в неё свои форматы, а
// правила уведомлений, карточки окна и CLI пользуются ею.
package domain

import (
	"slices"
	"time"
)

// MRState — состояние MR в GitLab.
type MRState string

const (
	MROpened MRState = "opened"
	MRMerged MRState = "merged"
	MRClosed MRState = "closed"
	MRLocked MRState = "locked"
)

// ReviewerState — что ревьюер сделал с MR, как его отдаёт GitLab.
type ReviewerState string

const (
	ReviewerUnreviewed       ReviewerState = "unreviewed"
	ReviewerReviewed         ReviewerState = "reviewed"
	ReviewerRequestedChanges ReviewerState = "requested_changes"
	ReviewerApproved         ReviewerState = "approved"
	ReviewerUnapproved       ReviewerState = "unapproved"
)

// ReviewDecision — итоговое решение человека по MR.
type ReviewDecision int

const (
	DecisionPending  ReviewDecision = iota // ещё не решил (или не ревьюер)
	DecisionApproved                       // апрувнул
	DecisionChanges                        // запросил изменения
)

type User struct {
	Username string
	Name     string
}

type Reviewer struct {
	User  User
	State ReviewerState
}

// Note — комментарий в MR. System — служебная запись GitLab («approved this»).
type Note struct {
	ID     int
	Author User
	Body   string
	System bool
}

type MR struct {
	ID        int // глобальный id GitLab — по нему ключи событий и заглушений
	IID       int // номер внутри проекта: !IID
	ProjectID int
	Title     string
	WebURL    string
	// SourceBranch — ветка MR; по ней (или по заголовку) MR находит свою таску.
	SourceBranch string
	State        MRState
	// SHA — головной коммит; меняется с каждым пушем.
	SHA       string
	CreatedAt time.Time
	Draft     bool
	Author    User

	HasConflicts bool
	// DiscussionsResolved — нет нерешённых тредов, блокирующих мерж.
	DiscussionsResolved bool
	// Behind — на сколько коммитов MR отстал от целевой ветки.
	Behind int
	// Comments — сколько всего пользовательских комментариев.
	Comments int
	Pipeline *Pipeline

	ApprovedBy []string
	Reviewers  []Reviewer
	// Notes — последние комментарии; у смёрженного MR не загружаются.
	Notes []Note
	// ReleaseBranches — релизные ветки, в которые попал смёрженный MR.
	ReleaseBranches []string
}

func (mr *MR) Open() bool   { return mr.State == MROpened }
func (mr *MR) Merged() bool { return mr.State == MRMerged }

// Approvals — сколько апрувов собрано.
func (mr *MR) Approvals() int { return len(mr.ApprovedBy) }

// IsApprovedBy сообщает, апрувнул ли пользователь MR.
func (mr *MR) IsApprovedBy(username string) bool { return slices.Contains(mr.ApprovedBy, username) }

// DecisionOf — решение пользователя по MR. Апрув считается и без назначения
// ревьюером: апрувнуть может любой.
func (mr *MR) DecisionOf(username string) ReviewDecision {
	if mr.IsApprovedBy(username) {
		return DecisionApproved
	}
	for _, r := range mr.Reviewers {
		if r.User.Username != username {
			continue
		}
		switch r.State {
		case ReviewerApproved:
			return DecisionApproved
		case ReviewerRequestedChanges:
			return DecisionChanges
		}
	}
	return DecisionPending
}

// ChangesRequested — хоть один ревьюер запросил изменения.
func (mr *MR) ChangesRequested() bool {
	for _, r := range mr.Reviewers {
		if mr.DecisionOf(r.User.Username) == DecisionChanges {
			return true
		}
	}
	return false
}
