// Package youtrack — клиент нужной части YouTrack REST API (client.go) и источник
// тасок для сборщика (source.go): переводит ответы API в доменную модель.
package youtrack

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"

	"taskwatch/internal/integrations/httpx"
)

type Client struct {
	api *httpx.Client
}

// New — клиент YouTrack REST API; opts — для тестов (паузы повторов).
func New(baseURL, token string, opts ...httpx.Option) *Client {
	auth := func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+token) }
	return &Client{api: httpx.New("youtrack", baseURL, auth, opts...)}
}

type CustomField struct {
	Name  string          `json:"name"`
	Value json.RawMessage `json:"value"`
}

type Issue struct {
	ID           string        `json:"idReadable"`
	Summary      string        `json:"summary"`
	Updated      int64         `json:"updated"`
	CustomFields []CustomField `json:"customFields"`
}

// Field возвращает значение кастомного поля: enum/state — объект с name, multi-value — массив.
func (i Issue) Field(name string) string {
	for _, f := range i.CustomFields {
		if f.Name != name || len(f.Value) == 0 || string(f.Value) == "null" {
			continue
		}
		var one struct {
			Name string `json:"name"`
		}
		if json.Unmarshal(f.Value, &one) == nil && one.Name != "" {
			return one.Name
		}
		var many []struct {
			Name string `json:"name"`
		}
		if json.Unmarshal(f.Value, &many) == nil {
			names := make([]string, 0, len(many))
			for _, v := range many {
				names = append(names, v.Name)
			}
			return strings.Join(names, ", ")
		}
		var s string
		if json.Unmarshal(f.Value, &s) == nil {
			return s
		}
	}
	return ""
}

type Comment struct {
	ID      string `json:"id"`
	Text    string `json:"text"`
	Created int64  `json:"created"`
	Author  struct {
		Login string `json:"login"`
	} `json:"author"`
}

func (c *Client) Issues(ctx context.Context, query string) ([]Issue, error) {
	q := url.Values{
		"query":  {query},
		"fields": {"idReadable,summary,updated,customFields(name,value(name))"},
		"$top":   {"100"},
	}
	var out []Issue
	return out, c.api.Get(ctx, "/api/issues", q, &out)
}

func (c *Client) Comments(ctx context.Context, issueID string) ([]Comment, error) {
	q := url.Values{"fields": {"id,text,created,author(login)"}, "$top": {"100"}}
	var out []Comment
	return out, c.api.Get(ctx, "/api/issues/"+url.PathEscape(issueID)+"/comments", q, &out)
}

// Command применяет команду YouTrack к задаче, например "State Ready for QA".
func (c *Client) Command(ctx context.Context, issueID, command string) error {
	body := map[string]any{
		"query":  command,
		"issues": []map[string]string{{"idReadable": issueID}},
	}
	return c.api.Post(ctx, "/api/commands", body, nil)
}

func (c *Client) IssueURL(issueID string) string {
	return c.api.Base() + "/issue/" + issueID
}

// CurrentUser проверяет токен и возвращает логин его владельца.
func (c *Client) CurrentUser(ctx context.Context) (string, error) {
	var out struct {
		Login string `json:"login"`
	}
	err := c.api.Get(ctx, "/users/me", url.Values{"fields": {"login"}}, &out)
	return out.Login, err
}
