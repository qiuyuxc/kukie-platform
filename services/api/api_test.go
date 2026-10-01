package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pquerna/otp/totp"
)

type fixture struct {
	app        *server
	handler    http.Handler
	adminToken string
}

func newFixture(test *testing.T) fixture {
	test.Helper()
	directory := test.TempDir()
	app, err := openServer(filepath.Join(directory, "data"), directory)
	if err != nil {
		test.Fatal(err)
	}
	test.Cleanup(func() { app.db.Close() })
	adminID := randomToken()
	_, err = app.db.Exec("INSERT INTO users(id,email,name,password,role,created_at) VALUES(?,?,?,?,?,?)", adminID, "admin@example.test", "管理员", hashPassword("a-safe-admin-password"), "admin", time.Now().UTC().Format(time.RFC3339))
	if err != nil {
		test.Fatal(err)
	}
	token := randomToken()
	_, err = app.db.Exec("INSERT INTO sessions(hash,user_id,expires) VALUES(?,?,?)", digest(token), adminID, time.Now().Add(time.Hour).Unix())
	if err != nil {
		test.Fatal(err)
	}
	return fixture{app: app, handler: app.routes(), adminToken: token}
}
func call(test *testing.T, fixture fixture, method, path, token string, input any, expected int) map[string]any {
	test.Helper()
	var body io.Reader
	if input != nil {
		data, err := json.Marshal(input)
		if err != nil {
			test.Fatal(err)
		}
		body = bytes.NewReader(data)
	}
	request := httptest.NewRequest(method, path, body)
	request.Header.Set("Content-Type", "application/json")
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	response := httptest.NewRecorder()
	fixture.handler.ServeHTTP(response, request)
	if response.Code != expected {
		test.Fatalf("%s %s: expected %d got %d: %s", method, path, expected, response.Code, response.Body.String())
	}
	var data map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &data); err != nil {
		test.Fatal(err)
	}
	return data
}
func createReader(test *testing.T, fixture fixture, email string) string {
	test.Helper()
	code := "123456"
	if err := fixture.app.storeSetting("smtp", smtpSettings{Enabled: true, Host: "smtp.example.test", Port: 587, Mode: "starttls", From: "admin@example.test"}); err != nil {
		test.Fatal(err)
	}
	_, err := fixture.app.db.Exec("INSERT INTO email_codes(email,hash,expires) VALUES(?,?,?)", email, fixture.app.keyed("email:"+email+":"+code), time.Now().Add(time.Minute).Unix())
	if err != nil {
		test.Fatal(err)
	}
	result := call(test, fixture, "POST", "/api/v1/auth/register", "", map[string]string{"email": email, "name": "测试读者", "password": "a-safe-reader-password", "code": code, "client": "android"}, 200)
	return result["token"].(string)
}

func TestRegistrationVerification(test *testing.T) {
	fixture := newFixture(test)
	test.Run("SMTP unavailable fails closed", func(test *testing.T) {
		call(test, fixture, "POST", "/api/v1/auth/email-code", "", map[string]string{"email": "off@example.test"}, 503)
	})
	test.Run("missing verification cannot register", func(test *testing.T) {
		call(test, fixture, "POST", "/api/v1/auth/register", "", map[string]string{"email": "new@example.test", "name": "新读者", "password": "a-safe-reader-password", "code": "000000"}, 400)
	})
	var delivered string
	fixture.app.sendMail = func(ctx context.Context, config smtpSettings, to, subject, body string) error {
		delivered = body
		return nil
	}
	if err := fixture.app.storeSetting("smtp", smtpSettings{Enabled: true, Host: "smtp.example.test", Port: 587, Mode: "starttls", From: "admin@example.test"}); err != nil {
		test.Fatal(err)
	}
	response := call(test, fixture, "POST", "/api/v1/auth/email-code", "", map[string]string{"email": "new@example.test"}, 200)
	code := strings.Split(strings.Split(delivered, "：")[1], "\r")[0]
	if len(code) != 6 || strings.Contains(fmt.Sprint(response), code) {
		test.Fatal("verification code must be delivered by email only")
	}
	var stored string
	fixture.app.db.QueryRow("SELECT hash FROM email_codes WHERE email=?", "new@example.test").Scan(&stored)
	if stored == code {
		test.Fatal("plaintext email code persisted")
	}
	input := map[string]string{"email": "new@example.test", "name": "新读者", "password": "a-safe-reader-password", "code": code, "client": "android"}
	result := call(test, fixture, "POST", "/api/v1/auth/register", "", input, 200)
	if result["user"].(map[string]any)["role"] != "reader" {
		test.Fatal("reader registration elevated privileges")
	}
	test.Run("code consumed once", func(test *testing.T) { call(test, fixture, "POST", "/api/v1/auth/register", "", input, 400) })
	test.Run("reader forbidden from management", func(test *testing.T) {
		call(test, fixture, "GET", "/api/v1/admin/settings", result["token"].(string), nil, 403)
	})
	test.Run("email rate limited", func(test *testing.T) {
		call(test, fixture, "POST", "/api/v1/auth/email-code", "", map[string]string{"email": "new@example.test"}, 429)
	})
	test.Run("SMTP failure invalidates code", func(test *testing.T) {
		fixture.app.sendMail = func(context.Context, smtpSettings, string, string, string) error { return fmt.Errorf("SMTP down") }
		call(test, fixture, "POST", "/api/v1/auth/email-code", "", map[string]string{"email": "failed@example.test"}, 502)
		var count int
		fixture.app.db.QueryRow("SELECT COUNT(*) FROM email_codes WHERE email=?", "failed@example.test").Scan(&count)
		if count != 0 {
			test.Fatal("failed send left a usable code")
		}
	})
}

