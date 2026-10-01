package main

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

type server struct {
	site                *blogSite
	db                  *sql.DB
	directory           string
	repository          string
	key                 []byte
	setupToken          string
	origins             map[string]bool
	secureCookie        bool
	allowPrivateStorage bool
	limiterMu           sync.Mutex
	buckets             map[string]rateBucket
	sendMail            func(context.Context, smtpSettings, string, string, string) error
	accountMu           sync.Mutex
}

type rateBucket struct {
	Count int
	Until time.Time
}

func main() {
	repository, err := filepath.Abs(env("KUKIE_REPOSITORY", "."))
	if err != nil {
		log.Fatal(err)
	}
	directory := env("KUKIE_DATA_DIR", filepath.Join(repository, ".local", "kukie"))
	app, err := openServer(directory, repository)
	if err != nil {
		log.Fatal(err)
	}
	defer app.db.Close()
	if err := app.importPosts(); err != nil {
		log.Fatal("import Hugo content: ", err)
	}
	if err := app.configureSite(); err != nil {
		log.Fatal("load site: ", err)
	}
	if err := app.configureIndexNow(); err != nil {
		log.Fatal("configure IndexNow: ", err)
	}
	if app.site != nil {
		defer app.site.root.Close()
	}
	api := &http.Server{Addr: env("KUKIE_API_ADDR", "127.0.0.1:8084"), Handler: app.routes(), ReadHeaderTimeout: 10 * time.Second, ReadTimeout: 40 * time.Second, WriteTimeout: 60 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16384}
	console := &http.Server{Addr: env("KUKIE_CONSOLE_ADDR", "127.0.0.1:8085"), Handler: app.consoleHandler(), ReadHeaderTimeout: 10 * time.Second, ReadTimeout: 40 * time.Second, WriteTimeout: 60 * time.Second, IdleTimeout: 60 * time.Second}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	instances := []*http.Server{api, console}
	if app.site != nil {
		instances = append(instances, &http.Server{Addr: env("KUKIE_SITE_ADDR", "127.0.0.1:8086"), Handler: app.siteHandler(), ReadHeaderTimeout: 10 * time.Second, ReadTimeout: 40 * time.Second, WriteTimeout: 60 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16384})
	}
	for _, instance := range instances {
		go func(instance *http.Server) {
			log.Printf("Listening on http://%s", instance.Addr)
			if err := instance.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
				log.Print(err)
				stop()
			}
		}(instance)
	}
	if app.setupToken != "" {
		log.Printf("First-run setup token: %s (read locally; no default administrator)", filepath.Join(directory, "setup-token"))
	}
	backgroundDone := app.startMaintenance(ctx)
	<-ctx.Done()
	shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for _, instance := range instances {
		_ = instance.Shutdown(shutdown)
	}
	<-backgroundDone
}

func env(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}

func openServer(directory, repository string) (*server, error) {
	if err := os.MkdirAll(directory, 0700); err != nil {
		return nil, err
	}
	if err := os.Chmod(directory, 0700); err != nil {
		return nil, err
	}
	keyPath := filepath.Join(directory, "master.key")
	key, err := os.ReadFile(keyPath)
	if errors.Is(err, os.ErrNotExist) {
		key = make([]byte, 32)
		if _, err = rand.Read(key); err != nil {
			return nil, err
		}
		if err = os.WriteFile(keyPath, key, 0600); err != nil {
			return nil, err
		}
	} else if err != nil {
		return nil, err
	}
	if len(key) != 32 {
		return nil, errors.New("master.key must contain exactly 32 bytes; do not replace an existing key")
	}
	database, err := sql.Open("sqlite3", filepath.Join(directory, "app.db")+"?_foreign_keys=on&_journal_mode=WAL&_busy_timeout=5000&_txlock=immediate")
	if err != nil {
		return nil, err
	}
	database.SetMaxOpenConns(1)
	if _, err = database.Exec(schema); err != nil {
		database.Close()
		return nil, err
	}
	if err = os.Chmod(filepath.Join(directory, "app.db"), 0600); err != nil {
		database.Close()
		return nil, err
	}
	app := &server{db: database, directory: directory, repository: repository, key: key, buckets: make(map[string]rateBucket), origins: make(map[string]bool), secureCookie: os.Getenv("KUKIE_SECURE_COOKIE") == "1", allowPrivateStorage: os.Getenv("KUKIE_ALLOW_PRIVATE_STORAGE") == "1", sendMail: deliverMail}
	for _, origin := range strings.Split(env("KUKIE_CONSOLE_ORIGINS", "http://localhost:8085,http://127.0.0.1:8085"), ",") {
		app.origins[strings.TrimSpace(origin)] = true
	}
	var admins int
	if err = database.QueryRow("SELECT COUNT(*) FROM users WHERE role='admin'").Scan(&admins); err != nil {
		database.Close()
		return nil, err
	}
	if admins == 0 {
		tokenPath := filepath.Join(directory, "setup-token")
		token, readErr := os.ReadFile(tokenPath)
		if errors.Is(readErr, os.ErrNotExist) {
			token = []byte(randomToken())
			readErr = os.WriteFile(tokenPath, token, 0600)
		}
		if readErr != nil {
			database.Close()
			return nil, readErr
		}
		app.setupToken = strings.TrimSpace(string(token))
	}
	return app, nil
}

