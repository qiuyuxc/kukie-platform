package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSiteCommentsShareAPIAndEscapeHTML(test *testing.T) {
	fixture := siteFixture(test)
	text := `<img src=x onerror=alert(1)> 网站与后台共用评论`
	created := call(test, fixture, "POST", "/api/v1/posts/site-post/comments", fixture.adminToken, map[string]string{"text": text}, 201)
	body := siteCall(test, fixture, "/posts/nested/中文/", 200).Body.String()
	if !strings.Contains(body, "&lt;img") || strings.Contains(body, text) || !strings.Contains(body, `data-comment-provider="kukie"`) || strings.Contains(body, "tcomment") {
		test.Fatal("comments missing, unsafe, or using the old provider")
	}
	siteCall(test, fixture, "/api/v1/posts/site-post/comments", 200)
	call(test, fixture, "DELETE", "/api/v1/admin/comments/"+created["id"].(string), fixture.adminToken, nil, 200)
	if strings.Contains(siteCall(test, fixture, "/posts/nested/中文/", 200).Body.String(), "共用评论") {
		test.Fatal("deleted comment remains visible")
	}
	call(test, fixture, "POST", "/api/v1/posts/site-post/comments", fixture.adminToken, map[string]string{"text": text}, 201)
	if _, err := fixture.app.db.Exec("UPDATE posts SET status='draft' WHERE id='site-post'"); err != nil {
		test.Fatal(err)
	}
	if strings.Contains(siteCall(test, fixture, "/api/v1/posts/site-post/comments", 200).Body.String(), "共用评论") {
		test.Fatal("draft comments leaked")
	}
}

func TestSiteCommentCookieAuthAndBoundary(test *testing.T) {
	fixture := siteFixture(test)
	handler := fixture.app.siteHandler()
	post := func(path, body, origin, csrf string, cookie *http.Cookie) *httptest.ResponseRecorder {
		request := httptest.NewRequest("POST", path, strings.NewReader(body))
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Origin", origin)
		request.Header.Set("X-CSRF-Token", csrf)
		if cookie != nil {
			request.AddCookie(cookie)
		}
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		return response
	}
	login := post("/api/v1/auth/login", `{"email":"admin@example.test","password":"a-safe-admin-password"}`, "https://blog.example.test", "", nil)
	if login.Code != 200 || len(login.Result().Cookies()) != 1 {
		test.Fatalf("login failed: %d %s", login.Code, login.Body.String())
	}
	cookie := login.Result().Cookies()[0]
	if !cookie.HttpOnly || cookie.SameSite != http.SameSiteStrictMode {
		test.Fatal("unsafe session cookie")
	}
	var session struct {
		CSRF string `json:"csrf"`
	}
	if err := json.Unmarshal(login.Body.Bytes(), &session); err != nil {
		test.Fatal(err)
	}
	for _, scenario := range []struct {
		origin, csrf string
		cookie       *http.Cookie
		status       int
	}{
		{"https://blog.example.test", session.CSRF, nil, 401},
		{"https://blog.example.test", "", cookie, 403},
		{"https://evil.example", session.CSRF, cookie, 403},
		{"", session.CSRF, cookie, 403},
		{"https://blog.example.test", session.CSRF, cookie, 201},
	} {
		response := post("/api/v1/posts/site-post/comments", `{"text":"来自网页"}`, scenario.origin, scenario.csrf, scenario.cookie)
		if response.Code != scenario.status {
			test.Fatalf("expected %d, got %d: %s", scenario.status, response.Code, response.Body.String())
		}
	}
	for _, path := range []string{"/api/v1/admin/comments", "/api/v1/admin/posts", "/api/v1/admin/settings", "/api/v1/setup", "/api/v1/me/2fa/setup"} {
		siteCall(test, fixture, path, 404)
		if response := post(path, `{}`, "https://blog.example.test", session.CSRF, cookie); response.Code != 404 {
			test.Fatalf("admin route exposed: %s %d", path, response.Code)
		}
	}
	if response := post("/api/v1/auth/logout", `{}`, "https://blog.example.test", session.CSRF, cookie); response.Code != 200 {
		test.Fatal("logout failed", response.Body.String())
	}
	if response := post("/api/v1/posts/site-post/comments", `{"text":"失效会话"}`, "https://blog.example.test", session.CSRF, cookie); response.Code != 401 {
		test.Fatal("logout did not revoke session")
	}
}
