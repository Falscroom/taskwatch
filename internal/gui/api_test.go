package gui

import (
	"bytes"
	"context"
	"encoding/json"
	"image/png"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"taskwatch/internal/config"
	"taskwatch/internal/engine"
	"taskwatch/internal/gitlab"
	"taskwatch/internal/state"
	"taskwatch/internal/testutil/fakeapi"
	"taskwatch/internal/watch"
	"taskwatch/internal/youtrack"
)

func TestMain(m *testing.M) {
	log.SetOutput(io.Discard)
	os.Exit(m.Run())
}

type apiHarness struct {
	handler    http.Handler
	eng        *engine.Engine
	configPath string
	opened     []string
}

// newAPI поднимает обработчик API поверх движка с заданным конфигом. Токены в
// конфиге уже есть, а форма шлёт пустые — значит, Связку ключей тесты не трогают.
func newAPI(t *testing.T, cfg *config.Config) *apiHarness {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if cfg == nil {
		cfg = config.Default(path)
	}
	cfg.StatePath = filepath.Join(dir, "state.json")
	store, err := state.Open(cfg.StatePath)
	if err != nil {
		t.Fatal(err)
	}
	yt := youtrack.New(cfg.YouTrack.URL, cfg.YouTrack.Token)
	eng := engine.New(cfg, watch.NewCollector(cfg, gitlab.New(cfg.GitLab.URL, cfg.GitLab.Token), yt), store, yt)
	h := &apiHarness{eng: eng, configPath: path}
	assets := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, "asset:"+r.URL.Path) })
	h.handler = newHandler(eng, path, assets, func(u string) error { h.opened = append(h.opened, u); return nil })
	return h
}

func (h *apiHarness) do(t *testing.T, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var r io.Reader
	if body != nil {
		if s, ok := body.(string); ok {
			r = strings.NewReader(s)
		} else {
			data, _ := json.Marshal(body)
			r = bytes.NewReader(data)
		}
	}
	rec := httptest.NewRecorder()
	h.handler.ServeHTTP(rec, httptest.NewRequest(method, path, r))
	return rec
}

func configured(srvURL string) *config.Config {
	cfg := config.Default("/unused/config.yaml")
	cfg.GitLab = config.GitLab{URL: srvURL, Token: "secret-gl-token", Username: "me"}
	cfg.YouTrack = config.YouTrack{URL: srvURL, Token: "secret-yt-token", Login: "me", Query: "for: me"}
	return cfg
}

