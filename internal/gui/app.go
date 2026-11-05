// Package gui — иконка в меню-баре и выпадающее окно на Wails v3.
// Фронтенд ходит в Go обычным fetch к /api/*, который обслуживает тот же asset handler.
package gui

import (
	"context"
	"embed"
	"errors"
	"io/fs"
	"log"
	"net"
	"net/http"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"

	"taskwatch/internal/engine"
	"taskwatch/internal/settings"
	"taskwatch/internal/view"
)

//go:embed frontend
var frontend embed.FS

func Run(eng *engine.Engine, svc *settings.Service) error {
	assets, err := fs.Sub(frontend, "frontend")
	if err != nil {
		return err
	}

	var app *application.App
	openURL := func(url string) error { return app.Browser.OpenURL(url) }

	app = application.New(application.Options{
		Name:        "taskwatch",
		Description: "Таски спринта, MR и ревью",
		Assets: application.AssetOptions{
			Handler:        newHandler(eng, svc, application.AssetFileServerFS(assets), openURL),
			DisableLogging: true,
		},
		Mac: application.MacOptions{
			ActivationPolicy: application.ActivationPolicyAccessory,
		},
	})

	window := app.Window.NewWithOptions(application.WebviewWindowOptions{
		Name:            "taskwatch",
		Width:           520,
		Height:          640,
		Frameless:       true,
		AlwaysOnTop:     true,
		Hidden:          true,
		DisableResize:   true,
		HideOnEscape:    true,
		HideOnFocusLost: true,
		BackgroundType:  application.BackgroundTypeTransparent,
		Mac: application.MacWindow{
			Backdrop: application.MacBackdropTransparent,
			// Совпадает с border-radius панели в CSS: иначе AppKit скругляет окно
			// на свои ~10pt и в углах виден зазор между двумя радиусами.
			CornerRadius: 20,
		},
	})
	window.RegisterHook(events.Common.WindowClosing, func(e *application.WindowEvent) {
		window.Hide()
		e.Cancel()
	})

	menu := app.NewMenu()
	menu.Add("Обновить").OnClick(func(*application.Context) { eng.Refresh() })
	menu.AddSeparator()
	menu.Add("Выйти").OnClick(func(*application.Context) { app.Quit() })

	tray := app.SystemTray.New()
	tray.SetTemplateIcon(trayIcon())
	tray.SetTooltip("taskwatch")
	tray.SetMenu(menu)
	tray.AttachWindow(window).WindowOffset(5)

	eng.OnUpdate = func() {
		snap, updated, err := eng.Snapshot()
		label := ""
		if n := view.Build(snap, updated, err, eng.Config().Policy(), eng.Mutes()).NeedsYou; n > 0 {
			label = strconv.Itoa(n)
		}
		tray.SetLabel(label)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	// Цикл опроса должен жить независимо от GUI-событий: если Wails не доставит
	// ApplicationStarted (бывает под launchd), приложение осталось бы без тиков
	// вовсе — ручной «Обновить» при этом тоже ничего не поднимет. Страховка:
	// запускаем цикл по событию, а если оно не пришло за 10 секунд — сами.
	var once sync.Once
	startPolling := func(why string) {
		once.Do(func() {
			log.Printf("цикл опроса запущен (%s)", why)
			go eng.Run(ctx)
		})
	}
	app.Event.OnApplicationEvent(events.Common.ApplicationStarted, func(*application.ApplicationEvent) {
		startPolling("событие ApplicationStarted")
	})
	go func() {
		time.Sleep(10 * time.Second)
		startPolling("страховка: событие не пришло за 10s")
	}()

	return app.Run()
}

// guardOrigin защищает локальный API web-режима от чужих страниц. Страница любого
// сайта может отправить POST на 127.0.0.1 без preflight — поэтому Origin должен
// совпадать с Host. А чтобы не пройти через DNS rebinding (домен злоумышленника,
// указывающий на 127.0.0.1, даёт совпадающие Origin и Host), Host должен быть
// loopback-адресом или localhost. В app-режиме API живёт внутри webview и наружу
// не торчит — там защита не нужна.
func guardOrigin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !loopbackHost(r.Host) {
			http.Error(w, "only loopback hosts are served", http.StatusForbidden)
			return
		}
		if origin := r.Header.Get("Origin"); origin != "" && origin != "http://"+r.Host {
			http.Error(w, "cross-origin request rejected", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// loopbackHost — Host запроса указывает на эту машину: localhost или loopback-IP.
func loopbackHost(hostport string) bool {
	host, _, err := net.SplitHostPort(hostport)
	if err != nil {
		host = hostport
	}
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(strings.Trim(host, "[]"))
	return ip != nil && ip.IsLoopback()
}

// Serve отдаёт то же окно обычным HTTP — для отладки в браузере (taskwatch web).
// Отвечает только на loopback-адреса.
func Serve(ctx context.Context, eng *engine.Engine, svc *settings.Service, addr string) error {
	assets, err := fs.Sub(frontend, "frontend")
	if err != nil {
		return err
	}
	openURL := func(url string) error { return exec.Command("open", url).Run() }
	handler := guardOrigin(newHandler(eng, svc, http.FileServerFS(assets), openURL))
	srv := &http.Server{Addr: addr, Handler: handler}

	go eng.Run(ctx)
	go func() {
		<-ctx.Done()
		_ = srv.Close()
	}()
	if err := srv.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}
