package youtrack

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"taskwatch/internal/integrations/httpx"
)

func server(t *testing.T, h http.HandlerFunc) *Client {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return New(srv.URL+"/", "perm:tok", httpx.WithRetryDelays(time.Millisecond, time.Millisecond))
}

func TestIssues(t *testing.T) {
	var got *http.Request
	c := server(t, func(w http.ResponseWriter, r *http.Request) {
		got = r
		_, _ = w.Write([]byte(`[{"idReadable":"ABC-1","summary":"S","updated":5,"customFields":[{"name":"State","value":{"name":"Open"}}]}]`))
	})
	issues, err := c.Issues(context.Background(), "for: me")
	if err != nil {
		t.Fatal(err)
	}
	if got.URL.Path != "/api/issues" || got.Header.Get("Authorization") != "Bearer perm:tok" || got.Header.Get("Accept") != "application/json" {
		t.Errorf("request = %s auth=%q", got.URL.Path, got.Header.Get("Authorization"))
	}
	q := got.URL.Query()
	if q.Get("query") != "for: me" || q.Get("$top") != "100" || !strings.Contains(q.Get("fields"), "customFields(name,value(name))") {
		t.Errorf("query = %v", q)
	}
	if len(issues) != 1 || issues[0].ID != "ABC-1" || issues[0].Field("State") != "Open" {
		t.Errorf("issues = %+v", issues)
	}
}

func TestComments(t *testing.T) {
	var path string
	c := server(t, func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.EscapedPath()
		_, _ = w.Write([]byte(`[{"id":"c1","text":"hi","created":1,"author":{"login":"bob"}}]`))
	})
	got, err := c.Comments(context.Background(), "ABC/1")
	if err != nil || len(got) != 1 || got[0].Author.Login != "bob" {
		t.Fatalf("comments = %+v, %v", got, err)
	}
	if path != "/api/issues/ABC%2F1/comments" {
		t.Errorf("path = %s, issue id must be escaped", path)
	}
}

func TestAgiles(t *testing.T) {
	var got *http.Request
	c := server(t, func(w http.ResponseWriter, r *http.Request) {
		got = r
		_, _ = w.Write([]byte(`[{"name":"Team A","currentSprint":{"name":"Sprint 42"}},{"name":"Kanban","currentSprint":null}]`))
	})
	agiles, err := c.Agiles(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got.URL.Path != "/api/agiles" || got.URL.Query().Get("fields") != "name,currentSprint(name)" {
		t.Errorf("request = %s?%s", got.URL.Path, got.URL.RawQuery)
	}
	if len(agiles) != 2 || agiles[0].Name != "Team A" || agiles[0].CurrentSprint == nil ||
		agiles[0].CurrentSprint.Name != "Sprint 42" || agiles[1].CurrentSprint != nil {
		t.Errorf("agiles = %+v", agiles)
	}
}

func TestSprintQuery(t *testing.T) {
	if got := SprintQuery("Team A"); got != "for: me Board Team A: {Current sprint}" {
		t.Errorf("SprintQuery = %q", got)
	}
}

func TestCommandIsPostedOnceWithoutRetry(t *testing.T) {
	var calls atomic.Int32
	var body map[string]any
	var contentType string
	c := server(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		contentType = r.Header.Get("Content-Type")
		data, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(data, &body)
		http.Error(w, "busy", http.StatusServiceUnavailable)
	})
	err := c.Command(context.Background(), "ABC-1", "State Ready for QA")
	var se *httpx.StatusError
	if !errors.As(err, &se) || se.Code != 503 || !strings.Contains(se.Message, "youtrack POST /api/commands") {
		t.Errorf("err = %v", err)
	}
	if calls.Load() != 1 {
		t.Errorf("POST retried: %d calls", calls.Load())
	}
	if contentType != "application/json" || body["query"] != "State Ready for QA" {
		t.Errorf("content-type %q, body %v", contentType, body)
	}
	issues, _ := body["issues"].([]any)
	if len(issues) != 1 || issues[0].(map[string]any)["idReadable"] != "ABC-1" {
		t.Errorf("issues = %v", body["issues"])
	}
}

func TestGetIsRetried(t *testing.T) {
	var calls atomic.Int32
	c := server(t, func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			http.Error(w, "busy", http.StatusBadGateway)
			return
		}
		if r.URL.Path != "/users/me" || r.URL.Query().Get("fields") != "login" {
			t.Errorf("request = %s?%s", r.URL.Path, r.URL.RawQuery)
		}
		_, _ = w.Write([]byte(`{"login":"me"}`))
	})
	if login, err := c.CurrentUser(context.Background()); err != nil || login != "me" || calls.Load() != 2 {
		t.Errorf("login = %q, err = %v, calls = %d", login, err, calls.Load())
	}
}

func TestIssueURL(t *testing.T) {
	if got := New("https://yt.example.com/", "t").IssueURL("ABC-1"); got != "https://yt.example.com/issue/ABC-1" {
		t.Errorf("IssueURL = %q", got)
	}
}

func TestField(t *testing.T) {
	issue := Issue{CustomFields: []CustomField{
		{Name: "State", Value: json.RawMessage(`{"name":"Open"}`)},
		{Name: "Assignees", Value: json.RawMessage(`[{"name":"a"},{"name":"b"}]`)},
		{Name: "Text", Value: json.RawMessage(`"plain"`)},
		{Name: "Empty", Value: json.RawMessage(`null`)},
		{Name: "NoName", Value: json.RawMessage(`{"id":"1"}`)},
		{Name: "Number", Value: json.RawMessage(`5`)},
	}}
	cases := map[string]string{
		"State": "Open", "Assignees": "a, b", "Text": "plain", "Empty": "", "NoName": "", "Number": "", "Missing": "",
	}
	for name, want := range cases {
		if got := issue.Field(name); got != want {
			t.Errorf("Field(%q) = %q, want %q", name, got, want)
		}
	}
}
