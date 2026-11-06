// Package httpapi — HTTP API окна: карточки, заглушения, переводы, настройки.
// Хендлеры тонкие: разобрать запрос, позвать движок или настройки, ответить JSON.
// Одно и то же API обслуживает окно в меню-баре (через asset handler Wails) и
// режим web (обычный HTTP-сервер, см. Serve).
package httpapi

import (
	"context"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"taskwatch/internal/domain"
	"taskwatch/internal/engine"
	"taskwatch/internal/settings"
	"taskwatch/internal/view"
)

// longPoll — сколько GET /api/view?after= ждёт обновления, прежде чем ответить
// как есть. Меньше таймаутов fetch в WebKit и браузерах.
var longPoll = 25 * time.Second

type api struct {
	eng       *engine.Engine
	settings  *settings.Service
	openURL   Opener
	loginItem LoginItem
}

// Opener открывает ссылки в браузере пользователя одним вызовом; background —
// просить не выводить браузер вперёд.
type Opener func(urls []string, background bool) error

// maxOpen — сколько ссылок открывается за раз: защита от лавины вкладок.
const maxOpen = 20

// LoginItem — запуск при входе в систему. Есть только у приложения в меню-баре.
type LoginItem interface {
	Supported() bool
	Enabled() bool
	SetEnabled(on bool) error
}

// Option — необязательная часть API.
type Option func(*api)

// WithLoginItem включает переключатель «Запускать при входе» в настройках.
func WithLoginItem(li LoginItem) Option { return func(a *api) { a.loginItem = li } }

// Handler — API под /api/, на остальных путях — assets (файлы окна).
// openURL открывает ссылку в браузере пользователя.
func Handler(eng *engine.Engine, svc *settings.Service, openURL Opener, assets http.Handler, opts ...Option) http.Handler {
	a := &api{eng: eng, settings: svc, openURL: openURL}
	for _, o := range opts {
		o(a)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/view", a.view)
	mux.HandleFunc("POST /api/mute", a.mute)
	mux.HandleFunc("POST /api/unmute", a.unmute)
	mux.HandleFunc("POST /api/refresh", a.refresh)
	mux.HandleFunc("POST /api/transition", a.transition)
	mux.HandleFunc("POST /api/open", a.open)
	mux.HandleFunc("GET /api/config", a.getConfig)
	mux.HandleFunc("POST /api/config", a.saveConfig)
	mux.HandleFunc("POST /api/config/test", a.testConfig)
	mux.HandleFunc("POST /api/config/boards", a.boards)
	mux.HandleFunc("POST /api/secrets/clear", a.clearSecrets)
	mux.HandleFunc("GET /api/login-item", a.getLoginItem)
	mux.HandleFunc("POST /api/login-item", a.setLoginItem)
	mux.Handle("/", assets)
	return mux
}

// GET /api/view — карточки. С ?after=<version> это long-poll: ответ приходит,
// когда версия движка отличается от after (прошёл опрос, сменились заглушения)
// или через longPoll — так окно обновляется сразу, без опроса по таймеру.
func (a *api) view(w http.ResponseWriter, r *http.Request) {
	if after := r.URL.Query().Get("after"); after != "" {
		seen, err := strconv.ParseUint(after, 10, 64)
		if err != nil {
			writeError(w, http.StatusBadRequest, "after must be a version number")
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), longPoll)
		defer cancel()
		a.eng.WaitUpdate(ctx, seen)
	}
	// Версию читаем до снапшота: если обновление проскочит между ними, клиент
	// получит свежие данные со старой версией и просто спросит ещё раз.
	version := a.eng.Version()
	snap, updated, err := a.eng.Snapshot()
	v := view.Build(snap, updated, err, a.eng.Config().Policy(), a.eng.Mutes())
	v.Version = version
	if t := a.eng.SleepUntil(time.Now()); !t.IsZero() {
		v.SleepUntil = t.Format(time.RFC3339)
	}
	writeJSON(w, http.StatusOK, v)
}

// POST /api/mute — заглушить MR: {key: "mr:123", hours: 1|3|24}; hours 0 = навсегда.
// alsoTitle + title — заодно заглушить все MR с этим точным названием.
func (a *api) mute(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Key       string  `json:"key"`
		Hours     float64 `json:"hours"`
		AlsoTitle bool    `json:"alsoTitle"`
		Title     string  `json:"title"`
	}
	switch {
	case decode(r, &in) != nil || !domain.IsMRKey(in.Key):
		writeError(w, http.StatusBadRequest, "mr key is required")
		return
	case in.Hours < 0:
		writeError(w, http.StatusBadRequest, "hours must be non-negative")
		return
	case in.AlsoTitle && strings.TrimSpace(in.Title) == "":
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
	respond(w, a.eng.Mute(in.Key, title, until), http.StatusInternalServerError)
}

// POST /api/unmute — вернуть заглушенный MR (и правило по названию, если задано).
func (a *api) unmute(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Key   string `json:"key"`
		Title string `json:"title"`
	}
	if decode(r, &in) != nil || !domain.IsMRKey(in.Key) {
		writeError(w, http.StatusBadRequest, "mr key is required")
		return
	}
	respond(w, a.eng.Unmute(in.Key, strings.TrimSpace(in.Title)), http.StatusInternalServerError)
}

// POST /api/refresh — внеочередной проход опроса; его итог придёт через long-poll.
func (a *api) refresh(w http.ResponseWriter, r *http.Request) {
	a.eng.Refresh()
	w.WriteHeader(http.StatusNoContent)
}

// POST /api/transition — перевести таску командой из настроек: {issueId}.
func (a *api) transition(w http.ResponseWriter, r *http.Request) {
	var in struct {
		IssueID string `json:"issueId"`
	}
	if decode(r, &in) != nil || in.IssueID == "" {
		writeError(w, http.StatusBadRequest, "issueId is required")
		return
	}
	respond(w, a.eng.Transition(r.Context(), in.IssueID), http.StatusBadGateway)
}

// POST /api/open — открыть в браузере {url} или пачку {urls}, только http(s).
// background — пачка, собранная колесиком или ⌘+кликом: просить не выводить
// браузер вперёд.
func (a *api) open(w http.ResponseWriter, r *http.Request) {
	var in struct {
		URL        string   `json:"url"`
		URLs       []string `json:"urls"`
		Background bool     `json:"background"`
	}
	if decode(r, &in) != nil {
		writeError(w, http.StatusBadRequest, "invalid json")
		return
	}
	urls := in.URLs
	if in.URL != "" {
		urls = append([]string{in.URL}, urls...)
	}
	if len(urls) == 0 || len(urls) > maxOpen || slices.ContainsFunc(urls, notHTTP) {
		writeError(w, http.StatusBadRequest, "1–20 http(s) urls are required")
		return
	}
	respond(w, a.openURL(urls, in.Background), http.StatusInternalServerError)
}

func notHTTP(u string) bool {
	return !strings.HasPrefix(u, "https://") && !strings.HasPrefix(u, "http://")
}
