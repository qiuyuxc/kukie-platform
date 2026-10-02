package main

import "net/http"

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
	return app.middleware(mux)
}
