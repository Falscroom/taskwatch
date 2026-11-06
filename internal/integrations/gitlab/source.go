package gitlab

import (
	"context"
	"fmt"
	"strings"
	"time"

	"taskwatch/internal/domain"
)

// Source — твои MR и MR на ревью из GitLab в доменном виде (порт collect.MRSource).
type Source struct {
	client   *Client
	username string
	// releasePrefix — какие ветки считать релизными; пусто — не проверять.
	releasePrefix string
}

func NewSource(client *Client, username, releasePrefix string) *Source {
	return &Source{client: client, username: username, releasePrefix: releasePrefix}
}

// Authored — твои MR, обновлённые после since. Без деталей: их догружает Load.
func (s *Source) Authored(ctx context.Context, since time.Time) ([]*domain.MR, error) {
	return toMRs(s.client.MyMergeRequests(ctx, s.username, since))
}

// ReviewRequested — открытые MR, где ты ревьюер. Без деталей.
func (s *Source) ReviewRequested(ctx context.Context) ([]*domain.MR, error) {
	return toMRs(s.client.ReviewRequests(ctx, s.username))
}

// Load — MR целиком: с пайплайном и отставанием, а у открытого — с апрувами,
// ревьюерами и комментариями. Смёрженный уже не меняется: у него нужны только
// релизные ветки, куда попал его коммит, — минус три запроса на каждый за тик.
func (s *Source) Load(ctx context.Context, item *domain.MR) (*domain.MR, error) {
	full, err := s.client.MergeRequest(ctx, item.ProjectID, item.IID)
	if err != nil {
		return nil, err
	}
	mr := toMR(full)

	if mr.Merged() {
		sha := full.MergedSHA()
		if s.releasePrefix == "" || sha == "" {
			return mr, nil
		}
		branches, err := s.client.CommitBranches(ctx, full.ProjectID, sha)
		if err != nil {
			return nil, fmt.Errorf("branches of %s: %w", MRRef(full), err)
		}
		for _, b := range branches {
			if strings.HasPrefix(b, s.releasePrefix) {
				mr.ReleaseBranches = append(mr.ReleaseBranches, b)
			}
		}
		return mr, nil
	}

	approvals, err := s.client.Approvals(ctx, full.ProjectID, full.IID)
	if err != nil {
		return nil, err
	}
	for _, a := range approvals.ApprovedBy {
		mr.ApprovedBy = append(mr.ApprovedBy, a.User.Username)
	}
	reviewers, err := s.client.Reviewers(ctx, full.ProjectID, full.IID)
	if err != nil {
		return nil, err
	}
	for _, r := range reviewers {
		mr.Reviewers = append(mr.Reviewers, domain.Reviewer{User: toUser(r.User), State: domain.ReviewerState(r.State)})
	}
	notes, err := s.client.Notes(ctx, full.ProjectID, full.IID)
	if err != nil {
		return nil, err
	}
	for _, n := range notes {
		mr.Notes = append(mr.Notes, domain.Note{ID: n.ID, Author: toUser(n.Author), Body: n.Body, System: n.System})
	}
	return mr, nil
}

func toMRs(in []MergeRequest, err error) ([]*domain.MR, error) {
	if err != nil {
		return nil, err
	}
	out := make([]*domain.MR, 0, len(in))
	for _, m := range in {
		out = append(out, toMR(m))
	}
	return out, nil
}

func toMR(m MergeRequest) *domain.MR {
	mr := &domain.MR{
		ID:                  m.ID,
		IID:                 m.IID,
		ProjectID:           m.ProjectID,
		Title:               m.Title,
		WebURL:              m.WebURL,
		SourceBranch:        m.SourceBranch,
		TargetBranch:        m.TargetBranch,
		State:               domain.MRState(m.State),
		SHA:                 m.SHA,
		CreatedAt:           m.CreatedAt,
		Draft:               m.Draft,
		Author:              toUser(m.Author),
		HasConflicts:        m.HasConflicts,
		DiscussionsResolved: m.BlockingDiscussionsResolved,
		Behind:              m.DivergedCommitsCount,
		Comments:            m.UserNotesCount,
	}
	if p := m.HeadPipeline; p != nil {
		mr.Pipeline = &domain.Pipeline{ID: p.ID, Status: domain.PipelineStatus(p.Status), WebURL: p.WebURL}
	}
	return mr
}

func toUser(u User) domain.User { return domain.User{Username: u.Username, Name: u.Name} }
