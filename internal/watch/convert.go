package watch

import (
	"taskwatch/internal/domain"
	"taskwatch/internal/gitlab"
	"taskwatch/internal/youtrack"
)

// Перевод ответов GitLab и YouTrack в доменную модель.

func toMR(m gitlab.MergeRequest) *domain.MR {
	mr := &domain.MR{
		ID:                  m.ID,
		IID:                 m.IID,
		ProjectID:           m.ProjectID,
		Title:               m.Title,
		WebURL:              m.WebURL,
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

func toUser(u gitlab.User) domain.User { return domain.User{Username: u.Username, Name: u.Name} }

func toReviewers(in []gitlab.Reviewer) []domain.Reviewer {
	out := make([]domain.Reviewer, 0, len(in))
	for _, r := range in {
		out = append(out, domain.Reviewer{User: toUser(r.User), State: domain.ReviewerState(r.State)})
	}
	return out
}

func toNotes(in []gitlab.Note) []domain.Note {
	out := make([]domain.Note, 0, len(in))
	for _, n := range in {
		out = append(out, domain.Note{ID: n.ID, Author: toUser(n.Author), Body: n.Body, System: n.System})
	}
	return out
}

func toTask(issue youtrack.Issue, url string, comments []youtrack.Comment) *domain.Task {
	t := &domain.Task{ID: issue.ID, Summary: issue.Summary, State: issue.Field("State"), URL: url}
	for _, c := range comments {
		t.Comments = append(t.Comments, domain.Comment{ID: c.ID, Author: c.Author.Login, Text: c.Text})
	}
	return t
}
