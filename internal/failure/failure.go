// Package failure — почему опрос не удался, словами для человека. Один и тот же
// текст идёт и в уведомление (internal/engine), и в строку над списком
// (internal/view): раньше окно печатало сырую ошибку Go, а уведомление —
// человеческую, и они расходились.
package failure

import (
	"context"
	"errors"
	"fmt"
	"net"
	"regexp"
	"strings"
)

// Имена служб для Wrap: они попадают в текст, который читает человек.
const (
	GitLab   = "GitLab"
	YouTrack = "YouTrack"
)

// Service — ошибка, помеченная службой, которая её вернула. Без пометки по
// ошибке не понять, отвалился GitLab или YouTrack: оба ходят по HTTP и
// возвращают ошибки одного вида.
type Service struct {
	Name string
	Err  error
}

func (e *Service) Error() string { return e.Name + ": " + e.Err.Error() }

func (e *Service) Unwrap() error { return e.Err }

// Wrap помечает ошибку службой; nil остаётся nil, чтобы звать прямо в return.
func Wrap(name string, err error) error {
	if err == nil {
		return nil
	}
	return &Service{Name: name, Err: err}
}

// Reason — причина ошибки одной строкой: что сломалось и что с этим делать.
// Пустая строка — ошибки нет.
func Reason(err error) string {
	if err == nil {
		return ""
	}
	who := "Сервер"
	var svc *Service
	if errors.As(err, &svc) {
		who = svc.Name
	}

	var status interface{ StatusCode() int }
	if errors.As(err, &status) {
		switch code := status.StatusCode(); {
		case code == 401 || code == 403:
			return who + " не принимает токен — обнови его в настройках"
		case code == 404:
			return who + " не нашёл, что спрашивали — проверь адрес и запрос в настройках"
		case code >= 500:
			return fmt.Sprintf("%s отвечает ошибкой %d — похоже, он сам лежит", who, code)
		default:
			return fmt.Sprintf("%s отвечает ошибкой %d", who, code)
		}
	}

	var netErr net.Error
	if errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &netErr) && netErr.Timeout()) {
		return who + " не ответил вовремя"
	}
	// Имя хоста полезнее имени службы: по нему сразу видно, что Mac не в рабочей сети.
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) && dnsErr.Name != "" {
		return "Нет связи с " + dnsErr.Name + " — включи VPN или проверь сеть"
	}
	var opErr *net.OpError
	if errors.As(err, &opErr) || errors.As(err, &netErr) {
		return "Нет связи с " + who + " — проверь VPN или сеть"
	}
	return short(err)
}

// request — «Get "https://host/api/v4/…?a=b": » из ошибок net/http. Адрес с
// параметрами растягивает сообщение на несколько строк; причина идёт после него.
var request = regexp.MustCompile(`[A-Z][a-z]+ "[^"]*": `)

// short — ошибка одной строкой и без адреса запроса: запасной вариант, когда
// причина не опознана.
func short(err error) string {
	return cut(request.ReplaceAllString(oneLine(err), ""), 140)
}

// Detail — ошибка как есть, только в одну строку: её показывает «подробнее» в
// окне, когда причины мало и надо видеть, что именно вернул сервер.
func Detail(err error) string {
	if err == nil {
		return ""
	}
	return cut(oneLine(err), 400)
}

func oneLine(err error) string { return strings.Join(strings.Fields(err.Error()), " ") }

func cut(s string, max int) string {
	if r := []rune(s); len(r) > max {
		return string(r[:max]) + "…"
	}
	return s
}
