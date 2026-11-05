package httpapi

import (
	"context"
	"errors"
	"net"
	"net/http"
	"strings"
)

// Serve отдаёт handler обычным HTTP на addr — режим web, для отладки окна в
// браузере. Отвечает только на loopback-адреса (см. guardOrigin) и
// останавливается вместе с ctx.
func Serve(ctx context.Context, addr string, handler http.Handler) error {
	srv := &http.Server{Addr: addr, Handler: guardOrigin(handler)}
	go func() {
		<-ctx.Done()
		_ = srv.Close()
	}()
	if err := srv.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// guardOrigin защищает локальный API web-режима от чужих страниц. Страница любого
// сайта может отправить POST на 127.0.0.1 без preflight — поэтому Origin должен
// совпадать с Host. А чтобы не пройти через DNS rebinding (домен злоумышленника,
// указывающий на 127.0.0.1, даёт совпадающие Origin и Host), Host должен быть
// loopback-адресом или localhost. В меню-баре API живёт внутри webview и наружу
// не торчит — там защита не нужна.
func guardOrigin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !loopbackHost(r.Host) {
			http.Error(w, "only loopback hosts are served", http.StatusForbidden)
			return
		}
		if origin := r.Header.Get("Origin"); origin != "" && origin != "http://"+r.Host {
			http.Error(w, "cross-origin request rejected", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// loopbackHost — Host запроса указывает на эту машину: localhost или loopback-IP.
func loopbackHost(hostport string) bool {
	host, _, err := net.SplitHostPort(hostport)
	if err != nil {
		host = hostport
	}
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(strings.Trim(host, "[]"))
	return ip != nil && ip.IsLoopback()
}
