// Package req — общий HTTP-клей клиентов: ретраи сетевых осечек и статусов 429/5xx.
package req

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"syscall"
	"time"
)

// attempts — всего попыток: первая + две повторных с паузами 500мс и 1с.
const attempts = 3

// StatusError — ответ с нежелательным статусом.
type StatusError struct {
	Code    int
	Message string
}

func (e *StatusError) Error() string { return e.Message }

// Do выполняет attempt до attempts раз, пока ошибка ретраебельная.
// Для идемпотентных запросов (GET) — POST не вызывать.
func Do(ctx context.Context, attempt func() error) error {
	var err error
	for i := 0; i < attempts; i++ {
		if i > 0 && !sleep(ctx, time.Duration(i)*500*time.Millisecond) {
			return ctx.Err()
		}
		if err = attempt(); err == nil || !Retryable(err) {
			return err
		}
	}
	return err
}

// Retryable — сетевая осечка (таймаут, refused, reset, обрыв соединения) или 429/5xx.
func Retryable(err error) bool {
	if err == nil {
		return false
	}
	var se *StatusError
	if errors.As(err, &se) {
		return se.Code == http.StatusTooManyRequests || se.Code >= 500
	}
	var ne net.Error
	if errors.As(err, &ne) && ne.Timeout() {
		return true
	}
	return errors.Is(err, syscall.ECONNREFUSED) ||
		errors.Is(err, syscall.ECONNRESET) ||
		errors.Is(err, syscall.EPIPE) ||
		errors.Is(err, io.EOF) ||
		errors.Is(err, io.ErrUnexpectedEOF)
}

func sleep(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}
