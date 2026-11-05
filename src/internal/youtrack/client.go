package youtrack

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
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
		base:  strings.TrimRight(baseURL, "/"),
		token: token,
		http:  &http.Client{Timeout: 30 * time.Second},
	}
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
	return out, c.do(ctx, http.MethodGet, "/api/issues", q, nil, &out)
}

func (c *Client) Comments(ctx context.Context, issueID string) ([]Comment, error) {
	q := url.Values{"fields": {"id,text,created,author(login)"}, "$top": {"100"}}
	var out []Comment
	return out, c.do(ctx, http.MethodGet, "/api/issues/"+url.PathEscape(issueID)+"/comments", q, nil, &out)
}

// Command применяет команду YouTrack к задаче, например "State Ready for QA".
func (c *Client) Command(ctx context.Context, issueID, command string) error {
	body := map[string]any{
		"query":  command,
		"issues": []map[string]string{{"idReadable": issueID}},
	}
	return c.do(ctx, http.MethodPost, "/api/commands", nil, body, nil)
}

func (c *Client) IssueURL(issueID string) string {
	return c.base + "/issue/" + issueID
}

// CurrentUser проверяет токен и возвращает логин его владельца.
func (c *Client) CurrentUser(ctx context.Context) (string, error) {
	var out struct {
		Login string `json:"login"`
	}
	err := c.do(ctx, http.MethodGet, "/users/me", url.Values{"fields": {"login"}}, nil, &out)
	return out.Login, err
}

func (c *Client) do(ctx context.Context, method, path string, q url.Values, in, out any) error {
	attempt := func() error { return c.doOnce(ctx, method, path, q, in, out) }
	if method == http.MethodGet {
		// GET идемпотентен — осечки сети и 429/5xx повторяем. POST (команды) не трогаем.
		return req.Do(ctx, attempt)
	}
	return attempt()
}

func (c *Client) doOnce(ctx context.Context, method, path string, q url.Values, in, out any) error {
	u := c.base + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}

	var body io.Reader
	if in != nil {
		data, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body = bytes.NewReader(data)
	}

	r, err := http.NewRequestWithContext(ctx, method, u, body)
	if err != nil {
		return err
	}
	r.Header.Set("Authorization", "Bearer "+c.token)
	r.Header.Set("Accept", "application/json")
	if in != nil {
		r.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.http.Do(r)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return &req.StatusError{
			Code:    resp.StatusCode,
			Message: fmt.Sprintf("youtrack %s %s: %s: %s", method, path, resp.Status, msg),
		}
	}
	if out == nil {
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}
