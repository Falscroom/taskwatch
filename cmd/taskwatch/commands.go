package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"taskwatch/internal/alerts"
	"taskwatch/internal/app"
	"taskwatch/internal/config"
	"taskwatch/internal/engine"
	"taskwatch/internal/platform/browser"
	"taskwatch/internal/platform/loginitem"
	"taskwatch/internal/settings"
	"taskwatch/internal/state"
	"taskwatch/internal/ui/frontend"
	"taskwatch/internal/ui/httpapi"
	"taskwatch/internal/ui/tray"
)

// configNeed — какой конфиг нужен команде.
type configNeed int

const (
	noConfig      configNeed = iota // конфиг не читается вовсе
	relaxedConfig                   // годится неполный: настройки попросят в окне
	validConfig                     // нужен валидный, иначе ошибка
)

type command struct {
	name    string
	summary string
	config  configNeed
	run     func(env) error
}

// commands — все команды; порядок — как в справке.
var commands = []command{
	{"app", "иконка в меню-баре + уведомления (default)", relaxedConfig, runApp},
	{"web", "то же окно в браузере на -addr (для отладки)", relaxedConfig, runWeb},
	{"run", "опрашивать в цикле и слать уведомления, без UI", validConfig, runLoop},
	{"once", "один проход опроса", validConfig, runOnce},
	{"status", "показать таски спринта, MR, апрувы, сборки", validConfig, runStatus},
	{"secret-clear", "удалить токены из Связки ключей", noConfig, runSecretClear},
	{"login-item", "запуск при входе: login-item [on|off] (бинарь из taskwatch.app)", noConfig, runLoginItem},
}

func lookup(name string) (command, bool) {
	for _, c := range commands {
		if c.name == name {
			return c, true
		}
	}
	return command{}, false
}

func usage() string {
	var b strings.Builder
	b.WriteString("usage: taskwatch [-config path] [-addr host:port] <command>\n\ncommands:\n")
	for _, c := range commands {
		fmt.Fprintf(&b, "  %-13s %s\n", c.name, c.summary)
	}
	return b.String()
}

// setup — то, что main берёт из флагов и платформы; тесты подставляют своё.
type setup struct {
	settings *settings.Service
	notify   alerts.Notifier
	notifier *tray.Notifier // для режима app: тот же notify, подключённый к Центру уведомлений
	addr     string
	args     []string // аргументы после имени команды
}

// env — всё, что получает команда. cfg и eng есть, только если команде нужен конфиг.
type env struct {
	setup
	cfg *config.Config
	eng *engine.Engine
}

func execute(cmd command, s setup) error {
	e := env{setup: s}
	if cmd.config != noConfig {
		cfg, err := loadConfig(s.settings, cmd.config == relaxedConfig)
		if err != nil {
			return err
		}
		store, err := state.Open(cfg.StatePath)
		if err != nil {
			return err
		}
		e.cfg = cfg
		e.eng = engine.New(cfg, store, s.notify, app.Deps)
	}
	return cmd.run(e)
}

// runApp — меню-бар. Перед стартом убирает установку прежних версий и на первом
// запуске включает запуск при входе (prepareApp).
func runApp(e env) error {
	prepareApp(e.settings.Path())
	return tray.Run(e.eng, e.settings, e.notifier, loginitem.Item{})
}

func runWeb(e env) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go e.eng.Run(ctx)
	handler := httpapi.Handler(e.eng, e.settings, browser.Open, http.FileServerFS(frontend.FS()))
	log.Printf("http://%s", e.addr)
	return httpapi.Serve(ctx, e.addr, handler)
}

func runLoop(e env) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	e.eng.Run(ctx)
	return nil
}

func runOnce(e env) error { return e.eng.Tick(context.Background()) }

func runStatus(e env) error {
	snap, err := app.Deps(e.cfg).Collector.Collect(context.Background())
	if err != nil {
		return err
	}
	printStatus(os.Stdout, snap)
	return nil
}

// runSecretClear — то же, что «Удалить токены из Связки ключей» в настройках, из
// терминала. Конфиг для этого не нужен.
func runSecretClear(e env) error {
	if err := e.settings.ClearSecrets(); err != nil {
		return err
	}
	fmt.Println("токены удалены из связки ключей")
	return nil
}

// runLoginItem — запуск при входе из терминала: без аргумента показывает
// состояние, on/off включают и выключают. Работает только бинарь из
// taskwatch.app: голый бинарь macOS в объекты входа не пускает.
func runLoginItem(e env) error {
	if !loginitem.InBundle() {
		return errors.New("запуск при входе доступен только бинарю из taskwatch.app: /Applications/taskwatch.app/Contents/MacOS/taskwatch login-item")
	}
	switch arg := strings.Join(e.args, " "); arg {
	case "":
	case "on", "off":
		if err := loginitem.Set(arg == "on"); err != nil {
			return err
		}
	default:
		return fmt.Errorf("login-item: %q — нужно on или off", arg)
	}
	fmt.Println("запуск при входе:", loginitem.Current())
	return nil
}
