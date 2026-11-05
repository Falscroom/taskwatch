package gitlab

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"taskwatch/internal/req"
)

type Client struct {
	base  string
	token string
	http  *http.Client
}

func New(baseURL, token string) *Client {
	return &Client{
		base:  strings.TrimRight(baseURL, "/") + "/api/v4",
		token: token,
		http:  &http.Client{Timeout: 30 * time.Second},
	}
}

type User struct {
	Username string `json:"username"`
	Name     string `json:"name"`
}

type Pipeline struct {
	ID     int    `json:"id"`
	Status string `json:"status"`
	WebURL string `json:"web_url"`
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
	return out, c.get(ctx, "/merge_requests", q, &out)
}

func (c *Client) ReviewRequests(ctx context.Context, username string) ([]MergeRequest, error) {
	q := url.Values{
		"scope":             {"all"},
		"state":             {"opened"},
		"reviewer_username": {username},
		"per_page":          {"100"},
	}
	var out []MergeRequest
	return out, c.get(ctx, "/merge_requests", q, &out)
}

// MergeRequest возвращает MR с head_pipeline и diverged_commits_count, которых нет в списке.
func (c *Client) MergeRequest(ctx context.Context, projectID, iid int) (MergeRequest, error) {
	q := url.Values{"include_diverged_commits_count": {"true"}}
	var out MergeRequest
	return out, c.get(ctx, fmt.Sprintf("/projects/%d/merge_requests/%d", projectID, iid), q, &out)
}

func (c *Client) Approvals(ctx context.Context, projectID, iid int) (Approvals, error) {
	var out Approvals
	return out, c.get(ctx, fmt.Sprintf("/projects/%d/merge_requests/%d/approvals", projectID, iid), nil, &out)
}

func (c *Client) Reviewers(ctx context.Context, projectID, iid int) ([]Reviewer, error) {
	var out []Reviewer
	return out, c.get(ctx, fmt.Sprintf("/projects/%d/merge_requests/%d/reviewers", projectID, iid), nil, &out)
}

func (c *Client) Notes(ctx context.Context, projectID, iid int) ([]Note, error) {
	q := url.Values{"sort": {"desc"}, "order_by": {"created_at"}, "per_page": {"50"}}
	var out []Note
	return out, c.get(ctx, fmt.Sprintf("/projects/%d/merge_requests/%d/notes", projectID, iid), q, &out)
}

// CommitBranches — ветки, в которых есть коммит.
func (c *Client) CommitBranches(ctx context.Context, projectID int, sha string) ([]string, error) {
	q := url.Values{"type": {"branch"}, "per_page": {"100"}}
	var refs []ref
	if err := c.get(ctx, fmt.Sprintf("/projects/%d/repository/commits/%s/refs", projectID, sha), q, &refs); err != nil {
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
	return out, c.get(ctx, "/user", nil, &out)
}

func (c *Client) get(ctx context.Context, path string, q url.Values, out any) error {
	// GET идемпотентен — осечки сети и 429/5xx повторяем.
	return req.Do(ctx, func() error { return c.getOnce(ctx, path, q, out) })
}

func (c *Client) getOnce(ctx context.Context, path string, q url.Values, out any) error {
	u := c.base + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	r, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	r.Header.Set("PRIVATE-TOKEN", c.token)

	resp, err := c.http.Do(r)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return &req.StatusError{
			Code:    resp.StatusCode,
			Message: fmt.Sprintf("gitlab GET %s: %s: %s", path, resp.Status, body),
		}
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

func MRRef(mr MergeRequest) string {
	return strconv.Itoa(mr.ProjectID) + "!" + strconv.Itoa(mr.IID)
}