func TestContentAndSessionBoundaries(test *testing.T) {
	fixture := newFixture(test)
	readerToken := createReader(test, fixture, "reader@example.test")
	draft := map[string]any{"title": "草稿", "markdown": "## 测试\n\n<script>alert(1)</script>", "description": "内容简介", "category": "随笔", "tags": []string{}, "date": "2026-09-30", "status": "draft"}
	created := call(test, fixture, "POST", "/api/v1/admin/posts", fixture.adminToken, draft, 200)
	id := created["id"].(string)
	test.Run("draft detail is private", func(test *testing.T) { call(test, fixture, "GET", "/api/v1/posts/"+id, "", nil, 404) })
	test.Run("reader cannot publish", func(test *testing.T) { call(test, fixture, "POST", "/api/v1/admin/posts", readerToken, draft, 403) })
	draft["status"] = "published"
	draft["revision"] = 1
	call(test, fixture, "PUT", "/api/v1/admin/posts/"+id, fixture.adminToken, draft, 200)
	result := call(test, fixture, "GET", "/api/v1/posts/"+id, "", nil, 200)
	if strings.Contains(result["post"].(map[string]any)["html"].(string), "<script>") {
		test.Fatal("unsafe HTML rendered")
	}
	test.Run("optimistic revision prevents overwrite", func(test *testing.T) {
		call(test, fixture, "PUT", "/api/v1/admin/posts/"+id, fixture.adminToken, draft, 409)
	})
	call(test, fixture, "PUT", "/api/v1/me/bookmarks/"+id, readerToken, map[string]any{}, 200)
	bookmarks := call(test, fixture, "GET", "/api/v1/me/bookmarks", readerToken, nil, 200)
	if len(bookmarks["posts"].([]any)) != 1 {
		test.Fatal("bookmark not saved")
	}
	comment := call(test, fixture, "POST", "/api/v1/posts/"+id+"/comments", readerToken, map[string]string{"text": "一条真实留言"}, 201)
	call(test, fixture, "DELETE", "/api/v1/admin/comments/"+comment["id"].(string), readerToken, nil, 403)
	call(test, fixture, "DELETE", "/api/v1/admin/comments/"+comment["id"].(string), fixture.adminToken, nil, 200)
	test.Run("comment author cannot be forged", func(test *testing.T) {
		call(test, fixture, "POST", "/api/v1/posts/"+id+"/comments", readerToken, map[string]string{"text": "伪造", "user_id": "admin"}, 400)
	})
	test.Run("cross-origin requests blocked", func(test *testing.T) {
		request := httptest.NewRequest("POST", "/api/v1/auth/login", strings.NewReader("{}"))
		request.Header.Set("Origin", "https://evil.example")
		response := httptest.NewRecorder()
		fixture.handler.ServeHTTP(response, request)
		if response.Code != 403 {
			test.Fatal(response.Code)
		}
	})
	test.Run("cookie mutation requires CSRF", func(test *testing.T) {
		request := httptest.NewRequest("POST", "/api/v1/auth/logout", strings.NewReader("{}"))
		request.AddCookie(&http.Cookie{Name: "kukie_session", Value: readerToken})
		request.Header.Set("Origin", "http://localhost:8085")
		response := httptest.NewRecorder()
		fixture.handler.ServeHTTP(response, request)
		if response.Code != 403 {
			test.Fatal(response.Code)
		}
		request = httptest.NewRequest("POST", "/api/v1/auth/logout", strings.NewReader("{}"))
		request.AddCookie(&http.Cookie{Name: "kukie_session", Value: readerToken})
		request.Header.Set("Origin", "http://localhost:8085")
		request.Header.Set("X-CSRF-Token", fixture.app.keyed("csrf:"+readerToken))
		response = httptest.NewRecorder()
		fixture.handler.ServeHTTP(response, request)
		if response.Code != 200 {
			test.Fatal(response.Code)
		}
	})
	call(test, fixture, "GET", "/api/v1/me", readerToken, nil, 401)
	call(test, fixture, "DELETE", "/api/v1/admin/posts/"+id, fixture.adminToken, nil, 200)
	call(test, fixture, "GET", "/api/v1/posts/"+id, "", nil, 404)
}

