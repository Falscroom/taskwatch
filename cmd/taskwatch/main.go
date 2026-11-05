// Команда taskwatch — монитор MR из GitLab и тасок YouTrack в меню-баре macOS.
// Здесь только точка входа: флаги, выбор команды из таблицы (commands.go) и
// сборка того, что зависит от платформы.
package main

import (
	"flag"
	"fmt"
	"log"
	"os"

	"taskwatch/internal/config"
	"taskwatch/internal/platform/keychain"
	"taskwatch/internal/platform/macnotify"
	"taskwatch/internal/settings"
)

func main() {
	configPath := flag.String("config", config.DefaultPath(), "path to config.yaml")
	addr := flag.String("addr", "127.0.0.1:7777", "listen address for web")
	flag.Usage = func() { fmt.Fprint(os.Stderr, usage()) }
	flag.Parse()

	name := flag.Arg(0)
	if name == "" {
		name = "app"
	}
	cmd, ok := lookup(name)
	if !ok {
		flag.Usage()
		os.Exit(2)
	}

	err := execute(cmd, setup{
		settings: settings.New(*configPath, settings.WithEnv(keychain.Store{})),
		notify:   macnotify.Send,
		addr:     *addr,
	})
	if err != nil {
		log.Fatal(err)
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
