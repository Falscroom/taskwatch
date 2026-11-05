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
	"taskwatch/internal/gitlab"
	"taskwatch/internal/gui"
	"taskwatch/internal/keychain"
	"taskwatch/internal/state"
	"taskwatch/internal/watch"
	"taskwatch/internal/youtrack"
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

	// secret-clear не требует конфига — вызывается из uninstall.sh, когда
	// токены из Связки ключей надо убрать вместе с приложением.
	if cmd == "secret-clear" {
		failed := false
		for _, acc := range []string{"gitlab", "youtrack"} {
			if err := keychain.Delete(acc); err != nil {
				log.Printf("%s: %v", acc, err)
				failed = true
			}
		}
		if failed {
			os.Exit(1)
		}
		fmt.Println("токены удалены из связки ключей")
		return
	}

	// В UI-режимах конфиг может ещё не существовать: движок всё равно заводим,
	// а настройки попросят в окне. Остальным командам нужен валидный конфиг.
	relaxed := cmd == "app" || cmd == "web"
	cfg, cfgErr := config.Load(*configPath)
	if cfgErr != nil {
		if !relaxed {
			log.Fatal(cfgErr)
		}
		cfg = config.Default(*configPath)
		log.Printf("config: %v — открою настройки в окне", cfgErr)
	}

	store, err := state.Open(cfg.StatePath)
	if err != nil {
		log.Fatal(err)
	}
	gl := gitlab.New(cfg.GitLab.URL, cfg.GitLab.Token)
	yt := youtrack.New(cfg.YouTrack.URL, cfg.YouTrack.Token)
	collector := watch.NewCollector(cfg, gl, yt)
	eng := engine.New(cfg, collector, store, yt)

	switch cmd {
	case "app":
		if err := gui.Run(eng, *configPath); err != nil {
			log.Fatal(err)
		}
	case "web":
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		log.Printf("http://%s", *addr)
		if err := gui.Serve(ctx, eng, *configPath, *addr); err != nil {
			log.Fatal(err)
		}
	case "run":
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		eng.Run(ctx)
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