func TestOptionalTOTPAndRecovery(test *testing.T) {
	fixture := newFixture(test)
	token := createReader(test, fixture, "mfa@example.test")
	login := map[string]string{"email": "mfa@example.test", "password": "a-safe-reader-password", "client": "android"}
	call(test, fixture, "POST", "/api/v1/auth/login", "", login, 200)
	setup := call(test, fixture, "POST", "/api/v1/me/2fa/setup", token, map[string]string{"password": login["password"]}, 200)
	code, err := totp.GenerateCode(setup["secret"].(string), time.Now())
	if err != nil {
		test.Fatal(err)
	}
	enabled := call(test, fixture, "POST", "/api/v1/me/2fa/enable", token, map[string]string{"code": code}, 200)
	codes := enabled["recovery_codes"].([]any)
	if len(codes) != 8 {
		test.Fatal("recovery codes missing")
	}
	test.Run("password alone insufficient after enable", func(test *testing.T) {
		result := call(test, fixture, "POST", "/api/v1/auth/login", "", login, 401)
		if result["code"] != "totp_required" {
			test.Fatal(result)
		}
	})
	test.Run("TOTP replay rejected", func(test *testing.T) {
		login["otp"] = code
		call(test, fixture, "POST", "/api/v1/auth/login", "", login, 401)
	})
	login["otp"] = codes[0].(string)
	signed := call(test, fixture, "POST", "/api/v1/auth/login", "", login, 200)
	token = signed["token"].(string)
	test.Run("recovery code single use", func(test *testing.T) { call(test, fixture, "POST", "/api/v1/auth/login", "", login, 401) })
	test.Run("disable needs password and factor", func(test *testing.T) {
		call(test, fixture, "POST", "/api/v1/me/2fa/disable", token, map[string]string{"password": "wrong", "code": codes[1].(string)}, 401)
	})
	call(test, fixture, "POST", "/api/v1/me/2fa/disable", token, map[string]string{"password": login["password"], "code": codes[1].(string)}, 200)
	delete(login, "otp")
	call(test, fixture, "POST", "/api/v1/auth/login", "", login, 200)
}