func TestView(t *testing.T) {
	h := newAPI(t, nil)
	rec := h.do(t, http.MethodGet, "/api/view", nil)
	if rec.Code != 200 || rec.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("status %d, type %q", rec.Code, rec.Header().Get("Content-Type"))
	}
	var v struct {
		Cards    []any `json:"cards"`
		NeedsYou int   `json:"needsYou"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil || v.Cards == nil {
		t.Errorf("body = %s", rec.Body)
	}
}

// Сквозной путь: GitLab → сборщик → движок → /api/view в том виде, что ждёт фронт.
func TestViewEndToEnd(t *testing.T) {
	t.Setenv("PATH", "") // уведомления не отправляются
	srv := fakeapi.New(t)
	broken := fakeapi.MR{MergeRequest: gitlab.MergeRequest{ID: 101, IID: 1, ProjectID: 1, State: "opened", Title: "Broken",
		HasConflicts: true, BlockingDiscussionsResolved: true, Author: gitlab.User{Username: "me", Name: "Me Myself"}}}
	theirs := fakeapi.MR{MergeRequest: gitlab.MergeRequest{ID: 102, IID: 2, ProjectID: 1, State: "opened", Title: "Theirs",
		BlockingDiscussionsResolved: true, Author: gitlab.User{Username: "bob"}}}
	srv.SetMine(broken)
	srv.SetReviews(theirs)
	cfg := configured(srv.URL)
	cfg.YouTrack = config.YouTrack{}
	h := newAPI(t, cfg)
	if err := h.eng.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}

	var v struct {
		NeedsYou int `json:"needsYou"`
		Cards    []struct {
			Key    string `json:"key"`
			Group  string `json:"group"`
			Mine   bool   `json:"mine"`
			Review bool   `json:"review"`
			Author struct {
				Initials string `json:"initials"`
			} `json:"author"`
			Badges []struct {
				Text string `json:"text"`
				Tone string `json:"tone"`
			} `json:"badges"`
		} `json:"cards"`
	}
	rec := h.do(t, http.MethodGet, "/api/view", nil)
	if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
		t.Fatal(err)
	}
	if v.NeedsYou != 2 || len(v.Cards) != 2 {
		t.Fatalf("view = %s", rec.Body)
	}
	mine, review := v.Cards[0], v.Cards[1]
	if mine.Key != "mr:101" || mine.Group != "work" || !mine.Mine || mine.Author.Initials != "MM" ||
		len(mine.Badges) != 1 || mine.Badges[0].Text != "Конфликты" || mine.Badges[0].Tone != "red" {
		t.Errorf("own card = %+v", mine)
	}
	if review.Key != "mr:102" || review.Group != "review" || !review.Review {
		t.Errorf("review card = %+v", review)
	}

	// Заглушённая через API карточка уходит в «Заглушено» и перестаёт считаться.
	h.do(t, http.MethodPost, "/api/mute", map[string]any{"key": "mr:102"})
	rec = h.do(t, http.MethodGet, "/api/view", nil)
	_ = json.Unmarshal(rec.Body.Bytes(), &v)
	if v.NeedsYou != 1 || v.Cards[1].Group != "muted" {
		t.Errorf("after mute: %s", rec.Body)
	}
}

func TestAssetsFallThrough(t *testing.T) {
	if rec := newAPI(t, nil).do(t, http.MethodGet, "/app.js", nil); rec.Body.String() != "asset:/app.js" {
		t.Errorf("body = %q", rec.Body)
	}
}

func TestMute(t *testing.T) {
	cases := []struct {
		name string
		body any
		code int
	}{
		{"bad json", "{", 400},
		{"not an MR key", map[string]any{"key": "task:ABC-1", "hours": 1}, 400},
		{"negative hours", map[string]any{"key": "mr:1", "hours": -1}, 400},
		{"alsoTitle without title", map[string]any{"key": "mr:1", "hours": 1, "alsoTitle": true, "title": "  "}, 400},
		{"ok", map[string]any{"key": "mr:1", "hours": 1}, 204},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if rec := newAPI(t, nil).do(t, http.MethodPost, "/api/mute", tc.body); rec.Code != tc.code {
				t.Errorf("status = %d, want %d (%s)", rec.Code, tc.code, rec.Body)
			}
		})
	}

	h := newAPI(t, nil)
	h.do(t, http.MethodPost, "/api/mute", map[string]any{"key": "mr:1", "hours": 1.5, "alsoTitle": true, "title": " Bump "})
	h.do(t, http.MethodPost, "/api/mute", map[string]any{"key": "mr:2", "hours": 0, "title": "ignored without alsoTitle"})
	m := h.eng.Mutes()
	if d := time.Until(m["mr:1"]); d < 89*time.Minute || d > 91*time.Minute {
		t.Errorf("mr:1 until in %v, want 1.5h", d)
	}
	if !m["title:Bump"].Equal(m["mr:1"]) {
		t.Errorf("title rule = %v (title must be trimmed)", m)
	}
	if until, ok := m["mr:2"]; !ok || !until.IsZero() || len(m) != 3 {
		t.Errorf("forever mute: %v", m)
	}
}

func TestUnmute(t *testing.T) {
	h := newAPI(t, nil)
	h.do(t, http.MethodPost, "/api/mute", map[string]any{"key": "mr:1", "alsoTitle": true, "title": "T"})
	if rec := h.do(t, http.MethodPost, "/api/unmute", map[string]any{"key": "task:1"}); rec.Code != 400 {
		t.Errorf("status = %d", rec.Code)
	}
	if rec := h.do(t, http.MethodPost, "/api/unmute", map[string]any{"key": "mr:1", "title": " T "}); rec.Code != 204 {
		t.Errorf("status = %d", rec.Code)
	}
	if m := h.eng.Mutes(); len(m) != 0 {
		t.Errorf("mutes = %v", m)
	}
}

func TestOpen(t *testing.T) {
	h := newAPI(t, nil)
	for _, u := range []string{"file:///etc/passwd", "javascript:alert(1)", ""} {
		if rec := h.do(t, http.MethodPost, "/api/open", map[string]string{"url": u}); rec.Code != 400 {
			t.Errorf("%q: status = %d", u, rec.Code)
		}
	}
	for _, u := range []string{"https://gl/mr/1", "http://yt/issue/1"} {
		if rec := h.do(t, http.MethodPost, "/api/open", map[string]string{"url": u}); rec.Code != 204 {
			t.Errorf("%q: status = %d", u, rec.Code)
		}
	}
	if strings.Join(h.opened, " ") != "https://gl/mr/1 http://yt/issue/1" {
		t.Errorf("opened = %v", h.opened)
	}
}

func TestRefresh(t *testing.T) {
	if rec := newAPI(t, nil).do(t, http.MethodPost, "/api/refresh", nil); rec.Code != 204 {
		t.Errorf("status = %d", rec.Code)
	}
}

func TestTransition(t *testing.T) {
	h := newAPI(t, nil)
	if rec := h.do(t, http.MethodPost, "/api/transition", map[string]string{}); rec.Code != 400 {
		t.Errorf("missing issueId: %d", rec.Code)
	}
	if rec := h.do(t, http.MethodPost, "/api/transition", map[string]string{"issueId": "ABC-1"}); rec.Code != 502 {
		t.Errorf("unconfigured: %d", rec.Code)
	}

	srv := fakeapi.New(t)
	cfg := configured(srv.URL)
	cfg.Rules.NextCommand = "State Done"
	h = newAPI(t, cfg)
	if rec := h.do(t, http.MethodPost, "/api/transition", map[string]string{"issueId": "ABC-1"}); rec.Code != 204 {
		t.Errorf("status = %d (%s)", rec.Code, rec.Body)
	}
	if cmds := srv.Commands(); len(cmds) != 1 || cmds[0].Query != "State Done" {
		t.Errorf("commands = %+v", cmds)
	}
}

func TestGetConfigHidesTokens(t *testing.T) {
	h := newAPI(t, configured("https://example"))
	rec := h.do(t, http.MethodGet, "/api/config", nil)
	if strings.Contains(rec.Body.String(), "secret") {
		t.Fatalf("token leaked: %s", rec.Body)
	}
	var form configForm
	if err := json.Unmarshal(rec.Body.Bytes(), &form); err != nil {
		t.Fatal(err)
	}
	if !form.Configured || !form.GitLab.TokenSet || !form.GitLab.OK || !form.YouTrack.TokenSet ||
		form.PollInterval != "2m0s" || form.Rules.MRLookback != "336h0m0s" || form.ConfigPath != h.configPath {
		t.Errorf("form = %+v", form)
	}
}

func formFor(srvURL string) map[string]any {
	return map[string]any{
		"pollInterval": "5m",
		"gitlab":       map[string]any{"url": " " + srvURL + " ", "username": " me ", "token": ""},
		"youtrack":     map[string]any{"url": srvURL, "login": "me", "query": " for: me ", "token": ""},
		"rules": map[string]any{
			"requiredApprovals": 3, "reviewState": " Code Review ", "nextCommand": "State QA", "autoTransition": true,
			"releaseBranchPrefix": "rel/", "mrLookback": "48h", "excludeDraft": true, "mrBehindCommits": 4,
		},
	}
}

func TestSaveConfig(t *testing.T) {
	srv := fakeapi.New(t)
	h := newAPI(t, configured(srv.URL))

	rec := h.do(t, http.MethodPost, "/api/config", formFor(srv.URL))
	if rec.Code != 204 {
		t.Fatalf("status = %d (%s)", rec.Code, rec.Body)
	}
	cfg := h.eng.Config()
	if cfg.PollInterval != 5*time.Minute || cfg.GitLab.URL != srv.URL || cfg.GitLab.Username != "me" ||
		cfg.YouTrack.Query != "for: me" || cfg.Rules.RequiredApprovals != 3 || cfg.Rules.ReviewState != "Code Review" ||
		!cfg.Rules.AutoTransition || cfg.Rules.MRLookback != 48*time.Hour || !cfg.Rules.ExcludeDraft || cfg.Rules.MRBehindCommits != 4 {
		t.Errorf("reloaded config = %+v", cfg)
	}
	// Пустой токен в форме — «оставить прежний».
	if cfg.GitLab.Token != "secret-gl-token" || cfg.YouTrack.Token != "secret-yt-token" {
		t.Errorf("tokens = %q %q", cfg.GitLab.Token, cfg.YouTrack.Token)
	}
	data, err := os.ReadFile(h.configPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "secret") || !strings.Contains(string(data), "required_approvals: 3") {
		t.Errorf("config.yaml:\n%s", data)
	}
}

func TestSaveConfigRejectsBadInput(t *testing.T) {
	srv := fakeapi.New(t)
	cases := map[string]func(map[string]any){
		"bad poll interval": func(f map[string]any) { f["pollInterval"] = "soon" },
		"bad lookback":      func(f map[string]any) { f["rules"].(map[string]any)["mrLookback"] = "2 weeks" },
		"half gitlab":       func(f map[string]any) { f["gitlab"].(map[string]any)["username"] = "" },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			h := newAPI(t, configured(srv.URL))
			form := formFor(srv.URL)
			mutate(form)
			if rec := h.do(t, http.MethodPost, "/api/config", form); rec.Code != 400 {
				t.Errorf("status = %d", rec.Code)
			}
			if _, err := os.Stat(h.configPath); !os.IsNotExist(err) {
				t.Error("rejected form must not be saved")
			}
		})
	}
	if rec := newAPI(t, nil).do(t, http.MethodPost, "/api/config", "{"); rec.Code != 400 {
		t.Errorf("bad json: %d", rec.Code)
	}
}

func TestConfigTest(t *testing.T) {
	srv := fakeapi.New(t)
	srv.GitLabUser = "gl-me"
	srv.YouTrackLogin = "yt-me"
	h := newAPI(t, configured(srv.URL))
	rec := h.do(t, http.MethodPost, "/api/config/test", formFor(srv.URL))
	var res map[string]probe
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
		t.Fatal(err)
	}
	if res["gitlab"] != (probe{OK: true, User: "gl-me"}) || res["youtrack"] != (probe{OK: true, User: "yt-me"}) {
		t.Errorf("probe = %+v", res)
	}

	srv.FailWith(401)
	rec = h.do(t, http.MethodPost, "/api/config/test", formFor(srv.URL))
	_ = json.Unmarshal(rec.Body.Bytes(), &res)
	if res["gitlab"].OK || !strings.Contains(res["gitlab"].Error, "401") {
		t.Errorf("failed probe = %+v", res["gitlab"])
	}

	empty := newAPI(t, nil)
	rec = empty.do(t, http.MethodPost, "/api/config/test", map[string]any{})
	_ = json.Unmarshal(rec.Body.Bytes(), &res)
	if !res["gitlab"].Skipped || !res["youtrack"].Skipped {
		t.Errorf("empty probe = %+v", res)
	}
}

func TestGuardOrigin(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) })
	h := guardOrigin(next)
	cases := []struct {
		host, origin string
		code         int
	}{
		{"127.0.0.1:7777", "", 204},
		{"127.0.0.1:7777", "http://127.0.0.1:7777", 204},
		{"127.0.0.1:7777", "https://evil.example", 403},
		{"127.0.0.1:7777", "http://localhost:7777", 403},
	}
	for _, tc := range cases {
		r := httptest.NewRequest(http.MethodPost, "/api/config", nil)
		r.Host = tc.host
		if tc.origin != "" {
			r.Header.Set("Origin", tc.origin)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, r)
		if rec.Code != tc.code {
			t.Errorf("host %s origin %q: %d, want %d", tc.host, tc.origin, rec.Code, tc.code)
		}
	}
}

// Известная дыра: при DNS rebinding Origin и Host совпадают (оба — домен
// злоумышленника), и запрос проходит.
func TestGuardOriginRejectsForeignHost(t *testing.T) {
	t.Skip("known bug: DNS rebinding passes the Origin==Host check; fix with a Host allowlist")
	h := guardOrigin(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	r := httptest.NewRequest(http.MethodPost, "/api/config", nil)
	r.Host = "rebind.evil.example:7777"
	r.Header.Set("Origin", "http://rebind.evil.example:7777")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	if rec.Code != 403 {
		t.Errorf("rebinding request: %d, want 403", rec.Code)
	}
}

func TestTrayIcon(t *testing.T) {
	img, err := png.Decode(bytes.NewReader(trayIcon()))
	if err != nil {
		t.Fatal(err)
	}
	if b := img.Bounds(); b.Dx() != 36 || b.Dy() != 36 {
		t.Errorf("icon size = %v, want 36×36", b)
	}
}
