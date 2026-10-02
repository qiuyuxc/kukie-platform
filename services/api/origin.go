package main

import (
	"errors"
	"net/http"
)

func (app *server) requestOrigin(request *http.Request) (string, error) {
	scheme := "http"
	if request.TLS != nil || app.secureCookie {
		scheme = "https"
	}
	value := scheme + "://" + request.Host
	origin, err := siteOrigin(value)
	if err != nil || origin != value {
		return "", errors.New("invalid request host")
	}
	return origin, nil
}

func (app *server) allowedOrigin(request *http.Request) bool {
	origin := request.Header.Get("Origin")
	if origin == "" {
		return false
	}
	if app.origins[origin] {
		return true
	}
	expected, err := app.requestOrigin(request)
	return err == nil && origin == expected
}
