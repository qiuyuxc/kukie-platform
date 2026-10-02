package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSameOriginConsoleWithoutAllowlist(test *testing.T) {
	test.Setenv("KUKIE_CONSOLE_ORIGINS", "")
	test.Setenv("KUKIE_SECURE_COOKIE", "0")
	for _, scenario := range []struct {
		origin, target string
		secure         bool
	}{
		{"http://127.0.0.1:8085", "http://127.0.0.1:8085", false},
		{"http://[::1]:8085", "http://[::1]:8085", false},
		{"https://console.example.test", "https://console.example.test", true},
		{"https://console.example.test", "http://console.example.test", true},
	} {
		test.Run(scenario.target, func(test *testing.T) {
			fixture := newFixture(test)
			fixture.app.secureCookie = scenario.secure
			if len(fixture.app.origins) != 0 {
				test.Fatal("same-origin deployment should not require a configured allowlist")
			}
			handler := fixture.app.consoleHandler()
			request := httptest.NewRequest("POST", scenario.target+"/api/v1/auth/login", strings.NewReader(`{"email":"admin@example.test","password":"a-safe-admin-password"}`))
			request.Header.Set("Content-Type", "application/json")
			request.Header.Set("Origin", scenario.origin)
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != 200 || len(response.Result().Cookies()) != 1 {
				test.Fatalf("same-origin login: %d %s", response.Code, response.Body.String())
			}
			cookie := response.Result().Cookies()[0]
			if cookie.Secure != scenario.secure || !cookie.HttpOnly {
				test.Fatal("session cookie protection changed")
			}
			var session struct{ CSRF string }
			if err := json.Unmarshal(response.Body.Bytes(), &session); err != nil {
				test.Fatal(err)
			}
			for _, mutation := range []struct {
				origin, csrf string
				status       int
			}{
				{scenario.origin, "", 403},
				{"", session.CSRF, 403},
				{"null", session.CSRF, 403},
				{"https://evil.example.test", session.CSRF, 403},
				{scenario.origin, session.CSRF, 200},
			} {
				request := httptest.NewRequest("POST", scenario.target+"/api/v1/auth/logout", strings.NewReader(`{}`))
				request.Header.Set("Content-Type", "application/json")
				request.Header.Set("Origin", mutation.origin)
				request.Header.Set("X-CSRF-Token", mutation.csrf)
				request.Header.Set("X-Forwarded-Host", "evil.example.test")
				request.AddCookie(cookie)
				response := httptest.NewRecorder()
				handler.ServeHTTP(response, request)
				if response.Code != mutation.status {
					test.Fatalf("origin %q: expected %d, got %d: %s", mutation.origin, mutation.status, response.Code, response.Body.String())
				}
			}
		})
	}
}

func TestOriginIsolationAndExplicitAllowlist(test *testing.T) {
	test.Setenv("KUKIE_CONSOLE_ORIGINS", "https://trusted.example.test")
	fixture := newFixture(test)
	fixture.app.secureCookie = true
	for _, scenario := range []struct {
		origin  string
		allowed bool
	}{
		{"https://console.example.test", true},
		{"http://console.example.test", false},
		{"https://console.example.test:8443", false},
		{"https://blog.example.test", false},
		{"https://trusted.example.test", true},
		{"https://evil.example.test", false},
	} {
		request := httptest.NewRequest("GET", "http://console.example.test/api/v1/me", nil)
		request.Header.Set("Origin", scenario.origin)
		request.Header.Set("X-Forwarded-Host", "evil.example.test")
		request.Header.Set("X-Forwarded-Proto", "http")
		request.AddCookie(&http.Cookie{Name: "kukie_session", Value: fixture.adminToken})
		response := httptest.NewRecorder()
		fixture.app.consoleHandler().ServeHTTP(response, request)
		if allowed := response.Header().Get("Access-Control-Allow-Origin") == scenario.origin; allowed != scenario.allowed {
			test.Fatalf("origin %s: allowed=%v", scenario.origin, allowed)
		}
		request.Method = "OPTIONS"
		response = httptest.NewRecorder()
		fixture.handler.ServeHTTP(response, request)
		if (response.Code == 204) != scenario.allowed {
			test.Fatalf("preflight %s: %d", scenario.origin, response.Code)
		}
	}
}

