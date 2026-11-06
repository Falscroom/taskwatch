// Package gitlab — клиент нужной части GitLab REST API v4 (client.go) и источник
// MR для сборщика (source.go): переводит ответы API в доменную модель.
package gitlab

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"taskwatch/internal/integrations/httpx"
)

type Client struct {
	api *httpx.Client
}

// New — клиент GitLab REST API v4; opts — для тестов (паузы повторов).
func New(baseURL, token string, opts ...httpx.Option) *Client {
	auth := func(r *http.Request) { r.Header.Set("PRIVATE-TOKEN", token) }
	return &Client{api: httpx.New("gitlab", strings.TrimRight(baseURL, "/")+"/api/v4", auth, opts...)}
}

type User struct {
	Username string `json:"username"`
	Name     string `json:"name"`
}

type Pipeline struct {
	ID     int    `json:"id"`
	Status string `json:"status"`
	WebURL string `json:"web_url"`
	SHA    string `json:"sha"`
}

type MergeRequest struct {
	ID              int       `json:"id"`
	IID             int       `json:"iid"`
	ProjectID       int       `json:"project_id"`
	Title           string    `json:"title"`
	WebURL          string    `json:"web_url"`
	SourceBranch    string    `json:"source_branch"`
	TargetBranch    string    `json:"target_branch"`
	State           string    `json:"state"`
	SHA             string    `json:"sha"`
	MergeCommitSHA  string    `json:"merge_commit_sha"`
	SquashCommitSHA string    `json:"squash_commit_sha"`
	CreatedAt       time.Time `json:"created_at"`
	// Draft — MR помечен как черновик; отфильтровывается настройкой exclude_draft.
	Draft                bool      `json:"draft"`
	HasConflicts         bool      `json:"has_conflicts"`
	Author               User      `json:"author"`
	HeadPipeline         *Pipeline `json:"head_pipeline"`
	DivergedCommitsCount int       `json:"diverged_commits_count"`
	UserNotesCount       int       `json:"user_notes_count"`
	// false — есть нерешённые треды, блокирующие мерж
	BlockingDiscussionsResolved bool `json:"blocking_discussions_resolved"`
}

// MergedSHA — коммит, по которому проверяем попадание в релизные ветки.
func (mr MergeRequest) MergedSHA() string {
	for _, sha := range []string{mr.MergeCommitSHA, mr.SquashCommitSHA, mr.SHA} {
		if sha != "" {
			return sha
		}
	}
	return ""
}

type Approvals struct {
	ApprovalsLeft int `json:"approvals_left"`
	ApprovedBy    []struct {
		User User `json:"user"`
	} `json:"approved_by"`
}

// Reviewer.State: unreviewed, reviewed, requested_changes, approved, unapproved
type Reviewer struct {
	User  User   `json:"user"`
	State string `json:"state"`
}

type Note struct {
	ID        int       `json:"id"`
	Body      string    `json:"body"`
	System    bool      `json:"system"`
	Author    User      `json:"author"`
	CreatedAt time.Time `json:"created_at"`
}

type ref struct {
	Type string `json:"type"`
	Name string `json:"name"`
}

func (c *Client) MyMergeRequests(ctx context.Context, username string, since time.Time) ([]MergeRequest, error) {
	q := url.Values{
		"scope":           {"all"},
		"state":           {"all"},
		"author_username": {username},
		"updated_after":   {since.Format(time.RFC3339)},
		"per_page":        {"100"},
	}
	var out []MergeRequest
	return out, c.api.Get(ctx, "/merge_requests", q, &out)
}

func (c *Client) ReviewRequests(ctx context.Context, username string) ([]MergeRequest, error) {
	q := url.Values{
		"scope":             {"all"},
		"state":             {"opened"},
		"reviewer_username": {username},
		"per_page":          {"100"},
	}
	var out []MergeRequest
	return out, c.api.Get(ctx, "/merge_requests", q, &out)
}

