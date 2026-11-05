package config

import (
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestMain(m *testing.M) {
	log.SetOutput(io.Discard)
	os.Exit(m.Run())
}

// withEnvTokens задаёт оба env-токена: тогда Load не ходит в настоящую Связку ключей.
func withEnvTokens(t *testing.T) {
	t.Setenv("TASKWATCH_GITLAB_TOKEN", "gl-env")
	t.Setenv("TASKWATCH_YOUTRACK_TOKEN", "yt-env")
}

func writeYAML(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

const fullYAML = `
poll_interval: 5m
gitlab:
  url: https://gitlab.example.com
  username: ivanov
youtrack:
  url: https://yt.example.com
  login: ivanov
  query: "for: me"
rules:
  required_approvals: 3
  review_state: Code Review
  mr_behind_commits: 7
`

func TestDefault(t *testing.T) {
	c := Default("/x/y/config.yaml")
	if c.PollInterval != 2*time.Minute || c.StatePath != "/x/y/state.json" ||
		c.Rules.RequiredApprovals != 2 || c.Rules.ReleaseBranchPrefix != "release/" ||
		c.Rules.MRLookback != 14*24*time.Hour || c.Configured() {
		t.Errorf("default = %+v", c)
	}
	if err := c.Validate(); err != nil {
		t.Errorf("default must be valid: %v", err)
	}
}

func TestLoad(t *testing.T) {
	withEnvTokens(t)
	path := writeYAML(t, fullYAML)
	c, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if c.PollInterval != 5*time.Minute || c.GitLab.Token != "gl-env" || c.YouTrack.Token != "yt-env" ||
		c.Rules.RequiredApprovals != 3 || c.Rules.MRBehindCommits != 7 {
		t.Errorf("loaded = %+v", c)
	}
	// Незаданное в yaml берётся из Default.
	if c.Rules.ReleaseBranchPrefix != "release/" || c.Rules.MRLookback != 14*24*time.Hour ||
		c.StatePath != filepath.Join(filepath.Dir(path), "state.json") {
		t.Errorf("defaults not merged: %+v", c)
	}
	if !c.GitLabOK() || !c.YouTrackOK() || !c.Configured() {
		t.Error("both services must be OK")
	}
}

func TestLoadErrors(t *testing.T) {
	withEnvTokens(t)
	if _, err := Load(filepath.Join(t.TempDir(), "missing.yaml")); err == nil || !strings.Contains(err.Error(), "read config") {
		t.Errorf("missing: %v", err)
	}
	if _, err := Load(writeYAML(t, "gitlab: [")); err == nil || !strings.Contains(err.Error(), "parse config") {
		t.Errorf("broken yaml: %v", err)
	}
}

// Load отдаёт конфиг даже вместе с ошибкой валидации. Env-токен для
// незаполненного сервиса делает его «половинчатым».
func TestLoadValidationErrorKeepsConfig(t *testing.T) {
	withEnvTokens(t)
	c, err := Load(writeYAML(t, "gitlab:\n  url: https://gl\n  username: me\n"))
	if c == nil || c.GitLab.URL != "https://gl" {
		t.Fatalf("config = %+v", c)
	}
	if err == nil || !strings.Contains(err.Error(), "youtrack.url is required") {
		t.Errorf("err = %v", err)
	}
}

func TestValidate(t *testing.T) {
	cases := []struct {
		name    string
		mutate  func(*Config)
		wantErr []string
	}{
		{"empty is fine", func(*Config) {}, nil},
		{"full gitlab", func(c *Config) { c.GitLab = GitLab{URL: "u", Token: "t", Username: "n"} }, nil},
		{"half gitlab", func(c *Config) { c.GitLab = GitLab{URL: "u"} }, []string{"gitlab.token is required", "gitlab.username is required"}},
		{"half youtrack", func(c *Config) { c.YouTrack = YouTrack{URL: "u", Token: "t", Login: "l"} }, []string{"youtrack.query is required"}},
		{"poll interval", func(c *Config) { c.PollInterval = 0 }, []string{"poll_interval must be positive"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := Default("/x/config.yaml")
			tc.mutate(c)
			err := c.Validate()
			if len(tc.wantErr) == 0 {
				if err != nil {
					t.Errorf("err = %v", err)
				}
				return
			}
			if err == nil {
				t.Fatal("want error")
			}
			for _, w := range tc.wantErr {
				if !strings.Contains(err.Error(), w) {
					t.Errorf("err %q lacks %q", err, w)
				}
			}
		})
	}
}

func TestServiceOK(t *testing.T) {
	c := Default("/x/config.yaml")
	c.GitLab = GitLab{URL: "u", Token: "t", Username: "n"}
	if !c.GitLabOK() || c.YouTrackOK() || !c.Configured() {
		t.Error("gitlab only")
	}
	c.YouTrack = YouTrack{URL: "u", Token: "t", Login: "l"}
	if c.YouTrackOK() {
		t.Error("youtrack without query is not OK")
	}
}

func TestSave(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nested", "config.yaml")
	c := Default(path)
	c.GitLab = GitLab{URL: "https://gl", Token: "secret-gl", Username: "me"}
	c.YouTrack = YouTrack{URL: "https://yt", Token: "secret-yt", Login: "me", Query: "for: me"}
	if err := Save(path, c); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	s := string(data)
	if strings.Contains(s, "secret") {
		t.Errorf("token written to yaml:\n%s", s)
	}
	if strings.Contains(s, "state_path") {
		t.Errorf("default state_path written:\n%s", s)
	}
	if c.GitLab.Token != "secret-gl" {
		t.Error("Save mutated the config")
	}
	info, _ := os.Stat(path)
	if info.Mode().Perm() != 0o600 {
		t.Errorf("perm = %v", info.Mode().Perm())
	}

	c.StatePath = "/custom/state.json"
	if err := Save(path, c); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(path); !strings.Contains(string(data), "state_path: /custom/state.json") {
		t.Errorf("custom state_path lost:\n%s", data)
	}

	withEnvTokens(t)
	back, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if back.GitLab.URL != "https://gl" || back.StatePath != "/custom/state.json" || back.PollInterval != c.PollInterval {
		t.Errorf("roundtrip = %+v", back)
	}
}
