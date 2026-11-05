package gui

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"taskwatch/internal/config"
	"taskwatch/internal/domain"
	"taskwatch/internal/engine"
	"taskwatch/internal/settings"
	"taskwatch/internal/view"
)

func newHandler(eng *engine.Engine, svc *settings.Service, assets http.Handler, openURL func(string) error) http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /api/view", func(w http.ResponseWriter, r *http.Request) {
		snap, updated, err := eng.Snapshot()
		writeJSON(w, http.StatusOK, view.Build(snap, updated, err, eng.Config().Policy(), eng.Mutes()))
	})

	// POST /api/mute — заглушить MR: {key: "mr:123", hours: 1|3|24}; hours 0 = навсегда.
	// alsoTitle + title — заодно заглушить все MR с этим точным названием.
	mux.HandleFunc("POST /api/mute", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Key       string  `json:"key"`
			Hours     float64 `json:"hours"`
			AlsoTitle bool    `json:"alsoTitle"`
			Title     string  `json:"title"`
		}
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil || !domain.IsMRKey(in.Key) {
			writeError(w, http.StatusBadRequest, "mr key is required")
			return
		}
		if in.Hours < 0 {
			writeError(w, http.StatusBadRequest, "hours must be non-negative")
			return
		}
		if in.AlsoTitle && strings.TrimSpace(in.Title) == "" {
			writeError(w, http.StatusBadRequest, "title is required with alsoTitle")
			return
		}
		until := time.Time{} // навсегда
		if in.Hours > 0 {
			until = time.Now().Add(time.Duration(in.Hours * float64(time.Hour)))
		}
		title := ""
		if in.AlsoTitle {
			title = strings.TrimSpace(in.Title)
		}
		if err := eng.Mute(in.Key, title, until); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})

	// POST /api/unmute — вернуть заглушенный MR (и правило по названию, если задано).
	mux.HandleFunc("POST /api/unmute", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Key   string `json:"key"`
			Title string `json:"title"`
		}
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil || !domain.IsMRKey(in.Key) {
			writeError(w, http.StatusBadRequest, "mr key is required")
			return
		}
		if err := eng.Unmute(in.Key, strings.TrimSpace(in.Title)); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})

	mux.HandleFunc("POST /api/refresh", func(w http.ResponseWriter, r *http.Request) {
		eng.Refresh()
		w.WriteHeader(http.StatusNoContent)
	})

	mux.HandleFunc("POST /api/transition", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			IssueID string `json:"issueId"`
		}
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil || in.IssueID == "" {
			writeError(w, http.StatusBadRequest, "issueId is required")
			return
		}
		if err := eng.Transition(r.Context(), in.IssueID); err != nil {
			writeError(w, http.StatusBadGateway, err.Error())
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})

	mux.HandleFunc("POST /api/open", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			URL string `json:"url"`
		}
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil ||
			!strings.HasPrefix(in.URL, "https://") && !strings.HasPrefix(in.URL, "http://") {
			writeError(w, http.StatusBadRequest, "http(s) url is required")
			return
		}
		if err := openURL(in.URL); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})

	// GET /api/config — текущие настройки для формы. Токены не отдаём, только tokenSet.
	mux.HandleFunc("GET /api/config", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, configView(eng.Config(), svc.Path()))
	})

	// POST /api/config — валидируем, новый токен прячем в Связку ключей,
	// в config.yaml пишем настройки без токенов и перезаряжаем движок на лету.
	mux.HandleFunc("POST /api/config", func(w http.ResponseWriter, r *http.Request) {
		cur := eng.Config()
		cfg, err := formConfig(r, cur, svc.Path())
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		if err := svc.Save(cur, cfg); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		eng.Reload(cfg)
		w.WriteHeader(http.StatusNoContent)
	})

	// POST /api/config/test — проверить связь с теми значениями, что сейчас в форме.
	mux.HandleFunc("POST /api/config/test", func(w http.ResponseWriter, r *http.Request) {
		cfg, err := formConfig(r, eng.Config(), svc.Path())
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		gl, yt := settings.Test(r.Context(), cfg)
		writeJSON(w, http.StatusOK, map[string]probe{"gitlab": probeView(gl), "youtrack": probeView(yt)})
	})

	mux.Handle("/", assets)
	return mux
}

// probe — результат проверки одного сервиса из формы настроек.
type probe struct {
	OK      bool   `json:"ok"`
	User    string `json:"user,omitempty"`
	Error   string `json:"error,omitempty"`
	Skipped bool   `json:"skipped,omitempty"` // сервис не заполнен — проверять нечего
}

func probeView(p settings.Probe) probe {
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
	// Token приходит только в POST: configView его никогда не заполняет.
	Token string `json:"token,omitempty"`
}

type youtrackForm struct {
	serviceForm
	Login string `json:"login"`
	Query string `json:"query"`
	// Token приходит только в POST: configView его никогда не заполняет.
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

// configView отдаёт настройки в UI. Токены наружу не уходят — только флаг tokenSet.
func configView(cfg *config.Config, path string) configForm {
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

// formConfig собирает конфиг из формы и готовит его к сохранению (settings.Resolve:
// прежние токены, проверка).
func formConfig(r *http.Request, cur *config.Config, path string) (*config.Config, error) {
	cfg, err := decodeConfig(r, cur, path)
	if err != nil {
		return nil, err
	}
	if err := settings.Resolve(cur, cfg); err != nil {
		return nil, err
	}
	return cfg, nil
}

// decodeConfig переводит форму в конфиг как есть: токены — только введённые.
func decodeConfig(r *http.Request, cur *config.Config, path string) (*config.Config, error) {
	var in configForm
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		return nil, fmt.Errorf("bad json: %w", err)
	}

	cfg := config.Default(path)
	cfg.StatePath = cur.StatePath
	if in.PollInterval != "" {
		d, err := time.ParseDuration(in.PollInterval)
		if err != nil {
			return nil, fmt.Errorf("poll_interval: %w", err)
		}
		cfg.PollInterval = d
	}

	cfg.GitLab.URL = strings.TrimSpace(in.GitLab.URL)
	cfg.GitLab.Username = strings.TrimSpace(in.GitLab.Username)
	cfg.GitLab.Token = strings.TrimSpace(in.GitLab.Token)

	cfg.YouTrack.URL = strings.TrimSpace(in.YouTrack.URL)
	cfg.YouTrack.Login = strings.TrimSpace(in.YouTrack.Login)
	cfg.YouTrack.Query = strings.TrimSpace(in.YouTrack.Query)
	cfg.YouTrack.Token = strings.TrimSpace(in.YouTrack.Token)

	cfg.Rules.RequiredApprovals = in.Rules.RequiredApprovals
	cfg.Rules.MRBehindCommits = in.Rules.MRBehindCommits
	cfg.Rules.ReviewState = strings.TrimSpace(in.Rules.ReviewState)
	cfg.Rules.NextCommand = strings.TrimSpace(in.Rules.NextCommand)
	cfg.Rules.AutoTransition = in.Rules.AutoTransition
	cfg.Rules.ReleaseBranchPrefix = strings.TrimSpace(in.Rules.ReleaseBranchPrefix)
	cfg.Rules.ExcludeDraft = in.Rules.ExcludeDraft
	if in.Rules.MRLookback != "" {
		d, err := time.ParseDuration(in.Rules.MRLookback)
		if err != nil {
			return nil, fmt.Errorf("mr_lookback: %w", err)
		}
		cfg.Rules.MRLookback = d
	}

	return cfg, nil
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}
