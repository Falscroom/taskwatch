package main

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"taskwatch/internal/platform/loginitem"
)

// prepareApp — перед запуском в меню-баре: завести лог, убрать установку прежних
// версий и на первом запуске (нет config.yaml) или после неё включить запуск при
// входе. Выключенный пользователем автозапуск не трогаем: config.yaml уже есть.
func prepareApp(configPath string) {
	logToFile(filepath.Join(filepath.Dir(configPath), "taskwatch.log"))
	home, err := os.UserHomeDir()
	if err != nil {
		return
	}
	_, statErr := os.Stat(configPath)
	fresh := errors.Is(statErr, fs.ErrNotExist)
	migrated := removeOldInstall(home, bootoutAgent)
	// Уже добавленный (в том числе выключенный пользователем) не трогаем.
	if (fresh || migrated) && loginitem.InBundle() && !loginitem.Registered() {
		if err := loginitem.Set(true); err != nil {
			log.Printf("запуск при входе: %v", err)
			return
		}
		log.Printf("запуск при входе включён")
	}
}

// removeOldInstall убирает установку прежних версий: LaunchAgent, копию
// приложения в ~/.local/share/taskwatch и ссылку на неё в ~/.local/bin.
// true — она была: автозапуск, который давал LaunchAgent, переезжает в
// объекты входа.
func removeOldInstall(home string, bootout func() error) bool {
	plist := filepath.Join(home, "Library", "LaunchAgents", "com.taskwatch.plist")
	if _, err := os.Stat(plist); err != nil {
		return false
	}
	// Экземпляр, запущенный LaunchAgent'ом, иначе остался бы второй иконкой.
	// Если LaunchAgent запустил нас самих — не выключаем себя: задание уйдёт
	// вместе с plist при следующем входе.
	if os.Getenv("XPC_SERVICE_NAME") != "com.taskwatch" {
		if err := bootout(); err != nil {
			log.Printf("прежний LaunchAgent: %v", err)
		}
	}
	if err := os.Remove(plist); err != nil {
		log.Printf("прежний LaunchAgent: %v", err)
	}
	old := filepath.Join(home, ".local", "share", "taskwatch")
	if exe, err := os.Executable(); err == nil && !strings.HasPrefix(exe, old+string(filepath.Separator)) {
		if err := os.RemoveAll(old); err != nil {
			log.Printf("прежняя установка: %v", err)
		}
	}
	link := filepath.Join(home, ".local", "bin", "taskwatch")
	if target, err := os.Readlink(link); err == nil && strings.HasPrefix(target, old) {
		_ = os.Remove(link)
	}
	log.Printf("прежняя установка (LaunchAgent, %s) убрана", old)
	return true
}

// logToFile дублирует лог в файл рядом с конфигом. Раньше вывод в файл писал
// LaunchAgent (StandardOutPath), а объект входа его никуда не сохраняет. Файл
// больше мегабайта уходит в .old, чтобы лог не рос бесконечно.
func logToFile(path string) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		log.Printf("лог: %v", err)
		return
	}
	if fi, err := os.Stat(path); err == nil && fi.Size() > 1<<20 {
		_ = os.Rename(path, path+".old")
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		log.Printf("лог: %v", err)
		return
	}
	log.SetOutput(io.MultiWriter(os.Stderr, f))
}

func bootoutAgent() error {
	return exec.Command("launchctl", "bootout", fmt.Sprintf("gui/%d/com.taskwatch", os.Getuid())).Run()
}
