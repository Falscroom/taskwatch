package httpapi

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"taskwatch/internal/config"
	"taskwatch/internal/settings"
)

// GET /api/config — текущие настройки для формы. Токены не отдаём, только tokenSet.
func (a *api) getConfig(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, formOf(a.eng.Config(), a.settings.Path()))
}

// POST /api/config — новый токен прячем в Связку ключей, в config.yaml пишем
// настройки без токенов и перезаряжаем движок на лету.
func (a *api) saveConfig(w http.ResponseWriter, r *http.Request) {
	cur := a.eng.Config()
	cfg, err := a.submitted(r, cur)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := a.settings.Save(cur, cfg); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.eng.Reload(cfg)
	w.WriteHeader(http.StatusNoContent)
}

// POST /api/config/test — проверить связь с теми значениями, что сейчас в форме.
func (a *api) testConfig(w http.ResponseWriter, r *http.Request) {
	cfg, err := a.submitted(r, a.eng.Config())
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	gl, yt := settings.Test(r.Context(), cfg)
	writeJSON(w, http.StatusOK, map[string]probe{"gitlab": probeOf(gl), "youtrack": probeOf(yt)})
}

// submitted — конфиг из присланной формы, готовый к сохранению: прежние
// токены подставлены, проверка пройдена (settings.Resolve).
func (a *api) submitted(r *http.Request, cur *config.Config) (*config.Config, error) {
	var in configForm
	if err := decode(r, &in); err != nil {
		return nil, fmt.Errorf("bad json: %w", err)
	}
	cfg, err := in.config(cur, a.settings.Path())
	if err != nil {
		return nil, err
	}
	if err := settings.Resolve(cur, cfg); err != nil {
		return nil, err
	}
	return cfg, nil
}

// probe — результат проверки одного сервиса из формы настроек.
type probe struct {
	OK      bool   `json:"ok"`
	User    string `json:"user,omitempty"`
	Error   string `json:"error,omitempty"`
	Skipped bool   `json:"skipped,omitempty"` // сервис не заполнен — проверять нечего
}

func probeOf(p settings.Probe) probe {
	switch {
	case p.Skipped:
		return probe{Skipped: true}
	case p.Err != nil:
		return probe{Error: p.Err.Error()}
	default:
		return probe{OK: true, User: p.User}
	}
}

// configForm — форма настроек как она уходит в UI и приходит обратно.
type configForm struct {
	ConfigPath   string       `json:"configPath"`
	Configured   bool         `json:"configured"`
	PollInterval string       `json:"pollInterval"`
	GitLab       gitlabForm   `json:"gitlab"`
	YouTrack     youtrackForm `json:"youtrack"`
	Rules        rulesForm    `json:"rules"`
}

type serviceForm struct {
	URL      string `json:"url"`
	TokenSet bool   `json:"tokenSet"`
	OK       bool   `json:"ok"` // заполнен целиком — движок его опрашивает
}

type gitlabForm struct {
	serviceForm
	Username string `json:"username"`
	// Token приходит только в POST: formOf его никогда не заполняет.
	Token string `json:"token,omitempty"`
}

type youtrackForm struct {
	serviceForm
	Login string `json:"login"`
	Query string `json:"query"`
	// Token приходит только в POST: formOf его никогда не заполняет.
	Token string `json:"token,omitempty"`
}

type rulesForm struct {
	RequiredApprovals   int    `json:"requiredApprovals"`
	ReviewState         string `json:"reviewState"`
	NextCommand         string `json:"nextCommand"`
	AutoTransition      bool   `json:"autoTransition"`
	ReleaseBranchPrefix string `json:"releaseBranchPrefix"`
	MRLookback          string `json:"mrLookback"`
	ExcludeDraft        bool   `json:"excludeDraft"`
	MRBehindCommits     int    `json:"mrBehindCommits"`
}

// formOf отдаёт настройки в UI. Токены наружу не уходят — только флаг tokenSet.
func formOf(cfg *config.Config, path string) configForm {
	return configForm{
		ConfigPath:   path,
		Configured:   cfg.Configured(),
		PollInterval: cfg.PollInterval.String(),
		GitLab: gitlabForm{
			serviceForm: serviceForm{URL: cfg.GitLab.URL, TokenSet: cfg.GitLab.Token != "", OK: cfg.GitLabOK()},
			Username:    cfg.GitLab.Username,
		},
		YouTrack: youtrackForm{
			serviceForm: serviceForm{URL: cfg.YouTrack.URL, TokenSet: cfg.YouTrack.Token != "", OK: cfg.YouTrackOK()},
			Login:       cfg.YouTrack.Login,
			Query:       cfg.YouTrack.Query,
		},
		Rules: rulesForm{
			RequiredApprovals:   cfg.Rules.RequiredApprovals,
			ReviewState:         cfg.Rules.ReviewState,
			NextCommand:         cfg.Rules.NextCommand,
			AutoTransition:      cfg.Rules.AutoTransition,
			ReleaseBranchPrefix: cfg.Rules.ReleaseBranchPrefix,
			MRLookback:          cfg.Rules.MRLookback.String(),
			ExcludeDraft:        cfg.Rules.ExcludeDraft,
			MRBehindCommits:     cfg.Rules.MRBehindCommits,
		},
	}
}

// config переводит форму в конфиг как есть: токены — только введённые.
func (f configForm) config(cur *config.Config, path string) (*config.Config, error) {
	cfg := config.Default(path)
	cfg.StatePath = cur.StatePath
	if f.PollInterval != "" {
		d, err := time.ParseDuration(f.PollInterval)
		if err != nil {
			return nil, fmt.Errorf("poll_interval: %w", err)
		}
		cfg.PollInterval = d
	}

	cfg.GitLab = config.GitLab{
		URL:      strings.TrimSpace(f.GitLab.URL),
		Token:    strings.TrimSpace(f.GitLab.Token),
		Username: strings.TrimSpace(f.GitLab.Username),
	}
	cfg.YouTrack = config.YouTrack{
		URL:   strings.TrimSpace(f.YouTrack.URL),
		Token: strings.TrimSpace(f.YouTrack.Token),
		Login: strings.TrimSpace(f.YouTrack.Login),
		Query: strings.TrimSpace(f.YouTrack.Query),
	}

	r := f.Rules
	cfg.Rules.RequiredApprovals = r.RequiredApprovals
	cfg.Rules.MRBehindCommits = r.MRBehindCommits
	cfg.Rules.ReviewState = strings.TrimSpace(r.ReviewState)
	cfg.Rules.NextCommand = strings.TrimSpace(r.NextCommand)
	cfg.Rules.AutoTransition = r.AutoTransition
	cfg.Rules.ReleaseBranchPrefix = strings.TrimSpace(r.ReleaseBranchPrefix)
	cfg.Rules.ExcludeDraft = r.ExcludeDraft
	if r.MRLookback != "" {
		d, err := time.ParseDuration(r.MRLookback)
		if err != nil {
			return nil, fmt.Errorf("mr_lookback: %w", err)
		}
		cfg.Rules.MRLookback = d
	}
	return cfg, nil
}