func TestSettingsAndMediaProviders(test *testing.T) {
	fixture := newFixture(test)
	settings := smtpSettings{Enabled: true, Host: "smtp.example.test", Port: 587, Mode: "starttls", From: "admin@example.test", Password: "private-smtp-key"}
	call(test, fixture, "PUT", "/api/v1/admin/settings/smtp", fixture.adminToken, settings, 200)
	result := call(test, fixture, "GET", "/api/v1/admin/settings", fixture.adminToken, nil, 200)
	encoded, _ := json.Marshal(result)
	if strings.Contains(string(encoded), "private-smtp-key") {
		test.Fatal("secret leaked")
	}
	var sealed string
	fixture.app.db.QueryRow("SELECT value FROM settings WHERE name='smtp'").Scan(&sealed)
	if strings.Contains(sealed, "private-smtp-key") {
		test.Fatal("plaintext secret in SQLite")
	}
	settings.Password = ""
	call(test, fixture, "PUT", "/api/v1/admin/settings/smtp", fixture.adminToken, settings, 200)
	loaded, _ := fixture.app.smtpConfig()
	if loaded.Password != "private-smtp-key" {
		test.Fatal("blank erased secret")
	}
	test.Run("private IPs denied by default", func(test *testing.T) {
		for _, ip := range []string{"127.0.0.1", "::1", "10.0.0.2", "169.254.169.254"} {
			if fixture.app.allowedIP(netip.MustParseAddr(ip)) {
				test.Fatal(ip)
			}
		}
	})
	objects := map[string][]byte{}
	webdav := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		username, password, ok := request.BasicAuth()
		if !ok || username != "reader" || password != "dav-password" {
			writer.WriteHeader(401)
			return
		}
		switch request.Method {
		case "PUT":
			objects[request.URL.Path], _ = io.ReadAll(request.Body)
			writer.WriteHeader(201)
		case "GET":
			data, exists := objects[request.URL.Path]
			if !exists {
				writer.WriteHeader(404)
				return
			}
			writer.Write(data)
		case "PROPFIND":
			writer.WriteHeader(207)
		default:
			writer.WriteHeader(405)
		}
	}))
	defer webdav.Close()
	fixture.app.allowPrivateStorage = true
	config := storageSettings{Active: "webdav", WebDAV: webdavSettings{URL: webdav.URL + "/images", Username: "reader", Password: "dav-password"}}
	call(test, fixture, "PUT", "/api/v1/admin/settings/storage", fixture.adminToken, config, 200)
	call(test, fixture, "POST", "/api/v1/admin/settings/storage/test", fixture.adminToken, map[string]any{}, 200)
	if err := fixture.app.putImage(context.Background(), config, "test.png", "image/png", []byte("picture")); err != nil {
		test.Fatal(err)
	}
	stream, err := fixture.app.openImage(context.Background(), config, "test.png")
	if err != nil {
		test.Fatal(err)
	}
	data, _ := io.ReadAll(stream)
	stream.Close()
	if string(data) != "picture" {
		test.Fatal("WebDAV data mismatch")
	}
	s3Server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if !strings.HasPrefix(request.Header.Get("Authorization"), "AWS4-HMAC-SHA256 ") {
			test.Error("S3 request not signed")
			writer.WriteHeader(403)
			return
		}
		if request.Method == "PUT" {
			objects[request.URL.Path], _ = io.ReadAll(request.Body)
			writer.Header().Set("ETag", `"example"`)
			writer.WriteHeader(200)
			return
		}
		writer.WriteHeader(200)
	}))
	defer s3Server.Close()
	s3Config := storageSettings{Active: "s3", S3: s3Settings{Endpoint: s3Server.URL, Region: "us-east-1", Bucket: "pictures", AccessKey: "access", SecretKey: "secret", PathStyle: true}}
	if err := fixture.app.putImage(context.Background(), s3Config, "upload.png", "image/png", []byte("image")); err != nil {
		test.Fatal(err)
	}
	if _, exists := objects["/pictures/kukie/upload.png"]; !exists {
		test.Fatal("S3 path-style object not uploaded")
	}
	test.Run("local avatar upload and MIME validation", func(test *testing.T) {
		if err := fixture.app.storeSetting("storage", storageSettings{Active: "local"}); err != nil {
			test.Fatal(err)
		}
		bitmap := image.NewRGBA(image.Rect(0, 0, 4, 4))
		bitmap.Set(1, 1, color.RGBA{R: 180, A: 255})
		var imageData bytes.Buffer
		png.Encode(&imageData, bitmap)
		var body bytes.Buffer
		form := multipart.NewWriter(&body)
		part, _ := form.CreateFormFile("file", "avatar.png")
		part.Write(imageData.Bytes())
		form.Close()
		request := httptest.NewRequest("POST", "/api/v1/me/avatar", &body)
		request.Header.Set("Authorization", "Bearer "+fixture.adminToken)
		request.Header.Set("Content-Type", form.FormDataContentType())
		response := httptest.NewRecorder()
		fixture.handler.ServeHTTP(response, request)
		if response.Code != 201 {
			test.Fatal(response.Code, response.Body.String())
		}
		var result map[string]string
		json.Unmarshal(response.Body.Bytes(), &result)
		request = httptest.NewRequest("GET", result["url"], nil)
		response = httptest.NewRecorder()
		fixture.handler.ServeHTTP(response, request)
		if response.Code != 200 || response.Header().Get("Content-Type") != "image/jpeg" {
			test.Fatal(response.Code)
		}
		decoded, _, err := image.DecodeConfig(response.Body)
		if err != nil || decoded.Width != 256 {
			test.Fatal("avatar not resized", err)
		}
	})
}

func TestBootstrapAndHugoImport(test *testing.T) {
	directory := test.TempDir()
	postDir := filepath.Join(directory, "content", "posts")
	os.MkdirAll(postDir, 0700)
	os.WriteFile(filepath.Join(postDir, "hello.md"), []byte("---\ntitle: Hello\ndate: 2026-09-30\ncategories: [Notes]\n---\n\nFirst post."), 0600)
	app, err := openServer(filepath.Join(directory, "data"), directory)
	if err != nil {
		test.Fatal(err)
	}
	defer app.db.Close()
	fixture := fixture{app: app, handler: app.routes()}
	if err = app.importPosts(); err != nil {
		test.Fatal(err)
	}
	if err = app.importPosts(); err != nil {
		test.Fatal(err)
	}
	var count int
	app.db.QueryRow("SELECT COUNT(*) FROM posts").Scan(&count)
	if count != 1 {
		test.Fatal("import not idempotent")
	}
	setup := map[string]string{"token": app.setupToken, "email": "owner@example.test", "name": "小站站长", "password": "a-safe-owner-password"}
	call(test, fixture, "POST", "/api/v1/setup", "", setup, 200)
	call(test, fixture, "POST", "/api/v1/setup", "", setup, 409)
	if _, err = os.Stat(filepath.Join(app.directory, "setup-token")); !os.IsNotExist(err) {
		test.Fatal("setup token not removed")
	}
}
