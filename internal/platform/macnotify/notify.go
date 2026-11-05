// Package macnotify показывает уведомления macOS.
package macnotify

import (
	"os/exec"
	"strings"
)

// Send показывает уведомление. terminal-notifier умеет открывать url по клику,
// но требует своего разрешения на уведомления; если он не сработал — osascript
// (клик никуда не ведёт, зато работает всегда). group — ключ события: по нему
// terminal-notifier заменяет уведомление, а не дублирует.
func Send(title, message, url, group string) error {
	return system.send(title, message, url, group)
}

// sender — Send с подменяемыми поиском и запуском программ (для тестов).
type sender struct {
	lookPath func(file string) (string, error)
	run      func(name string, args ...string) error
}

var system = sender{
	lookPath: exec.LookPath,
	run:      func(name string, args ...string) error { return exec.Command(name, args...).Run() },
}

func (s sender) send(title, message, url, group string) error {
	if bin, err := s.lookPath("terminal-notifier"); err == nil {
		args := []string{"-title", title, "-message", message, "-group", group}
		if url != "" {
			args = append(args, "-open", url)
		}
		if err := s.run(bin, args...); err == nil {
			return nil
		}
	}

	script := "display notification " + quote(message) + " with title " + quote(title)
	return s.run("osascript", "-e", script)
}

func quote(s string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s) + `"`
}
