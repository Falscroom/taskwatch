package httpx

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"syscall"
	"time"
)

// defaultDelays — паузы перед повторами: всего три попытки, первая сразу.
var defaultDelays = []time.Duration{500 * time.Millisecond, time.Second}

// StatusError — ответ с нежелательным статусом.
type StatusError struct {
	Code    int
	Message string
}

func (e *StatusError) Error() string { return e.Message }

// StatusCode — HTTP-код ответа: по нему движок, не зная про httpx, отличает
// «токен не подходит» от остальных ошибок.
func (e *StatusError) StatusCode() int { return e.Code }

// retry выполняет attempt, пока ошибка ретраебельная и не кончились паузы.
func retry(ctx context.Context, delays []time.Duration, attempt func() error) error {
	err := attempt()
	for _, d := range delays {
		if err == nil || !Retryable(err) {
			return err
		}
		if !sleep(ctx, d) {
			return ctx.Err()
		}
		err = attempt()
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
