package notify

import (
	"os/exec"
	"strings"
)

// Send показывает уведомление macOS. terminal-notifier умеет открывать url по клику,
// но требует своего разрешения на уведомления; если он не сработал — osascript
// (клик никуда не ведёт, зато работает всегда).
func Send(title, message, url, group string) error {
	if bin, err := exec.LookPath("terminal-notifier"); err == nil {
		args := []string{"-title", title, "-message", message, "-group", group}
		if url != "" {
			args = append(args, "-open", url)
		}
		if err := exec.Command(bin, args...).Run(); err == nil {
			return nil
		}
	}

	script := "display notification " + quote(message) + " with title " + quote(title)
	return exec.Command("osascript", "-e", script).Run()
}

func quote(s string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s) + `"`
}
