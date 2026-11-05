package youtrack

import (
	"context"

	"taskwatch/internal/domain"
)

// Source — таски YouTrack по запросу в доменном виде (порт collect.TaskSource).
type Source struct {
	client *Client
	query  string // например "for: me Sprint: {Current sprint}"
}

func NewSource(client *Client, query string) *Source {
	return &Source{client: client, query: query}
}

// Tasks — таски по запросу, без комментариев: их догружает Comments.
func (s *Source) Tasks(ctx context.Context) ([]*domain.Task, error) {
	issues, err := s.client.Issues(ctx, s.query)
	if err != nil {
		return nil, err
	}
	out := make([]*domain.Task, 0, len(issues))
	for _, i := range issues {
		out = append(out, &domain.Task{ID: i.ID, Summary: i.Summary, State: i.Field("State"), URL: s.client.IssueURL(i.ID)})
	}
	return out, nil
}

// Comments — комментарии таски.
func (s *Source) Comments(ctx context.Context, taskID string) ([]domain.Comment, error) {
	comments, err := s.client.Comments(ctx, taskID)
	if err != nil {
		return nil, err
	}
	out := make([]domain.Comment, 0, len(comments))
	for _, c := range comments {
		out = append(out, domain.Comment{ID: c.ID, Author: c.Author.Login, Text: c.Text})
	}
	return out, nil
}
