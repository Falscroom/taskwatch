// Package tray — иконка в меню-баре и выпадающее окно на Wails v3. Окно ходит
// в Go обычным fetch к /api/* (httpapi), которое обслуживает тот же asset handler.
package tray

import (
	"context"
	"fmt"
	"hash/fnv"
	"log"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"

	"taskwatch/internal/engine"
	"taskwatch/internal/settings"
	"taskwatch/internal/ui/frontend"
	"taskwatch/internal/ui/httpapi"
	"taskwatch/internal/view"
)

// Run показывает иконку и окно и крутит цикл опроса, пока пользователь не выйдет.
// notifier — тот же, что отдан движку: здесь он подключается к Центру уведомлений.
// loginItem — переключатель «Запускать при входе» в настройках.
func Run(eng *engine.Engine, svc *settings.Service, notifier *Notifier, loginItem httpapi.LoginItem) error {
	var app *application.App
	var systray *application.SystemTray
	openURL := func(url string) error { return app.Browser.OpenURL(url) }

	app = application.New(application.Options{
		Name:        "taskwatch",
		Description: "Таски спринта, MR и ревью",
		Assets: application.AssetOptions{
			Handler:        httpapi.Handler(eng, svc, openURL, application.AssetFileServerFS(frontend.FS()), httpapi.WithLoginItem(loginItem)),
			DisableLogging: true,
		},
		Mac: application.MacOptions{
			ActivationPolicy: application.ActivationPolicyAccessory,
		},
		Services: []application.Service{application.NewService(&service{n: notifier})},
		// Повторный запуск (двойной клик в «Программах») не плодит вторую иконку,
		// а показывает окно уже запущенного.
		SingleInstance: &application.SingleInstanceOptions{
			UniqueID:               instanceID(),
			OnSecondInstanceLaunch: func(application.SecondInstanceData) { systray.ShowWindow() },
		},
	})
	notifier.listen(eng, openURL)

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
	menu.Add("Пример уведомления").OnClick(func(*application.Context) { notifier.sample() })
	menu.AddSeparator()
	menu.Add("Выйти").OnClick(func(*application.Context) { app.Quit() })

	systray = app.SystemTray.New()
	systray.SetTemplateIcon(trayIcon())
	systray.SetTooltip("taskwatch")
	systray.SetMenu(menu)
	systray.AttachWindow(window).WindowOffset(5)

	// Цифра на иконке — сколько карточек ждут твоего действия.
	eng.Subscribe(func() { systray.SetLabel(badge(eng)) })

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	startPolling(ctx, eng, app)
	return app.Run()
}

// instanceID — ключ «один экземпляр». У taskwatch.app он общий; у бинаря вне
// бандла (демо, запуск из терминала при разработке) — свой, иначе такой запуск
// закрывался бы из-за установленного приложения.
func instanceID() string {
	exe, err := os.Executable()
	if err == nil && strings.Contains(exe, ".app/Contents/MacOS/") {
		return "com.taskwatch"
	}
	h := fnv.New32a()
	_, _ = h.Write([]byte(exe))
	return fmt.Sprintf("com.taskwatch.dev.%x", h.Sum32())
}

// badge — текст рядом с иконкой: число карточек «нужно тебе», пусто — если их нет.
func badge(eng *engine.Engine) string {
	snap, updated, err := eng.Snapshot()
	if n := view.Build(snap, updated, err, eng.Config().Policy(), eng.Mutes()).NeedsYou; n > 0 {
		return strconv.Itoa(n)
	}
	return ""
}

// startPolling запускает цикл опроса. Он должен жить независимо от GUI-событий:
// если Wails не доставит ApplicationStarted (бывает под launchd), приложение
// осталось бы без тиков вовсе — ручной «Обновить» при этом тоже ничего не
// поднимет. Страховка: запускаем по событию, а если оно не пришло за 10 секунд — сами.
func startPolling(ctx context.Context, eng *engine.Engine, app *application.App) {
	var once sync.Once
	start := func(why string) {
		once.Do(func() {
			log.Printf("цикл опроса запущен (%s)", why)
			go eng.Run(ctx)
		})
	}
	app.Event.OnApplicationEvent(events.Common.ApplicationStarted, func(*application.ApplicationEvent) {
		start("событие ApplicationStarted")
	})
	go func() {
		time.Sleep(10 * time.Second)
		start("страховка: событие не пришло за 10s")
	}()
}
