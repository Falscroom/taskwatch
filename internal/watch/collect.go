// Package watch собирает состояние тасок из YouTrack и связанных MR из GitLab в один снапшот.
package watch

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	"taskwatch/internal/config"
	"taskwatch/internal/gitlab"
	"taskwatch/internal/youtrack"
)

var issueKeyRe = regexp.MustCompile(`(?i)\b([a-z][a-z0-9]+-\d+)\b`)

type MR struct {
	gitlab.MergeRequest
	Approvals       int
	ApprovedBy      []string
	Reviewers       []gitlab.Reviewer
	Notes           []gitlab.Note
	ReleaseBranches []string
}

type Task struct {
	Issue    youtrack.Issue
	State    string
	URL      string
	Comments []youtrack.Comment
	MRs      []*MR
}

type Snapshot struct {
	Tasks []*Task
	// MR без таски из спринта — тоже показываем в статусе
	Orphans []*MR
	// MR других людей, где ты ревьюер
	Reviews []*MR
}

// AllMRs — все MR снапшота в одном списке: для обходов, которым не важно происхождение.
func (s *Snapshot) AllMRs() []*MR {
	out := make([]*MR, 0, len(s.Orphans)+len(s.Reviews))
	for _, t := range s.Tasks {
		out = append(out, t.MRs...)
	}
	out = append(out, s.Orphans...)
	out = append(out, s.Reviews...)
	return out
}

type Collector struct {
	cfg *config.Config
	gl  *gitlab.Client
	yt  *youtrack.Client
	// byIssue — таски последнего прохода; нужен, только если настроен YouTrack
	byIssue map[string]*Task
}

func NewCollector(cfg *config.Config, gl *gitlab.Client, yt *youtrack.Client) *Collector {
	return &Collector{cfg: cfg, gl: gl, yt: yt}
}

func (c *Collector) Collect(ctx context.Context) (*Snapshot, error) {
	snap := &Snapshot{}

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

func (c *Collector) collectIssues(ctx context.Context, snap *Snapshot) error {
	issues, err := c.yt.Issues(ctx, c.cfg.YouTrack.Query)
	if err != nil {
		return err
	}
	byID := make(map[string]*Task, len(issues))
	for _, issue := range issues {
		comments, err := c.yt.Comments(ctx, issue.ID)
		if err != nil {
			return err
		}
		task := &Task{
			Issue:    issue,
			State:    issue.Field("State"),
			URL:      c.yt.IssueURL(issue.ID),
			Comments: comments,
		}
		snap.Tasks = append(snap.Tasks, task)
		byID[issue.ID] = task
	}
	c.byIssue = byID
	return nil
}

func (c *Collector) collectMRs(ctx context.Context, snap *Snapshot) error {
	since := time.Now().Add(-c.cfg.Rules.MRLookback)
	mrs, err := c.gl.MyMergeRequests(ctx, c.cfg.GitLab.Username, since)
	if err != nil {
		return err
	}
	for _, item := range mrs {
		if item.State == "closed" || c.skipDraft(item) {
			continue
		}
		mr, err := c.loadMR(ctx, item)
		if err != nil {
			return err
		}
		if task, ok := c.byIssue[issueKey(item)]; ok {
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

func (c *Collector) loadMR(ctx context.Context, item gitlab.MergeRequest) (*MR, error) {
	full, err := c.gl.MergeRequest(ctx, item.ProjectID, item.IID)
	if err != nil {
		return nil, err
	}
	mr := &MR{MergeRequest: full}

	// Смёрженный уже ничего не меняется: апрувы, ревьюеры и комментарии бесполезны.
	// Нужны только sha и релизные ветки из него — минус 3 запроса на каждый за тик.
	if full.State == "merged" {
		if c.cfg.Rules.ReleaseBranchPrefix == "" {
			return mr, nil
		}
		sha := mr.MergedSHA()
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
	mr.Approvals = len(mr.ApprovedBy)

	if mr.Reviewers, err = c.gl.Reviewers(ctx, full.ProjectID, full.IID); err != nil {
		return nil, err
	}

	if mr.Notes, err = c.gl.Notes(ctx, full.ProjectID, full.IID); err != nil {
		return nil, err
	}
	return mr, nil
}

// IsApprovedBy сообщает, апрувнул ли пользователь MR.
func (mr *MR) IsApprovedBy(username string) bool {
	for _, u := range mr.ApprovedBy {
		if u == username {
			return true
		}
	}
	return false
}

// issueKey достаёт APP-1234 из имени ветки, а если там нет — из заголовка MR.
func issueKey(mr gitlab.MergeRequest) string {
	for _, s := range []string{mr.SourceBranch, mr.Title} {
		if m := issueKeyRe.FindStringSubmatch(s); m != nil {
			return strings.ToUpper(m[1])
		}
	}
	return ""
}
