package settings

import (
	"context"
	"errors"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"taskwatch/internal/config"
	"taskwatch/internal/integrations/youtrack"
	"taskwatch/internal/testutil/fakeapi"
	"taskwatch/internal/testutil/fakesecrets"
)

func TestMain(m *testing.M) {
	log.SetOutput(io.Discard)
	os.Exit(m.Run())
}

func writeYAML(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

const gitlabYAML = "gitlab:\n  url: https://gl.example\n  username: me\n"

func TestLoadFillsTokensFromStore(t *testing.T) {
	store := fakesecrets.New(map[string]string{AccountGitLab: "glpat"})
	cfg, err := New(writeYAML(t, gitlabYAML), store).Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.GitLab.Token != "glpat" || !cfg.GitLabOK() || cfg.YouTrack.Token != "" {
		t.Errorf("cfg = %+v", cfg)
	}
}

// Регрессия: недоступный токен не должен превращать конфиг в пустой — форма
// настроек должна показать адреса, логины и правила.
func TestLoadKeepsConfigWhenTokenUnavailable(t *testing.T) {
	store := fakesecrets.New(nil)
	store.GetErr[AccountGitLab] = errors.New("запись создана другой сборкой приложения")
	cfg, err := New(writeYAML(t, gitlabYAML+"rules:\n  required_approvals: 5\n"), store).Load()
	if err == nil || !strings.Contains(err.Error(), "gitlab.token is required") {
		t.Errorf("err = %v", err)
	}
	if cfg == nil || cfg.GitLab.URL != "https://gl.example" || cfg.GitLab.Username != "me" || cfg.Rules.RequiredApprovals != 5 {
		t.Errorf("cfg = %+v", cfg)
	}
}

func TestLoadMissingFile(t *testing.T) {
	cfg, err := New(filepath.Join(t.TempDir(), "nope.yaml"), fakesecrets.New(nil)).Load()
	if cfg != nil || err == nil {
		t.Errorf("cfg = %v, err = %v", cfg, err)
	}
}

func TestLoadMigratesLegacyTokens(t *testing.T) {
	path := writeYAML(t, "gitlab:\n  url: https://gl\n  username: me\n  token: glpat-legacy\n")
	store := fakesecrets.New(map[string]string{AccountGitLab: "stale"})
	cfg, err := New(path, store).Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.GitLab.Token != "glpat-legacy" || store.All()[AccountGitLab] != "glpat-legacy" {
		t.Errorf("token = %q, store = %v", cfg.GitLab.Token, store.All())
	}
	data, _ := os.ReadFile(path)
	if strings.Contains(string(data), "glpat") || !strings.Contains(string(data), "https://gl") {
		t.Errorf("yaml after migration:\n%s", data)
	}
}

func TestLoadMigrationFailureKeepsYAML(t *testing.T) {
	path := writeYAML(t, "gitlab:\n  url: https://gl\n  username: me\n  token: glpat-a\n"+
		"youtrack:\n  url: https://yt\n  login: me\n  query: q\n  token: perm-b\n")
	store := fakesecrets.New(nil)
	store.SetErr[AccountYouTrack] = errors.New("keychain locked")
	cfg, err := New(path, store).Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.GitLab.Token != "glpat-a" || cfg.YouTrack.Token != "perm-b" {
		t.Errorf("tokens = %q %q", cfg.GitLab.Token, cfg.YouTrack.Token)
	}
	// Один токен не переехал — файл не переписываем, иначе он бы пропал.
	data, _ := os.ReadFile(path)
	if !strings.Contains(string(data), "perm-b") {
		t.Errorf("unmigrated token lost from yaml:\n%s", data)
	}
}

func TestWithEnv(t *testing.T) {
	store := fakesecrets.New(map[string]string{AccountGitLab: "from-store", AccountYouTrack: "yt-store"})
	store.GetErr[AccountYouTrack] = nil
	t.Setenv("TASKWATCH_GITLAB_TOKEN", "from-env")
	s := WithEnv(store)
	if v, _ := s.Get(AccountGitLab); v != "from-env" {
		t.Errorf("env must win: %q", v)
	}
	if v, _ := s.Get(AccountYouTrack); v != "yt-store" {
		t.Errorf("no env — store: %q", v)
	}
	// Когда env задан, хранилище не читается вовсе.
	store.GetErr[AccountGitLab] = errors.New("must not be called")
	if v, err := s.Get(AccountGitLab); err != nil || v != "from-env" {
		t.Errorf("store consulted despite env: %q %v", v, err)
	}
	if err := s.Set(AccountGitLab, "new"); err != nil || store.All()[AccountGitLab] != "new" {
		t.Error("Set must reach the store")
	}
	if EnvVar(AccountYouTrack) != "TASKWATCH_YOUTRACK_TOKEN" {
		t.Error("env var name")
	}
}

func cfgWith(glURL, glToken string) *config.Config {
	c := config.Default("/x/config.yaml")
	c.GitLab = config.GitLab{URL: glURL, Token: glToken, Username: "me"}
	return c
}

func TestResolve(t *testing.T) {
	cases := []struct {
		name      string
		cur, next *config.Config
		wantToken string
		wantErr   string
	}{
		{"keeps token for the same URL", cfgWith("https://gl.example", "old"), cfgWith("https://gl.example", ""), "old", ""},
		{"same URL modulo case and slash", cfgWith("https://GL.example/", "old"), cfgWith("https://gl.example", ""), "old", ""},
		{"new token wins", cfgWith("https://gl.example", "old"), cfgWith("https://gl.example", "new"), "new", ""},
		{"changed URL needs a new token", cfgWith("https://gl.example", "old"), cfgWith("https://evil.example", ""), "", "адрес изменился"},
		{"changed URL with a new token", cfgWith("https://gl.example", "old"), cfgWith("https://gl2.example", "new"), "new", ""},
		{"first setup without token is incomplete", cfgWith("", ""), cfgWith("https://gl.example", ""), "", "gitlab.token is required"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := Resolve(tc.cur, tc.next)
			if tc.wantErr == "" && err != nil || tc.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tc.wantErr)) {
				t.Fatalf("err = %v, want %q", err, tc.wantErr)
			}
			if tc.next.GitLab.Token != tc.wantToken {
				t.Errorf("token = %q, want %q", tc.next.GitLab.Token, tc.wantToken)
			}
		})
	}

	// Очищенный сервис выключается: токен не тянется, конфиг валиден.
	off := config.Default("/x/config.yaml")
	if err := Resolve(cfgWith("https://gl.example", "old"), off); err != nil || off.GitLab.Token != "" {
		t.Errorf("disable service: err = %v, token = %q", err, off.GitLab.Token)
	}
}

