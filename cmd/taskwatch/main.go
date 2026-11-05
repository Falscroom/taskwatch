package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"taskwatch/internal/config"
	"taskwatch/internal/engine"
	"taskwatch/internal/gui"
	"taskwatch/internal/integrations/gitlab"
	"taskwatch/internal/integrations/youtrack"
	"taskwatch/internal/platform/keychain"
	"taskwatch/internal/platform/macnotify"
	"taskwatch/internal/settings"
	"taskwatch/internal/state"
	"taskwatch/internal/watch"
)

const usage = `usage: taskwatch [-config path] [-addr host:port] <command>

commands:
  app      иконка в меню-баре + уведомления (default)
  web      то же окно в браузере на -addr (для отладки)
  run      опрашивать в цикле и слать уведомления, без UI
  once     один проход опроса
  status   показать таски спринта, MR, апрувы, сборки
  secret-clear  удалить токены из Связки ключей (для uninstall)
`

func main() {
	configPath := flag.String("config", config.DefaultPath(), "path to config.yaml")
	addr := flag.String("addr", "127.0.0.1:7777", "listen address for web")
	flag.Usage = func() { fmt.Fprint(os.Stderr, usage) }
	flag.Parse()

	cmd := flag.Arg(0)
	if cmd == "" {
		cmd = "app"
	}

	svc := settings.New(*configPath, settings.WithEnv(keychain.Store{}))

	// secret-clear не требует конфига — вызывается из uninstall.sh, когда
	// токены из Связки ключей надо убрать вместе с приложением.
	if cmd == "secret-clear" {
		if err := svc.ClearSecrets(); err != nil {
			log.Fatal(err)
		}
		fmt.Println("токены удалены из связки ключей")
		return
	}

	cfg, err := loadConfig(svc, cmd == "app" || cmd == "web")
	if err != nil {
		log.Fatal(err)
	}

	store, err := state.Open(cfg.StatePath)
	if err != nil {
		log.Fatal(err)
	}
	gl := gitlab.New(cfg.GitLab.URL, cfg.GitLab.Token)
	yt := youtrack.New(cfg.YouTrack.URL, cfg.YouTrack.Token)
	collector := watch.NewCollector(cfg, gl, yt)
	eng := engine.New(cfg, collector, store, yt, macnotify.Send)

	switch cmd {
	case "app":
		if err := gui.Run(eng, svc); err != nil {
			log.Fatal(err)
		}
	case "web":
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		log.Printf("http://%s", *addr)
		err := gui.Serve(ctx, eng, svc, *addr)
		stop()
		if err != nil {
			log.Fatal(err)
		}
	case "run":
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		eng.Run(ctx)
		stop()
	case "once":
		if err := eng.Tick(context.Background()); err != nil {
			log.Fatal(err)
		}
	case "status":
		snap, err := collector.Collect(context.Background())
		if err != nil {
			log.Fatal(err)
		}
		printStatus(os.Stdout, snap)
	default:
		flag.Usage()
		os.Exit(2)
	}
}

// loadConfig загружает конфиг с токенами. UI-режимам (relaxed) годится и неполный:
// окно попросит недостающее. Если файл прочитался, но не прошёл проверку
// (например, токен в Связке ключей недоступен новой сборке), берём его как есть —
// в форме останутся адреса, логины и правила, а не пустота. Остальным командам
// нужен валидный конфиг.
func loadConfig(svc *settings.Service, relaxed bool) (*config.Config, error) {
	cfg, err := svc.Load()
	if err == nil {
		return cfg, nil
	}
	if !relaxed {
		return nil, err
	}
	if cfg == nil {
		cfg = config.Default(svc.Path())
	}
	log.Printf("config: %v — открою настройки в окне", err)
	return cfg, nil
}
