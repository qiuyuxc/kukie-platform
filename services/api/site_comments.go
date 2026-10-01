package main

import (
	"context"
	"net/http"
)

type siteRequestKey struct{}

func (app *server) allowedOrigin(request *http.Request) bool {
	origin := request.Header.Get("Origin")
	if request.Context().Value(siteRequestKey{}) == true && app.site != nil && origin == app.site.manifest.BaseURL {
		return true
	}
	return app.origins[origin]
}

func (app *server) siteReaderAPI() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/site/banner", app.siteBanner)
	mux.HandleFunc("GET /api/v1/config", app.publicConfig)
	mux.HandleFunc("POST /api/v1/auth/email-code", app.emailCode)
	mux.HandleFunc("POST /api/v1/auth/register", app.register)
	mux.HandleFunc("POST /api/v1/auth/login", app.login)
	mux.HandleFunc("POST /api/v1/auth/logout", app.auth(app.logout, false))
	mux.HandleFunc("GET /api/v1/me", app.auth(app.me, false))
	mux.HandleFunc("GET /api/v1/posts/{id}/comments", app.listComments)
	mux.HandleFunc("POST /api/v1/posts/{id}/comments", app.auth(app.createComment, false))
	handler := app.middleware(mux)
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		ctx := context.WithValue(request.Context(), siteRequestKey{}, true)
		handler.ServeHTTP(writer, request.WithContext(ctx))
	})
}
