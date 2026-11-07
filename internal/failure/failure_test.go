package failure

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"testing"
)

type statusErr struct{ code int }

func (e *statusErr) Error() string   { return fmt.Sprintf("%d", e.code) }
func (e *statusErr) StatusCode() int { return e.code }

type timeoutErr struct{}

func (timeoutErr) Error() string   { return "context deadline exceeded" }
func (timeoutErr) Timeout() bool   { return true }
func (timeoutErr) Temporary() bool { return true }

func TestReason(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want string
	}{
		{"нет ошибки", nil, ""},
		{"токен", Wrap(GitLab, &statusErr{401}), "GitLab не принимает токен — обнови его в настройках"},
		{"доступ", Wrap(YouTrack, &statusErr{403}), "YouTrack не принимает токен — обнови его в настройках"},
		{"не найдено", Wrap(YouTrack, &statusErr{404}), "YouTrack не нашёл, что спрашивали — проверь адрес и запрос в настройках"},
		{"сервер лёг", Wrap(GitLab, &statusErr{502}), "GitLab отвечает ошибкой 502 — похоже, он сам лежит"},
		{"прочий код", Wrap(GitLab, &statusErr{418}), "GitLab отвечает ошибкой 418"},
		{"без службы", &statusErr{401}, "Сервер не принимает токен — обнови его в настройках"},
		{"таймаут", Wrap(GitLab, timeoutErr{}), "GitLab не ответил вовремя"},
		{"срок вышел", Wrap(GitLab, context.DeadlineExceeded), "GitLab не ответил вовремя"},
		{
			"хост не найден",
			Wrap(GitLab, &net.DNSError{Err: "no such host", Name: "gitlab.xbet.lan"}),
			"Нет связи с gitlab.xbet.lan — включи VPN или проверь сеть",
		},
		{
			"соединение отвергнуто",
			Wrap(GitLab, &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("connection refused")}),
			"Нет связи с GitLab — проверь VPN или сеть",
		},
		{"непонятная ошибка", Wrap(GitLab, errors.New("unexpected\n  EOF")), "GitLab: unexpected EOF"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := Reason(tc.err); got != tc.want {
				t.Errorf("Reason() = %q, want %q", got, tc.want)
			}
		})
	}
}

// Адрес запроса с параметрами — главный источник простыни в окне: причина
// всегда после него, сам он в сообщении не нужен.
func TestReasonDropsRequestURL(t *testing.T) {
	err := fmt.Errorf(`Get "https://gitlab.xbet.lan/api/v4/merge_requests?scope=all&state=all": boom`)
	if got := Reason(err); got != "boom" {
		t.Errorf("Reason() = %q, want %q", got, "boom")
	}
}

func TestReasonIsOneShortLine(t *testing.T) {
	err := errors.New(strings.Repeat("длинная ошибка ", 40))
	got := Reason(err)
	if strings.Contains(got, "\n") {
		t.Errorf("Reason() переносит строку: %q", got)
	}
	if n := len([]rune(got)); n != 141 { // 140 и многоточие
		t.Errorf("длина = %d, want 141", n)
	}
}

// Detail оставляет ошибку как есть — он для отладки, а не для чтения.
func TestDetail(t *testing.T) {
	if got := Detail(nil); got != "" {
		t.Errorf("Detail(nil) = %q", got)
	}
	err := Wrap(GitLab, &net.DNSError{Err: "no such host", Name: "gitlab.xbet.lan"})
	want := "GitLab: lookup gitlab.xbet.lan: no such host"
	if got := Detail(err); got != want {
		t.Errorf("Detail() = %q, want %q", got, want)
	}
}
