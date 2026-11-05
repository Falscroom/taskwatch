// Package watch собирает таски из YouTrack и связанные MR из GitLab в один доменный снапшот.
package watch

import (
	"context"
	"fmt"
	"strings"
	"time"

	"taskwatch/internal/config"
	"taskwatch/internal/domain"
	"taskwatch/internal/gitlab"
	"taskwatch/internal/youtrack"
)

type Collector struct {
	cfg *config.Config
	gl  *gitlab.Client
	yt  *youtrack.Client
	// byIssue — таски последнего прохода; нужен, только если настроен YouTrack
	byIssue map[string]*domain.Task
}

func NewCollector(cfg *config.Config, gl *gitlab.Client, yt *youtrack.Client) *Collector {
	return &Collector{cfg: cfg, gl: gl, yt: yt}
}

func (c *Collector) Collect(ctx context.Context) (*domain.Snapshot, error) {
	snap := &domain.Snapshot{}

	// Сервисы независимы: опрашиваем только настроенные, остальное потом.
	if c.cfg.YouTrackOK() {
		if err := c.collectIssues(ctx, snap); err != nil {
			return nil, err
		}
	}
	if c.cfg.GitLabOK() {
		if err := c.collectMRs(ctx, snap); err != nil {
			return nil, err
		}
	}
	return snap, nil
}

func (c *Collector) collectIssues(ctx context.Context, snap *domain.Snapshot) error {
	issues, err := c.yt.Issues(ctx, c.cfg.YouTrack.Query)
	if err != nil {
		return err
	}
	byID := make(map[string]*domain.Task, len(issues))
	for _, issue := range issues {
		comments, err := c.yt.Comments(ctx, issue.ID)
		if err != nil {
			return err
		}
		task := toTask(issue, c.yt.IssueURL(issue.ID), comments)
		snap.Tasks = append(snap.Tasks, task)
		byID[issue.ID] = task
	}
	c.byIssue = byID
	return nil
}

func (c *Collector) collectMRs(ctx context.Context, snap *domain.Snapshot) error {
	since := time.Now().Add(-c.cfg.Rules.MRLookback)
	mrs, err := c.gl.MyMergeRequests(ctx, c.cfg.GitLab.Username, since)
	if err != nil {
		return err
	}
	for _, item := range mrs {
		if domain.MRState(item.State) == domain.MRClosed || c.skipDraft(item) {
			continue
		}
		mr, err := c.loadMR(ctx, item)
		if err != nil {
			return err
		}
		if task, ok := c.byIssue[domain.IssueKey(item.SourceBranch, item.Title)]; ok {
			task.MRs = append(task.MRs, mr)
		} else {
			snap.Orphans = append(snap.Orphans, mr)
		}
	}

	reviews, err := c.gl.ReviewRequests(ctx, c.cfg.GitLab.Username)
	if err != nil {
		return err
	}
	for _, item := range reviews {
		if c.skipDraft(item) {
			continue
		}
		mr, err := c.loadMR(ctx, item)
		if err != nil {
			return err
		}
		snap.Reviews = append(snap.Reviews, mr)
	}
	return nil
}

// skipDraft — черновики не показываем и не уведомляем, если включено в настройках.
// Убирают draft-флажок — MR появится как новый и придёт уведомление «Нужно ревью».
func (c *Collector) skipDraft(item gitlab.MergeRequest) bool {
	return c.cfg.Rules.ExcludeDraft && item.Draft
}

func (c *Collector) loadMR(ctx context.Context, item gitlab.MergeRequest) (*domain.MR, error) {
	full, err := c.gl.MergeRequest(ctx, item.ProjectID, item.IID)
	if err != nil {
		return nil, err
	}
	mr := toMR(full)

	// Смёрженный уже ничего не меняется: апрувы, ревьюеры и комментарии бесполезны.
	// Нужны только sha и релизные ветки из него — минус 3 запроса на каждый за тик.
	if mr.Merged() {
		if c.cfg.Rules.ReleaseBranchPrefix == "" {
			return mr, nil
		}
		sha := full.MergedSHA()
		if sha == "" {
			return mr, nil
		}
		branches, err := c.gl.CommitBranches(ctx, full.ProjectID, sha)
		if err != nil {
			return nil, fmt.Errorf("branches of %s: %w", gitlab.MRRef(full), err)
		}
		for _, b := range branches {
			if strings.HasPrefix(b, c.cfg.Rules.ReleaseBranchPrefix) {
				mr.ReleaseBranches = append(mr.ReleaseBranches, b)
			}
		}
		return mr, nil
	}

	approvals, err := c.gl.Approvals(ctx, full.ProjectID, full.IID)
	if err != nil {
		return nil, err
	}
	for _, a := range approvals.ApprovedBy {
		mr.ApprovedBy = append(mr.ApprovedBy, a.User.Username)
	}

	reviewers, err := c.gl.Reviewers(ctx, full.ProjectID, full.IID)
	if err != nil {
		return nil, err
	}
	mr.Reviewers = toReviewers(reviewers)

	notes, err := c.gl.Notes(ctx, full.ProjectID, full.IID)
	if err != nil {
		return nil, err
	}
	mr.Notes = toNotes(notes)
	return mr, nil
}
