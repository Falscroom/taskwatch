// Package browser открывает ссылки в браузере по умолчанию.
package browser

import "os/exec"

// Open открывает ссылки в браузере одним вызовом: пачка приходит в браузер
// разом и в том же порядке. background — просить macOS не выводить браузер
// вперёд; Firefox по умолчанию всё равно выходит сам.
func Open(urls []string, background bool) error {
	return exec.Command("open", args(urls, background)...).Run()
}

func args(urls []string, background bool) []string {
	if background {
		return append([]string{"-g"}, urls...) // -g: не активировать приложение
	}
	return urls
}