func randomToken() string { return base64.RawURLEncoding.EncodeToString(randomBytes(32)) }
func randomBytes(length int) []byte {
	data := make([]byte, length)
	if _, err := rand.Read(data); err != nil {
		panic(err)
	}
	return data
}

func respond(writer http.ResponseWriter, status int, data any) {
	writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	writer.Header().Set("Cache-Control", "no-store")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(data)
}
func fail(writer http.ResponseWriter, status int, code, message string) {
	respond(writer, status, map[string]string{"code": code, "error": message})
}
func readJSON(writer http.ResponseWriter, request *http.Request, target any) bool {
	if !strings.HasPrefix(request.Header.Get("Content-Type"), "application/json") {
		fail(writer, 415, "json_required", "请使用 JSON 请求")
		return false
	}
	request.Body = http.MaxBytesReader(writer, request.Body, 2<<20)
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		fail(writer, 400, "invalid_body", "请求内容不完整或格式错误")
		return false
	}
	return true
}

func (app *server) limited(writer http.ResponseWriter, key string, maximum int, interval time.Duration) bool {
	app.limiterMu.Lock()
	defer app.limiterMu.Unlock()
	now := time.Now()
	if len(app.buckets) > 10000 {
		for identity, bucket := range app.buckets {
			if now.After(bucket.Until) {
				delete(app.buckets, identity)
			}
		}
	}
	if len(app.buckets) > 20000 {
		fail(writer, 429, "busy", "请求过多，请稍后重试")
		return true
	}
	bucket := app.buckets[key]
	if now.After(bucket.Until) {
		bucket = rateBucket{Until: now.Add(interval)}
	}
	bucket.Count++
	app.buckets[key] = bucket
	if bucket.Count > maximum {
		writer.Header().Set("Retry-After", fmt.Sprint(int(time.Until(bucket.Until).Seconds())+1))
		fail(writer, 429, "rate_limited", "尝试过于频繁，请稍后再试")
		return true
	}
	return false
}
func clientIP(request *http.Request) string {
	host, _, err := net.SplitHostPort(request.RemoteAddr)
	if err != nil {
		return request.RemoteAddr
	}
	return host
}