func TestSiteRuntimeOrigin(test *testing.T) {
	test.Setenv("KUKIE_SECURE_COOKIE", "0")
	for _, configured := range []string{"", "https://new.example.test"} {
		test.Run("configured="+configured, func(test *testing.T) {
			fixture := siteFixtureAtOrigin(test, configured)
			handler := fixture.app.siteHandler()
			for _, origin := range []string{"http://127.0.0.1:8086", "https://first.example.test", "https://second.example.test"} {
				test.Run(origin, func(test *testing.T) {
					test.Parallel()
					want := configured
					if want == "" {
						want = origin
					}
					for _, path := range []string{"/", "/posts/nested/中文/", "/about/", "/sitemap.xml", "/index.xml", "/robots.txt", "/legacy.html"} {
						request := httptest.NewRequest("GET", origin+path, nil)
						request.Header.Set("X-Forwarded-Host", "attacker.invalid")
						request.Header.Set("X-Forwarded-Proto", "ftp")
						response := httptest.NewRecorder()
						handler.ServeHTTP(response, request)
						body := response.Body.String() + response.Header().Get("Location")
						if response.Code != 200 && response.Code != 308 {
							test.Fatalf("%s: %d %s", path, response.Code, body)
						}
						if !strings.Contains(body, want+"/") || strings.Contains(body, "https://blog.example.test/") || strings.Contains(body, "attacker.invalid") {
							test.Fatalf("%s has incorrect origin, want %s: %.1000s", path, want, body)
						}
						if path == "/about/" && !strings.Contains(body, "https://blog.example.test.external.test/") {
							test.Fatal("external URL was rewritten")
						}
					}
					if fixture.app.site.manifest.BaseURL != configured {
						test.Fatal("request changed the shared origin")
					}
				})
			}
		})
	}
}

func TestSiteOriginBehindHTTPSProxy(test *testing.T) {
	fixture := siteFixtureAtOrigin(test, "")
	fixture.app.secureCookie = true
	request := httptest.NewRequest("GET", "http://blog.example.test/", nil)
	response := httptest.NewRecorder()
	fixture.app.siteHandler().ServeHTTP(response, request)
	if response.Code != 200 || !strings.Contains(response.Body.String(), `rel="canonical" href="https://blog.example.test/"`) {
		test.Fatal("HTTPS proxy origin was not preserved")
	}
}

func TestSiteHTMLRebasing(test *testing.T) {
	site := blogSite{bundleOrigin: "https://kukie.invalid"}
	source := []byte(`<a href="https://kukie.invalid/about/">About</a><script>{"url":"https:\/\/kukie.invalid\/"}</script><a href="https://kukie.invalid.external.test/">External</a>`)
	result := string(site.rebaseHTML(source, "https://public.example.test:8443"))
	for _, expected := range []string{`href="https://public.example.test:8443/about/"`, `https:\/\/public.example.test:8443\/`, `href="https://kukie.invalid.external.test/"`} {
		if !strings.Contains(result, expected) {
			test.Fatalf("missing %q after HTML rebasing: %s", expected, result)
		}
	}
}

func TestInvalidRequestHosts(test *testing.T) {
	fixture := siteFixtureAtOrigin(test, "")
	for _, host := range []string{"", "example.test/path", "example.test?query", "example.test#fragment", "user@example.test", "bad'host", "bad&host"} {
		request := httptest.NewRequest("GET", "/", nil)
		request.Host = host
		response := httptest.NewRecorder()
		fixture.app.siteHandler().ServeHTTP(response, request)
		if response.Code != 400 {
			test.Fatalf("invalid host %q accepted: %d", host, response.Code)
		}
	}
}

func TestAndroidAPIWithoutOriginConfiguration(test *testing.T) {
	test.Setenv("KUKIE_CONSOLE_ORIGINS", "")
	fixture := siteFixtureAtOrigin(test, "")
	createReader(test, fixture, "android@example.test")
	login := call(test, fixture, "POST", "/api/v1/auth/login", "", map[string]any{"email": "android@example.test", "password": "a-safe-reader-password", "client": "android", "remember": true}, 200)
	token, ok := login["token"].(string)
	if !ok || len(token) != 43 || login["remember_token"] == nil || login["csrf"] != nil {
		test.Fatal("Android token login changed")
	}
	call(test, fixture, "GET", "/api/v1/posts", "", nil, 200)
	call(test, fixture, "PATCH", "/api/v1/me", token, map[string]string{"name": "App 读者", "bio": "同源部署仍支持 App"}, 200)
	call(test, fixture, "PUT", "/api/v1/me/bookmarks/site-post", token, nil, 200)
	call(test, fixture, "GET", "/api/v1/me/bookmarks", token, nil, 200)
	call(test, fixture, "GET", "/api/v1/me/subscription", token, nil, 200)
	call(test, fixture, "GET", "/api/v1/admin/posts", token, nil, 403)
	siteCall(test, fixture, "/api/v1/me/bookmarks", 404)
	siteCall(test, fixture, "/api/v1/admin/posts", 404)
}

func TestIndexNowRequiresExplicitPublicOrigin(test *testing.T) {
	test.Setenv("KUKIE_INDEXNOW_KEY", "indexnow-test-key")
	fixture := siteFixtureAtOrigin(test, "")
	if err := fixture.app.configureIndexNow(); err == nil || !strings.Contains(err.Error(), "KUKIE_SITE_URL") {
		test.Fatalf("IndexNow should require a configured public origin: %v", err)
	}
	fixture.app.site.manifest.BaseURL = "https://public.example.test"
	if err := fixture.app.configureIndexNow(); err != nil {
		test.Fatal(err)
	}
}