// MergeRequest возвращает MR с head_pipeline и diverged_commits_count, которых нет в списке.
func (c *Client) MergeRequest(ctx context.Context, projectID, iid int) (MergeRequest, error) {
	q := url.Values{"include_diverged_commits_count": {"true"}}
	var out MergeRequest
	return out, c.api.Get(ctx, fmt.Sprintf("/projects/%d/merge_requests/%d", projectID, iid), q, &out)
}

func (c *Client) Approvals(ctx context.Context, projectID, iid int) (Approvals, error) {
	var out Approvals
	return out, c.api.Get(ctx, fmt.Sprintf("/projects/%d/merge_requests/%d/approvals", projectID, iid), nil, &out)
}

func (c *Client) Reviewers(ctx context.Context, projectID, iid int) ([]Reviewer, error) {
	var out []Reviewer
	return out, c.api.Get(ctx, fmt.Sprintf("/projects/%d/merge_requests/%d/reviewers", projectID, iid), nil, &out)
}

func (c *Client) Notes(ctx context.Context, projectID, iid int) ([]Note, error) {
	q := url.Values{"sort": {"desc"}, "order_by": {"created_at"}, "per_page": {"50"}}
	var out []Note
	return out, c.api.Get(ctx, fmt.Sprintf("/projects/%d/merge_requests/%d/notes", projectID, iid), q, &out)
}

// OpenMRFrom — открытый MR из ветки branch (у релизной это обычно «Draft: Release …»);
// ok=false, если такого нет. Без head_pipeline: его отдаёт только MergeRequest.
func (c *Client) OpenMRFrom(ctx context.Context, projectID int, branch string) (mr MergeRequest, ok bool, err error) {
	q := url.Values{"state": {"opened"}, "source_branch": {branch}, "per_page": {"1"}}
	var out []MergeRequest
	if err := c.api.Get(ctx, fmt.Sprintf("/projects/%d/merge_requests", projectID), q, &out); err != nil || len(out) == 0 {
		return MergeRequest{}, false, err
	}
	return out[0], true, nil
}

// LatestPipeline — последний пайплайн ветки; nil, если пайплайнов нет.
func (c *Client) LatestPipeline(ctx context.Context, projectID int, ref string) (*Pipeline, error) {
	q := url.Values{"ref": {ref}, "per_page": {"1"}}
	var out []Pipeline
	if err := c.api.Get(ctx, fmt.Sprintf("/projects/%d/pipelines", projectID), q, &out); err != nil || len(out) == 0 {
		return nil, err
	}
	return &out[0], nil
}

// Contains — есть ли коммит sha в истории коммита head: их общий предок — сам sha.
func (c *Client) Contains(ctx context.Context, projectID int, head, sha string) (bool, error) {
	if head == sha {
		return true, nil
	}
	q := url.Values{"refs[]": {head, sha}}
	var base struct {
		ID string `json:"id"`
	}
	if err := c.api.Get(ctx, fmt.Sprintf("/projects/%d/repository/merge_base", projectID), q, &base); err != nil {
		return false, err
	}
	return base.ID == sha, nil
}

// CommitBranches — ветки, в которых есть коммит.
func (c *Client) CommitBranches(ctx context.Context, projectID int, sha string) ([]string, error) {
	q := url.Values{"type": {"branch"}, "per_page": {"100"}}
	var refs []ref
	if err := c.api.Get(ctx, fmt.Sprintf("/projects/%d/repository/commits/%s/refs", projectID, sha), q, &refs); err != nil {
		return nil, err
	}
	names := make([]string, 0, len(refs))
	for _, r := range refs {
		names = append(names, r.Name)
	}
	return names, nil
}

// CurrentUser проверяет токен и возвращает его владельца.
func (c *Client) CurrentUser(ctx context.Context) (User, error) {
	var out User
	return out, c.api.Get(ctx, "/user", nil, &out)
}

func MRRef(mr MergeRequest) string {
	return strconv.Itoa(mr.ProjectID) + "!" + strconv.Itoa(mr.IID)
}