func TestSave(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	store := fakesecrets.New(map[string]string{AccountGitLab: "old"})
	svc := New(path, store)

	cur := cfgWith("https://gl.example", "old")
	next := cfgWith("https://gl.example", "old")
	next.Rules.RequiredApprovals = 4
	store.SetErr[AccountGitLab] = errors.New("unchanged token must not be written")
	if err := svc.Save(cur, next); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	if !strings.Contains(string(data), "required_approvals: 4") || strings.Contains(string(data), "old") {
		t.Errorf("yaml:\n%s", data)
	}

	delete(store.SetErr, AccountGitLab)
	next.GitLab.Token = "rotated"
	if err := svc.Save(cur, next); err != nil || store.All()[AccountGitLab] != "rotated" {
		t.Errorf("rotated token: err = %v, store = %v", err, store.All())
	}

	store.SetErr[AccountGitLab] = errors.New("locked")
	next.GitLab.Token = "again"
	if err := svc.Save(cur, next); err == nil || !strings.Contains(err.Error(), "store token") {
		t.Errorf("err = %v", err)
	}
}

func TestClearSecrets(t *testing.T) {
	store := fakesecrets.New(map[string]string{AccountGitLab: "a", AccountYouTrack: "b", "other": "c"})
	if err := New("/x", store).ClearSecrets(); err != nil {
		t.Fatal(err)
	}
	if got := store.All(); len(got) != 1 || got["other"] != "c" {
		t.Errorf("store = %v", got)
	}
}

func TestProbe(t *testing.T) {
	srv := fakeapi.New(t)
	srv.GitLabUser = "gl-me"
	cfg := cfgWith(srv.URL, "tok")
	gl, yt := Test(context.Background(), cfg)
	if gl.Err != nil || gl.User != "gl-me" || gl.Skipped || !yt.Skipped {
		t.Errorf("gl = %+v, yt = %+v", gl, yt)
	}

	cfg.YouTrack = config.YouTrack{URL: srv.URL, Token: "t", Login: "me", Query: "q"}
	srv.FailWith(401)
	gl, yt = Test(context.Background(), cfg)
	if gl.Err == nil || yt.Err == nil || !strings.Contains(gl.Err.Error(), "401") {
		t.Errorf("gl = %+v, yt = %+v", gl, yt)
	}
}

func TestBoards(t *testing.T) {
	srv := fakeapi.New(t)
	srv.SetAgiles(
		youtrack.Agile{Name: "team b", CurrentSprint: &youtrack.Sprint{Name: "Sprint 7"}},
		youtrack.Agile{Name: "Kanban"},
		youtrack.Agile{Name: "Team A", CurrentSprint: &youtrack.Sprint{Name: "Sprint 42"}},
	)

	cfg := config.Default("/x/config.yaml")
	if _, err := Boards(context.Background(), cfg); !errors.Is(err, ErrNoYouTrack) {
		t.Errorf("without YouTrack: err = %v", err)
	}

	// Логин и запрос не нужны: их как раз подбирают.
	cfg.YouTrack = config.YouTrack{URL: srv.URL, Token: "t"}
	got, err := Boards(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	want := []Board{
		{Name: "Team A", Sprint: "Sprint 42", Query: "for: me Board Team A: {Current sprint}"},
		{Name: "team b", Sprint: "Sprint 7", Query: "for: me Board team b: {Current sprint}"},
	}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("boards = %+v", got)
	}

	srv.FailWith(401)
	if _, err := Boards(context.Background(), cfg); err == nil || !strings.Contains(err.Error(), "401") {
		t.Errorf("failure: err = %v", err)
	}
}
