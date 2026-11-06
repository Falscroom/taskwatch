// Package browser открывает ссылки в браузере по умолчанию.
package browser

import "os/exec"

// Open открывает url в браузере. background — не выводить браузер вперёд:
// фокус остаётся у taskwatch, и окно у иконки не прячется.
func Open(url string, background bool) error {
	return exec.Command("open", args(url, background)...).Run()
}

func args(url string, background bool) []string {
	if background {
		return []string{"-g", url} // -g: не активировать приложение
	}
	return []string{url}
}
