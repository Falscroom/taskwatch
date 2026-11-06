// Package macnotify показывает уведомления macOS без бандла приложения:
// через terminal-notifier или osascript. В taskwatch.app уведомления идут через
// Центр уведомлений (ui/tray), а этот пакет — запасной путь для run, once и web.
package macnotify

import (
	"os/exec"
	"strings"
)

// Notification — что показать. Group — ключ события: по нему terminal-notifier
// заменяет уведомление, а не дублирует.
type Notification struct {
	Title    string
	Subtitle string
	Body     string
	URL      string
	Group    string
}

// Send показывает уведомление. terminal-notifier умеет открывать url по клику,
// но требует своего разрешения на уведомления; если он не сработал — osascript
// (клик никуда не ведёт, зато работает всегда).
func Send(n Notification) error {
	return system.send(n)
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

func (s sender) send(n Notification) error {
	if bin, err := s.lookPath("terminal-notifier"); err == nil {
		args := []string{"-title", n.Title, "-message", n.Body, "-group", n.Group}
		if n.Subtitle != "" {
			args = append(args, "-subtitle", n.Subtitle)
		}
		if n.URL != "" {
			args = append(args, "-open", n.URL)
		}
		if err := s.run(bin, args...); err == nil {
			return nil
		}
	}

	script := "display notification " + quote(n.Body) + " with title " + quote(n.Title)
	if n.Subtitle != "" {
		script += " subtitle " + quote(n.Subtitle)
	}
	return s.run("osascript", "-e", script)
}

func quote(s string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s) + `"`
}
