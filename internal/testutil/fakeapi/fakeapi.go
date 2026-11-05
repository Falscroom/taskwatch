// Package fakeapi — поддельные GitLab и YouTrack на одном httptest-сервере для
// тестов сборщика, движка и HTTP API. Отдаёт заданные данные и запоминает запросы.
// GitLab живёт под /api/v4, YouTrack — под /api и /users, так что URL у них общий.
package fakeapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"testing"

	"taskwatch/internal/gitlab"
	"taskwatch/internal/youtrack"
)

// MR — merge request вместе со всем, что GitLab отдаёт по нему отдельными запросами.
type MR struct {
	gitlab.MergeRequest
	ApprovedBy []string
	Reviewers  []gitlab.Reviewer
	Notes      []gitlab.Note
	// Branches — ветки, содержащие коммит MR (ответ /repository/commits/:sha/refs).
	Branches []string
}

// Issue — таска YouTrack с комментариями.
type Issue struct {
	youtrack.Issue
	Comments []youtrack.Comment
}

// Command — применённая команда YouTrack (POST /api/commands).
type Command struct {
	Query   string
	IssueID string
}

type Server struct {
	*httptest.Server

	mu       sync.Mutex
	mine     []MR
	reviews  []MR
	issues   []Issue
	requests []string
	commands []Command
	failCode int

	GitLabUser    string
	YouTrackLogin string
}

// New запускает сервер; он остановится сам в конце теста.
func New(t testing.TB) *Server {
	s := &Server{GitLabUser: "me", YouTrackLogin: "me"}
	s.Server = httptest.NewServer(s.routes())
	t.Cleanup(s.Close)
	return s
}

// SetMine задаёт MR, где ты автор.
func (s *Server) SetMine(mrs ...MR) { s.mu.Lock(); s.mine = mrs; s.mu.Unlock() }

// SetReviews задаёт MR, где ты ревьюер.
func (s *Server) SetReviews(mrs ...MR) { s.mu.Lock(); s.reviews = mrs; s.mu.Unlock() }

// SetIssues задаёт таски, которые вернёт любой запрос задач.
func (s *Server) SetIssues(issues ...Issue) { s.mu.Lock(); s.issues = issues; s.mu.Unlock() }

// FailWith заставляет все запросы отвечать этим статусом; 0 — снова отвечать нормально.
func (s *Server) FailWith(code int) { s.mu.Lock(); s.failCode = code; s.mu.Unlock() }

// Requests — запросы в порядке поступления: "GET /api/v4/user?…".
func (s *Server) Requests() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.requests...)
}

// Commands — применённые команды YouTrack.
func (s *Server) Commands() []Command {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Command(nil), s.commands...)
}

// State — кастомное поле State со значением-объектом, как его отдаёт YouTrack.
func State(name string) youtrack.CustomField {
	v, _ := json.Marshal(map[string]string{"name": name})
	return youtrack.CustomField{Name: "State", Value: v}
}

func (s *Server) routes() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /api/v4/user", func(w http.ResponseWriter, r *http.Request) {
		write(w, gitlab.User{Username: s.GitLabUser, Name: "Me"})
	})
	mux.HandleFunc("GET /api/v4/merge_requests", func(w http.ResponseWriter, r *http.Request) {
		var src []MR
		s.mu.Lock()
		switch {
		case r.URL.Query().Has("author_username"):
			src = s.mine
		case r.URL.Query().Has("reviewer_username"):
			src = s.reviews
		}
		s.mu.Unlock()
		out := make([]gitlab.MergeRequest, 0, len(src))
		for _, m := range src {
			out = append(out, m.MergeRequest)
		}
		write(w, out)
	})
	mux.HandleFunc("GET /api/v4/projects/{project}/merge_requests/{iid}", func(w http.ResponseWriter, r *http.Request) {
		s.withMR(w, r, func(m MR) any { return m.MergeRequest })
	})
	mux.HandleFunc("GET /api/v4/projects/{project}/merge_requests/{iid}/approvals", func(w http.ResponseWriter, r *http.Request) {
		s.withMR(w, r, func(m MR) any {
			var a gitlab.Approvals
			for _, u := range m.ApprovedBy {
				a.ApprovedBy = append(a.ApprovedBy, struct {
					User gitlab.User `json:"user"`
				}{gitlab.User{Username: u}})
			}
			return a
		})
	})
	mux.HandleFunc("GET /api/v4/projects/{project}/merge_requests/{iid}/reviewers", func(w http.ResponseWriter, r *http.Request) {
		s.withMR(w, r, func(m MR) any { return nonNil(m.Reviewers) })
	})
	mux.HandleFunc("GET /api/v4/projects/{project}/merge_requests/{iid}/notes", func(w http.ResponseWriter, r *http.Request) {
		s.withMR(w, r, func(m MR) any { return nonNil(m.Notes) })
	})
	mux.HandleFunc("GET /api/v4/projects/{project}/repository/commits/{sha}/refs", func(w http.ResponseWriter, r *http.Request) {
		type ref struct {
			Type string `json:"type"`
			Name string `json:"name"`
		}
		out := []ref{}
		for _, m := range s.allMRs() {
			if strconv.Itoa(m.ProjectID) == r.PathValue("project") && m.MergedSHA() == r.PathValue("sha") {
				for _, b := range m.Branches {
					out = append(out, ref{"branch", b})
				}
			}
		}
		write(w, out)
	})

	mux.HandleFunc("GET /users/me", func(w http.ResponseWriter, r *http.Request) {
		write(w, map[string]string{"login": s.YouTrackLogin})
	})
	mux.HandleFunc("GET /api/issues", func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		out := make([]youtrack.Issue, 0, len(s.issues))
		for _, i := range s.issues {
			out = append(out, i.Issue)
		}
		s.mu.Unlock()
		write(w, out)
	})
	mux.HandleFunc("GET /api/issues/{id}/comments", func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		defer s.mu.Unlock()
		for _, i := range s.issues {
			if i.ID == r.PathValue("id") {
				write(w, nonNil(i.Comments))
				return
			}
		}
		http.NotFound(w, r)
	})
	mux.HandleFunc("POST /api/commands", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Query  string `json:"query"`
			Issues []struct {
				ID string `json:"idReadable"`
			} `json:"issues"`
		}
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil || len(in.Issues) != 1 {
			http.Error(w, "bad command", http.StatusBadRequest)
			return
		}
		s.mu.Lock()
		s.commands = append(s.commands, Command{Query: in.Query, IssueID: in.Issues[0].ID})
		s.mu.Unlock()
		write(w, map[string]any{})
	})

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		line := r.Method + " " + r.URL.Path
		if r.URL.RawQuery != "" {
			line += "?" + r.URL.RawQuery
		}
		s.requests = append(s.requests, line)
		code := s.failCode
		s.mu.Unlock()
		if code != 0 {
			http.Error(w, fmt.Sprintf("injected failure %d", code), code)
			return
		}
		mux.ServeHTTP(w, r)
	})
}

func (s *Server) allMRs() []MR {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append(append([]MR(nil), s.mine...), s.reviews...)
}

func (s *Server) withMR(w http.ResponseWriter, r *http.Request, fn func(MR) any) {
	for _, m := range s.allMRs() {
		if strconv.Itoa(m.ProjectID) == r.PathValue("project") && strconv.Itoa(m.IID) == r.PathValue("iid") {
			write(w, fn(m))
			return
		}
	}
	http.NotFound(w, r)
}

func write(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func nonNil[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}