func (app *server) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/health", func(writer http.ResponseWriter, request *http.Request) {
		respond(writer, 200, map[string]any{"ok": true, "version": "0.1.0"})
	})
	mux.HandleFunc("GET /api/v1/setup", app.setupStatus)
	mux.HandleFunc("POST /api/v1/setup", app.setup)
	mux.HandleFunc("GET /api/v1/config", app.publicConfig)
	mux.HandleFunc("POST /api/v1/auth/email-code", app.emailCode)
	mux.HandleFunc("POST /api/v1/auth/register", app.register)
	mux.HandleFunc("POST /api/v1/auth/login", app.login)
	mux.HandleFunc("POST /api/v1/auth/quick-login", app.quickLogin)
	mux.HandleFunc("POST /api/v1/auth/forget", app.forgetAccount)
	mux.HandleFunc("POST /api/v1/auth/logout", app.auth(app.logout, false))
	mux.HandleFunc("GET /api/v1/me", app.auth(app.me, false))
	mux.HandleFunc("PATCH /api/v1/me", app.auth(app.updateProfile, false))
	mux.HandleFunc("PUT /api/v1/me/password", app.auth(app.changePassword, false))
	mux.HandleFunc("POST /api/v1/me/email-code", app.auth(app.emailChangeCode, false))
	mux.HandleFunc("PUT /api/v1/me/email", app.auth(app.changeEmail, false))
	mux.HandleFunc("DELETE /api/v1/me", app.auth(app.deleteAccount, false))
	mux.HandleFunc("GET /api/v1/me/subscription", app.auth(app.getSubscription, false))
	mux.HandleFunc("PUT /api/v1/me/subscription", app.auth(app.saveSubscription, false))
	mux.HandleFunc("POST /api/v1/me/avatar", app.auth(app.uploadAvatar, false))
	mux.HandleFunc("POST /api/v1/me/2fa/setup", app.auth(app.setupTOTP, false))
	mux.HandleFunc("POST /api/v1/me/2fa/enable", app.auth(app.enableTOTP, false))
	mux.HandleFunc("POST /api/v1/me/2fa/disable", app.auth(app.disableTOTP, false))
	mux.HandleFunc("GET /api/v1/posts", app.listPosts)
	mux.HandleFunc("GET /api/v1/posts/by-slug", app.postBySlug)
	mux.HandleFunc("GET /api/v1/posts/{id}", app.getPost)
	mux.HandleFunc("GET /api/v1/posts/{id}/engagement", app.postEngagement)
	mux.HandleFunc("POST /api/v1/posts/{id}/views", app.postEngagement)
	mux.HandleFunc("PUT /api/v1/me/likes/{id}", app.auth(app.likePost, false))
	mux.HandleFunc("DELETE /api/v1/me/likes/{id}", app.auth(app.likePost, false))
	mux.HandleFunc("GET /api/v1/notices", app.listNotices)
	mux.HandleFunc("GET /api/v1/notices/{id}", app.getNotice)
	mux.HandleFunc("GET /api/v1/posts/{id}/comments", app.listComments)
	mux.HandleFunc("POST /api/v1/posts/{id}/comments", app.auth(app.createComment, false))
	mux.HandleFunc("GET /api/v1/me/bookmarks", app.auth(app.listBookmarks, false))
	mux.HandleFunc("PUT /api/v1/me/bookmarks/{id}", app.auth(app.bookmark, false))
	mux.HandleFunc("DELETE /api/v1/me/bookmarks/{id}", app.auth(app.bookmark, false))
	mux.HandleFunc("GET /api/v1/admin/posts", app.auth(app.adminPosts, true))
	mux.HandleFunc("POST /api/v1/admin/posts", app.auth(app.savePost, true))
	mux.HandleFunc("PUT /api/v1/admin/posts/{id}", app.auth(app.savePost, true))
	mux.HandleFunc("DELETE /api/v1/admin/posts/{id}", app.auth(app.deletePost, true))
	mux.HandleFunc("GET /api/v1/admin/notices", app.auth(app.adminNotices, true))
	mux.HandleFunc("POST /api/v1/admin/notices", app.auth(app.saveNotice, true))
	mux.HandleFunc("PUT /api/v1/admin/notices/{id}", app.auth(app.saveNotice, true))
	mux.HandleFunc("DELETE /api/v1/admin/notices/{id}", app.auth(app.deleteNotice, true))
	mux.HandleFunc("GET /api/v1/admin/comments", app.auth(app.adminComments, true))
	mux.HandleFunc("DELETE /api/v1/admin/comments/{id}", app.auth(app.deleteComment, true))
	mux.HandleFunc("GET /api/v1/admin/users", app.auth(app.listUsers, true))
	mux.HandleFunc("GET /api/v1/admin/settings", app.auth(app.getSettings, true))
	mux.HandleFunc("PUT /api/v1/admin/settings/smtp", app.auth(app.saveSMTP, true))
	mux.HandleFunc("POST /api/v1/admin/settings/smtp/test", app.auth(app.testSMTP, true))
	mux.HandleFunc("PUT /api/v1/admin/settings/storage", app.auth(app.saveStorage, true))
	mux.HandleFunc("POST /api/v1/admin/settings/storage/test", app.auth(app.testStorage, true))
	mux.HandleFunc("POST /api/v1/admin/media", app.auth(app.uploadMedia, true))
	mux.HandleFunc("GET /media/{id}", app.serveMedia)
	mux.Handle("GET /assets/", http.StripPrefix("/assets/", http.FileServer(http.Dir(filepath.Join(app.repository, "static")))))
	return app.middleware(mux)
}

func (app *server) middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("X-Content-Type-Options", "nosniff")
		writer.Header().Set("Referrer-Policy", "no-referrer")
		origin := request.Header.Get("Origin")
		if origin != "" && app.origins[origin] {
			writer.Header().Set("Access-Control-Allow-Origin", origin)
			writer.Header().Set("Access-Control-Allow-Credentials", "true")
			writer.Header().Set("Vary", "Origin")
		}
		if request.Method == "OPTIONS" {
			if !app.origins[origin] {
				fail(writer, 403, "origin_denied", "不允许此来源")
				return
			}
			writer.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE")
			writer.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-CSRF-Token")
			writer.WriteHeader(204)
			return
		}
		if request.Method != "GET" && request.Method != "HEAD" && origin != "" && !app.origins[origin] {
			fail(writer, 403, "origin_denied", "不允许此来源")
			return
		}
		next.ServeHTTP(writer, request)
	})
}

func (app *server) consoleHandler() http.Handler {
	api := app.routes()
	root := filepath.Join(app.repository, "apps", "console", "dist")
	files := http.FileServer(http.Dir(root))
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if strings.HasPrefix(request.URL.Path, "/api/") || strings.HasPrefix(request.URL.Path, "/media/") || strings.HasPrefix(request.URL.Path, "/assets/") {
			api.ServeHTTP(writer, request)
			return
		}
		writer.Header().Set("X-Frame-Options", "DENY")
		writer.Header().Set("X-Content-Type-Options", "nosniff")
		writer.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data: https:; connect-src 'self'; frame-ancestors 'none'; base-uri 'self'; form-action 'self'")
		if request.Method != "GET" && request.Method != "HEAD" {
			writer.WriteHeader(405)
			return
		}
		files.ServeHTTP(writer, request)
	})
}
