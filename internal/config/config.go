// Package config — настройки taskwatch и их yaml-файл. Без побочных эффектов:
// токены сюда подставляет пакет settings из хранилища секретов, в файл они не пишутся.
package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"time"

	"gopkg.in/yaml.v3"

	"taskwatch/internal/domain"
	"taskwatch/internal/platform/fsutil"
)

// DefaultPollInterval — как часто опрашивать сервисы, если не задано иное.
const DefaultPollInterval = 30 * time.Minute

type GitLab struct {
	URL string `yaml:"url"`
	// Token живёт в памяти: его подставляет settings из хранилища секретов.
	// В yaml он бывает только у старых версий и при загрузке оттуда переезжает.
	Token    string `yaml:"token,omitempty"`
	Username string `yaml:"username"`
}

type YouTrack struct {
	URL string `yaml:"url"`
	// Token — как у GitLab: в памяти, не в файле.
	Token string `yaml:"token,omitempty"`
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

// WorkHours — рабочее время: вне его taskwatch не опрашивает сервисы и молчит.
type WorkHours struct {
	Enabled bool     `yaml:"enabled"`
	Days    []string `yaml:"days"`  // mon, tue, wed, thu, fri, sat, sun
	Start   string   `yaml:"start"` // "10:00"
	End     string   `yaml:"end"`   // "19:00"
}

// WeekDays — названия дней в work_hours.days, по порядку недели.
var WeekDays = []string{"mon", "tue", "wed", "thu", "fri", "sat", "sun"}

type Config struct {
	PollInterval time.Duration `yaml:"poll_interval"`
	StatePath    string        `yaml:"state_path,omitempty"`
	GitLab       GitLab        `yaml:"gitlab"`
	YouTrack     YouTrack      `yaml:"youtrack"`
	Rules        Rules         `yaml:"rules"`
	WorkHours    WorkHours     `yaml:"work_hours"`
}

func DefaultPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "taskwatch", "config.yaml")
}

// Default — пустой конфиг с дефолтными правилами; state.json лежит рядом с config-файлом.
func Default(path string) *Config {
	return &Config{
		PollInterval: DefaultPollInterval,
		StatePath:    defaultStatePath(path),
		Rules: Rules{
			RequiredApprovals:   2,
			ReleaseBranchPrefix: "release/",
			MRLookback:          14 * 24 * time.Hour,
		},
		WorkHours: WorkHours{Days: []string{"mon", "tue", "wed", "thu", "fri"}, Start: "10:00", End: "19:00"},
	}
}

// Schedule — рабочее время в виде домена; false — оно выключено или задано
// неверно (такое не пропускает Validate), и taskwatch работает круглосуточно.
func (c *Config) Schedule() (domain.WorkHours, bool) {
	w, err := c.WorkHours.parse()
	if !c.WorkHours.Enabled || err != nil {
		return domain.WorkHours{}, false
	}
	return w, true
}

func (h WorkHours) parse() (domain.WorkHours, error) {
	var w domain.WorkHours
	var err error
	if w.Start, err = clock(h.Start); err != nil {
		return w, fmt.Errorf("work_hours.start: %w", err)
	}
	if w.End, err = clock(h.End); err != nil {
		return w, fmt.Errorf("work_hours.end: %w", err)
	}
	if w.Start >= w.End {
		return w, errors.New("work_hours: start must be before end")
	}
	for _, d := range h.Days {
		i := slices.Index(WeekDays, d)
		if i < 0 {
			return w, fmt.Errorf("work_hours.days: unknown day %q", d)
		}
		w.Days[time.Weekday((i+1)%7)] = true // mon → time.Monday, sun → time.Sunday
	}
	if len(h.Days) == 0 {
		return w, errors.New("work_hours.days: at least one day is required")
	}
	return w, nil
}

// clock — «ЧЧ:ММ» в минуты от полуночи.
func clock(s string) (int, error) {
	t, err := time.Parse("15:04", s)
	if err != nil {
		return 0, fmt.Errorf("%q is not HH:MM", s)
	}
	return t.Hour()*60 + t.Minute(), nil
}

func defaultStatePath(configPath string) string {
	return filepath.Join(filepath.Dir(configPath), "state.json")
}

// Load читает yaml поверх дефолтов. Не валидирует: без токенов из хранилища
// секретов конфиг ещё неполон — это делает settings.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	cfg := Default(path)
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	return cfg, nil
}

// Save пишет конфиг в yaml с правами 0600. Токены в yaml не попадают никогда.
func Save(path string, cfg *Config) error {
	out := *cfg
	out.GitLab.Token = ""
	out.YouTrack.Token = ""
	if out.StatePath == defaultStatePath(path) {
		out.StatePath = "" // дефолт не пишем, чтобы файл не пух путями
	}
	data, err := yaml.Marshal(&out)
	if err != nil {
		return err
	}
	return fsutil.WriteFileAtomic(path, data, 0o600)
}

// Policy — правила и «кто я» в виде, в котором их понимает домен.
func (c *Config) Policy() domain.Policy {
	return domain.Policy{
		GitLabUser:        c.GitLab.Username,
		YouTrackUser:      c.YouTrack.Login,
		RequiredApprovals: c.Rules.RequiredApprovals,
		ReviewState:       c.Rules.ReviewState,
		NextCommand:       c.Rules.NextCommand,
		BehindThreshold:   c.Rules.MRBehindCommits,
	}
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
	if c.WorkHours.Enabled {
		if _, err := c.WorkHours.parse(); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}
