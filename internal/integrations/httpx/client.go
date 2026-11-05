// Package httpx — общий HTTP-клей клиентов GitLab и YouTrack: JSON-запросы,
// ошибки статусов и повтор идемпотентных запросов при осечках сети и 429/5xx.
package httpx

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Client ходит в JSON API одного сервиса.
type Client struct {
	name   string // префикс ошибок: "gitlab GET /user: 401 …"
	base   string
	auth   func(*http.Request)
	http   *http.Client
	delays []time.Duration
}

type Option func(*Client)

// WithRetryDelays задаёт паузы перед повторами; без пауз — без повторов.
func WithRetryDelays(d ...time.Duration) Option { return func(c *Client) { c.delays = d } }

// New — клиент API с корнем base; auth подписывает каждый запрос.
func New(name, base string, auth func(*http.Request), opts ...Option) *Client {
	c := &Client{
		name:   name,
		base:   strings.TrimRight(base, "/"),
		auth:   auth,
		http:   &http.Client{Timeout: 30 * time.Second},
		delays: defaultDelays,
	}
	for _, o := range opts {
		o(c)
	}
	return c
}

// Base — корень API без завершающего слэша.
func (c *Client) Base() string { return c.base }

// Get запрашивает path и раскладывает JSON-ответ в out. Осечки повторяются.
func (c *Client) Get(ctx context.Context, path string, q url.Values, out any) error {
	return retry(ctx, c.delays, func() error { return c.do(ctx, http.MethodGet, path, q, nil, out) })
}

// Post отправляет in как JSON. Не повторяется: POST не идемпотентен.
func (c *Client) Post(ctx context.Context, path string, in, out any) error {
	return c.do(ctx, http.MethodPost, path, nil, in, out)
}

func (c *Client) do(ctx context.Context, method, path string, q url.Values, in, out any) error {
	u := c.base + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}

	var body io.Reader
	if in != nil {
		data, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body = bytes.NewReader(data)
	}

	r, err := http.NewRequestWithContext(ctx, method, u, body)
	if err != nil {
		return err
	}
	r.Header.Set("Accept", "application/json")
	if in != nil {
		r.Header.Set("Content-Type", "application/json")
	}
	if c.auth != nil {
		c.auth(r)
	}

	resp, err := c.http.Do(r)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return &StatusError{
			Code:    resp.StatusCode,
			Message: fmt.Sprintf("%s %s %s: %s: %s", c.name, method, path, resp.Status, msg),
		}
	}
	if out == nil {
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}
