package config

import (
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	"taskwatch/internal/keychain"

	"gopkg.in/yaml.v3"
)

type GitLab struct {
	URL      string `yaml:"url"`
	Token    string `yaml:"token"`
	Username string `yaml:"username"`
}

type YouTrack struct {
	URL   string `yaml:"url"`
	Token string `yaml:"token"`
	Login string `yaml:"login"`
	// Запрос задач, которые мониторим, например: "for: me Sprint: {Current sprint}"
	Query string `yaml:"query"`
}

type Rules struct {
	RequiredApprovals int `yaml:"required_approvals"`
	// Статус, в котором таска ждёт апрувов
	ReviewState string `yaml:"review_state"`
	// YouTrack-команда для перевода после апрувов, например "State Ready for QA"
	NextCommand    string `yaml:"next_command"`
	AutoTransition bool   `yaml:"auto_transition"`
	// Ветки, попадание в которые считаем «влито в релиз»
	ReleaseBranchPrefix string `yaml:"release_branch_prefix"`
	// За какой период смотреть свои MR (в т.ч. смёрженные)
	MRLookback time.Duration `yaml:"mr_lookback"`
	// Не показывать и не уведомлять о draft-MR
	ExcludeDraft bool `yaml:"exclude_draft"`
	// Подсвечивать свой MR, отставший от целевой ветки минимум на столько
	// коммитов: бейдж «Отстаёт на N» и перенос в «требуют работы». 0 — выключено.
	MRBehindCommits int `yaml:"mr_behind_commits"`
}

type Config struct {
	PollInterval time.Duration `yaml:"poll_interval"`
	StatePath    string        `yaml:"state_path,omitempty"`
	GitLab       GitLab        `yaml:"gitlab"`
	YouTrack     YouTrack      `yaml:"youtrack"`
	Rules        Rules         `yaml:"rules"`
}

func DefaultPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "taskwatch", "config.yaml")
}

// Default — пустой конфиг с дефолтными правилами; state.json лежит рядом с config-файлом.
func Default(path string) *Config {
	return &Config{
		PollInterval: 2 * time.Minute,
		StatePath:    filepath.Join(filepath.Dir(path), "state.json"),
		Rules: Rules{
			RequiredApprovals:   2,
			ReleaseBranchPrefix: "release/",
			MRLookback:          14 * 24 * time.Hour,
		},
	}
}

// Load читает yaml; токены берутся из Связки ключей (или TASKWATCH_*_TOKEN,
// они главнее — удобно для тестов). Открытые токены в yaml — наследие старых
// версий: при первом запуске переезжают в хранилище, из файла затираются.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}

	cfg := Default(path)
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}

	// Миграция: открытый токен в yaml → Связка ключей, из файла прочерк.
	// Хранилище недоступно — оставляем как есть: лучше открытый токен, чем
	// отвалившийся монитор.
	migrated := false
	for account, token := range map[string]*string{
		"gitlab":   &cfg.GitLab.Token,
		"youtrack": &cfg.YouTrack.Token,
	} {
		if *token == "" {
			continue
		}
		if err := keychain.Save(account, *token); err != nil {
			log.Printf("config: не смог спрятать токен %s в связку ключей (%v) — оставил в yaml", account, err)
			continue
		}
		*token = ""
		migrated = true
		log.Printf("config: токен %s перенесён из config.yaml в связку ключей", account)
	}
	if migrated {
		if err := Save(path, cfg); err != nil {
			// Не критично: при следующем запуске миграция повторится тем же значением.
			log.Printf("config: токены спрятаны, но yaml не перезаписан: %v", err)
		}
	}

	// Токены достаём из хранилища; env-переменные главнее — когда они заданы,
	// хранилище не читаем вовсе: экономим время и не провоцируем лишние вопросы.
	for _, s := range []struct {
		name, env string
		dst       *string
	}{
		{"gitlab", "TASKWATCH_GITLAB_TOKEN", &cfg.GitLab.Token},
		{"youtrack", "TASKWATCH_YOUTRACK_TOKEN", &cfg.YouTrack.Token},
	} {
		if *s.dst != "" || os.Getenv(s.env) != "" {
			continue
		}
		v, err := keychain.Load(s.name)
		switch {
		case errors.Is(err, keychain.ErrAccessDenied):
			// Запись осталась от предыдущей сборки: строгий ACL пускает к ней
			// только её создателя. Подсказываем лечение вместо сырого osstatus.
			log.Printf("config: токен %s есть в связке ключей, но недоступен этой сборке — введи его заново в настройках", s.name)
		case err != nil:
			log.Printf("config: не смог прочитать токен %s из связки ключей: %v", s.name, err)
		default:
			*s.dst = v
		}
	}

	if v := os.Getenv("TASKWATCH_GITLAB_TOKEN"); v != "" {
		cfg.GitLab.Token = v
	}
	if v := os.Getenv("TASKWATCH_YOUTRACK_TOKEN"); v != "" {
		cfg.YouTrack.Token = v
	}

	return cfg, cfg.Validate()
}

// Save пишет конфиг в yaml с правами 0600. Токены в yaml не попадают никогда —
// они живут в Связке ключей.
func Save(path string, cfg *Config) error {
	out := *cfg
	out.GitLab.Token = ""
	out.YouTrack.Token = ""
	if out.StatePath == filepath.Join(filepath.Dir(path), "state.json") {
		out.StatePath = "" // дефолт не пишем, чтобы файл не пух путями
	}
	data, err := yaml.Marshal(&out)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// GitLabOK — сервис заполнен целиком, его можно опрашивать.
func (c *Config) GitLabOK() bool {
	return c.GitLab.URL != "" && c.GitLab.Token != "" && c.GitLab.Username != ""
}

// YouTrackOK — сервис заполнен целиком, его можно опрашивать.
func (c *Config) YouTrackOK() bool {
	return c.YouTrack.URL != "" && c.YouTrack.Token != "" && c.YouTrack.Login != "" && c.YouTrack.Query != ""
}

// Configured — хоть один сервис настроен целиком, движок может опрашивать.
func (c *Config) Configured() bool { return c.GitLabOK() || c.YouTrackOK() }

// Validate отбраковывает только половинчатые сервисы: сервис либо пуст, либо заполнен целиком.
func (c *Config) Validate() error {
	var errs []error
	check := func(name string, fields [][2]string) {
		filled := 0
		for _, f := range fields {
			if f[1] != "" {
				filled++
			}
		}
		if filled == 0 || filled == len(fields) {
			return
		}
		for _, f := range fields {
			if f[1] == "" {
				errs = append(errs, fmt.Errorf("%s.%s is required", name, f[0]))
			}
		}
	}
	check("gitlab", [][2]string{
		{"url", c.GitLab.URL}, {"token", c.GitLab.Token}, {"username", c.GitLab.Username},
	})
	check("youtrack", [][2]string{
		{"url", c.YouTrack.URL}, {"token", c.YouTrack.Token}, {"login", c.YouTrack.Login}, {"query", c.YouTrack.Query},
	})
	if c.PollInterval <= 0 {
		errs = append(errs, errors.New("poll_interval must be positive"))
	}
	return errors.Join(errs...)
}
