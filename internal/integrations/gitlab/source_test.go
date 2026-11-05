package gitlab_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"taskwatch/internal/domain"
	"taskwatch/internal/integrations/gitlab"
	"taskwatch/internal/integrations/httpx"
	"taskwatch/internal/testutil/fakeapi"
)

func source(srv *fakeapi.Server, releasePrefix string) *gitlab.Source {
	return gitlab.NewSource(gitlab.New(srv.URL, "tok", httpx.WithRetryDelays()), "me", releasePrefix)
}

func TestSourceMapsMR(t *testing.T) {
	created := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	srv := fakeapi.New(t)
	srv.SetMine(fakeapi.MR{
		MergeRequest: gitlab.MergeRequest{
			ID: 101, IID: 3, ProjectID: 7, Title: "Fix", WebURL: "https://gl/mr/3", SourceBranch: "feature/abc-1",
			State: "opened", SHA: "head", CreatedAt: created, Draft: true, Author: gitlab.User{Username: "me", Name: "Me"},
			HasConflicts: true, BlockingDiscussionsResolved: true, DivergedCommitsCount: 4, UserNotesCount: 9,
			HeadPipeline: &gitlab.Pipeline{ID: 55, Status: "failed", WebURL: "https://gl/p/55"},
		},
		ApprovedBy: []string{"bob"},
		Reviewers:  []gitlab.Reviewer{{User: gitlab.User{Username: "bob", Name: "Bob"}, State: "approved"}},
		Notes:      []gitlab.Note{{ID: 1, Body: "hi", System: true, Author: gitlab.User{Username: "bob"}}},
	})
	src := source(srv, "release/")

	list, err := src.Authored(context.Background(), time.Now().Add(-time.Hour))
	if err != nil || len(list) != 1 {
		t.Fatalf("authored = %+v, %v", list, err)
	}
	mr, err := src.Load(context.Background(), list[0])
	if err != nil {
		t.Fatal(err)
	}
	want := domain.MR{
		ID: 101, IID: 3, ProjectID: 7, Title: "Fix", WebURL: "https://gl/mr/3", SourceBranch: "feature/abc-1",
		State: domain.MROpened, SHA: "head", CreatedAt: created, Draft: true, Author: domain.User{Username: "me", Name: "Me"},
		HasConflicts: true, DiscussionsResolved: true, Behind: 4, Comments: 9,
	}
	got := *mr
	if got.Pipeline == nil || *got.Pipeline != (domain.Pipeline{ID: 55, Status: "failed", WebURL: "https://gl/p/55"}) {
		t.Errorf("pipeline = %+v", got.Pipeline)
	}
	if len(got.ApprovedBy) != 1 || got.ApprovedBy[0] != "bob" ||
		len(got.Reviewers) != 1 || got.Reviewers[0] != (domain.Reviewer{User: domain.User{Username: "bob", Name: "Bob"}, State: domain.ReviewerApproved}) ||
		len(got.Notes) != 1 || got.Notes[0] != (domain.Note{ID: 1, Author: domain.User{Username: "bob"}, Body: "hi", System: true}) {
		t.Errorf("details = %+v / %+v / %+v", got.ApprovedBy, got.Reviewers, got.Notes)
	}
	got.Pipeline, got.ApprovedBy, got.Reviewers, got.Notes = nil, nil, nil, nil
	if !got.CreatedAt.Equal(want.CreatedAt) {
		t.Errorf("created = %v", got.CreatedAt)
	}
	got.CreatedAt = want.CreatedAt
	if got.ID != want.ID || got.Title != want.Title || got.SourceBranch != want.SourceBranch || got.State != want.State ||
		got.Author != want.Author || !got.HasConflicts || !got.DiscussionsResolved || got.Behind != 4 || got.Comments != 9 || !got.Draft {
		t.Errorf("mr = %+v", got)
	}
}

func TestSourceReviewRequested(t *testing.T) {
	srv := fakeapi.New(t)
	srv.SetReviews(fakeapi.MR{MergeRequest: gitlab.MergeRequest{ID: 9, IID: 9, ProjectID: 1, State: "opened"}})
	list, err := source(srv, "").ReviewRequested(context.Background())
	if err != nil || len(list) != 1 || list[0].ID != 9 {
		t.Errorf("reviews = %+v, %v", list, err)
	}
}

func TestSourceMergedBranchesError(t *testing.T) {
	srv := fakeapi.New(t)
	merged := fakeapi.MR{MergeRequest: gitlab.MergeRequest{ID: 1, IID: 3, ProjectID: 7, State: "merged", SHA: "abc"}}
	srv.SetMine(merged)
	src := source(srv, "release/")
	list, _ := src.Authored(context.Background(), time.Now())
	srv.FailWith(500)
	if _, err := src.Load(context.Background(), list[0]); err == nil {
		t.Fatal("want error")
	}
	srv.FailWith(0)
	// Ошибка запроса веток помечается, какому MR она принадлежит.
	srv.SetMine(merged)
	srv.FailRefs(404)
	if _, err := src.Load(context.Background(), list[0]); err == nil || !strings.Contains(err.Error(), "branches of 7!3") {
		t.Errorf("err = %v", err)
	}
}
