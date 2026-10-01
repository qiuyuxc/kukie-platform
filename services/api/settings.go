package main

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"mime"
	"net"
	"net/http"
	"net/mail"
	"net/smtp"
	"strconv"
	"strings"
	"time"
)

type smtpSettings struct {
	Enabled     bool   `json:"enabled"`
	Host        string `json:"host"`
	Port        int    `json:"port"`
	Mode        string `json:"mode"`
	Username    string `json:"username"`
	Password    string `json:"password,omitempty"`
	From        string `json:"from"`
	FromName    string `json:"from_name"`
	HasPassword bool   `json:"has_password,omitempty"`
}
type s3Settings struct {
	Endpoint  string `json:"endpoint"`
	Region    string `json:"region"`
	Bucket    string `json:"bucket"`
	AccessKey string `json:"access_key"`
	SecretKey string `json:"secret_key,omitempty"`
	PathStyle bool   `json:"path_style"`
	HasSecret bool   `json:"has_secret,omitempty"`
}
type webdavSettings struct {
	URL         string `json:"url"`
	Username    string `json:"username"`
	Password    string `json:"password,omitempty"`
	HasPassword bool   `json:"has_password,omitempty"`
}
type storageSettings struct {
	Active string         `json:"active"`
	S3     s3Settings     `json:"s3"`
	WebDAV webdavSettings `json:"webdav"`
}

func (app *server) loadSetting(name string, target any) error {
	var sealed string
	err := app.db.QueryRow("SELECT value FROM settings WHERE name=?", name).Scan(&sealed)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	data, err := app.unseal(sealed)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, target)
}
func (app *server) storeSetting(name string, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	sealed, err := app.seal(data)
	if err != nil {
		return err
	}
	_, err = app.db.Exec("INSERT INTO settings(name,value) VALUES(?,?) ON CONFLICT(name) DO UPDATE SET value=excluded.value", name, sealed)
	return err
}
func (app *server) smtpConfig() (smtpSettings, error) {
	config := smtpSettings{Port: 587, Mode: "starttls", FromName: "Kukie"}
	err := app.loadSetting("smtp", &config)
	return config, err
}
func (app *server) storageConfig() (storageSettings, error) {
	config := storageSettings{Active: "local", S3: s3Settings{Region: "us-east-1", PathStyle: true}}
	err := app.loadSetting("storage", &config)
	return config, err
}
func (app *server) publicConfig(writer http.ResponseWriter, request *http.Request) {
	config, err := app.smtpConfig()
	if databaseFailure(writer, err) {
		return
	}
	siteURL := ""
	if app.site != nil {
		siteURL = app.site.manifest.BaseURL
	}
	respond(writer, 200, map[string]any{"name": "Kukie", "registration_enabled": config.Enabled, "email_verification_required": true, "optional_2fa": true, "site_url": siteURL})
}
func (app *server) getSettings(writer http.ResponseWriter, request *http.Request, who identity) {
	smtpConfig, err := app.smtpConfig()
	if databaseFailure(writer, err) {
		return
	}
	storage, err := app.storageConfig()
	if databaseFailure(writer, err) {
		return
	}
	smtpConfig.HasPassword = smtpConfig.Password != ""
	smtpConfig.Password = ""
	storage.S3.HasSecret = storage.S3.SecretKey != ""
	storage.S3.SecretKey = ""
	storage.WebDAV.HasPassword = storage.WebDAV.Password != ""
	storage.WebDAV.Password = ""
	respond(writer, 200, map[string]any{"smtp": smtpConfig, "storage": storage, "private_storage_allowed": app.allowPrivateStorage})
}
func (app *server) saveSMTP(writer http.ResponseWriter, request *http.Request, who identity) {
	var input smtpSettings
	if !readJSON(writer, request, &input) {
		return
	}
	previous, err := app.smtpConfig()
	if databaseFailure(writer, err) {
		return
	}
	if input.Password == "" {
		input.Password = previous.Password
	}
	input.HasPassword = false
	input.Host = strings.TrimSpace(input.Host)
	if input.Enabled {
		if input.Host == "" || strings.ContainsAny(input.Host, "/\r\n:@") || input.Port < 1 || input.Port > 65535 || normalizeEmail(input.From) == "" || (input.Mode != "tls" && input.Mode != "starttls") || strings.ContainsAny(input.FromName, "\r\n") {
			fail(writer, 400, "invalid_smtp", "请填写有效 SMTP 主机、端口、发件邮箱，并使用 TLS 或 STARTTLS")
			return
		}
	}
	if databaseFailure(writer, app.storeSetting("smtp", input)) {
		return
	}
	app.audit(who, "settings.smtp", "smtp")
	respond(writer, 200, map[string]bool{"ok": true})
}
func (app *server) testSMTP(writer http.ResponseWriter, request *http.Request, who identity) {
	if app.limited(writer, "smtp-test:"+who.User.ID, 3, time.Minute) {
		return
	}
	config, err := app.smtpConfig()
	if databaseFailure(writer, err) {
		return
	}
	if !config.Enabled {
		fail(writer, 400, "smtp_disabled", "请先保存并启用 SMTP")
		return
	}
	ctx, cancel := context.WithTimeout(request.Context(), 20*time.Second)
	defer cancel()
	if err = app.sendMail(ctx, config, who.User.Email, "Kukie 邮件配置测试", "如果你收到了这封邮件，说明 SMTP 投递已连通。此邮件由管理员主动测试触发。"); err != nil {
		fail(writer, 502, "smtp_failed", "邮件服务未接受测试邮件，请检查主机、TLS、授权码与发件人配置")
		return
	}
	respond(writer, 200, map[string]string{"message": "SMTP 已接受测试邮件，请到管理员邮箱确认送达"})
}
func (app *server) emailCode(writer http.ResponseWriter, request *http.Request) {
	if app.limited(writer, "email-ip:"+clientIP(request), 10, time.Hour) {
		return
	}
	var input struct {
		Email string `json:"email"`
	}
	if !readJSON(writer, request, &input) {
		return
	}
	email := normalizeEmail(input.Email)
	if email == "" {
		fail(writer, 400, "invalid_email", "请填写有效邮箱")
		return
	}
	if app.limited(writer, "email-address:"+email, 1, time.Minute) {
		return
	}
	config, err := app.smtpConfig()
	if databaseFailure(writer, err) {
		return
	}
	if !config.Enabled {
		fail(writer, 503, "registration_unavailable", "暂未开放注册，管理员需要先配置邮件服务")
		return
	}
	var existing int
	if databaseFailure(writer, app.db.QueryRow("SELECT COUNT(*) FROM users WHERE email=?", email).Scan(&existing)) {
		return
	}
	if existing > 0 {
		respond(writer, 200, map[string]string{"message": "若邮箱可以注册，验证码将发送至该邮箱，请检查收件箱"})
		return
	}
	number, err := rand.Int(rand.Reader, big.NewInt(1000000))
	if err != nil {
		fail(writer, 500, "random_error", "无法生成验证码")
		return
	}
	code := fmt.Sprintf("%06d", number.Int64())
	hash := app.keyed("email:" + email + ":" + code)
	_, err = app.db.Exec("INSERT INTO email_codes(email,hash,expires,attempts) VALUES(?,?,?,0) ON CONFLICT(email) DO UPDATE SET hash=excluded.hash,expires=excluded.expires,attempts=0", email, hash, time.Now().Add(10*time.Minute).Unix())
	if databaseFailure(writer, err) {
		return
	}
	ctx, cancel := context.WithTimeout(request.Context(), 20*time.Second)
	defer cancel()
	err = app.sendMail(ctx, config, email, "Kukie 注册验证码", "你的注册验证码是："+code+"\r\n\r\n10 分钟内有效，仅可使用一次。请勿转发或向他人透露。\r\n如果不是你发起的注册，请忽略此邮件。")
	if err != nil {
		_, _ = app.db.Exec("DELETE FROM email_codes WHERE email=? AND hash=?", email, hash)
		fail(writer, 502, "email_failed", "验证码发送失败，请稍后重试或联系管理员")
		return
	}
	respond(writer, 200, map[string]string{"message": "若邮箱可以注册，验证码将发送至该邮箱，请检查收件箱"})
}
func deliverMail(ctx context.Context, config smtpSettings, recipient, subject, body string) error {
	if normalizeEmail(recipient) == "" || normalizeEmail(config.From) == "" {
		return errors.New("invalid mail address")
	}
	address := net.JoinHostPort(config.Host, strconv.Itoa(config.Port))
	dialer := &net.Dialer{Timeout: 15 * time.Second}
	var connection net.Conn
	var err error
	tlsConfig := &tls.Config{ServerName: config.Host, MinVersion: tls.VersionTLS12}
	if config.Mode == "tls" {
		connection, err = (&tls.Dialer{NetDialer: dialer, Config: tlsConfig}).DialContext(ctx, "tcp", address)
	} else {
		connection, err = dialer.DialContext(ctx, "tcp", address)
	}
	if err != nil {
		return err
	}
	defer connection.Close()
	stopCancellation := context.AfterFunc(ctx, func() { _ = connection.Close() })
	defer stopCancellation()
	deadline := time.Now().Add(20 * time.Second)
	if contextDeadline, ok := ctx.Deadline(); ok && contextDeadline.Before(deadline) {
		deadline = contextDeadline
	}
	_ = connection.SetDeadline(deadline)
	client, err := smtp.NewClient(connection, config.Host)
	if err != nil {
		return err
	}
	defer client.Close()
	if config.Mode == "starttls" {
		if ok, _ := client.Extension("STARTTLS"); !ok {
			return errors.New("SMTP server lacks STARTTLS")
		}
		if err = client.StartTLS(tlsConfig); err != nil {
			return err
		}
	} else if config.Mode != "tls" {
		return errors.New("TLS is required")
	}
	if config.Username != "" {
		if err = client.Auth(smtp.PlainAuth("", config.Username, config.Password, config.Host)); err != nil {
			return err
		}
	}
	if err = client.Mail(config.From); err != nil {
		return err
	}
	if err = client.Rcpt(recipient); err != nil {
		return err
	}
	writer, err := client.Data()
	if err != nil {
		return err
	}
	from := (&mail.Address{Name: config.FromName, Address: config.From}).String()
	message := "From: " + from + "\r\nTo: " + recipient + "\r\nSubject: " + mime.QEncoding.Encode("utf-8", subject) + "\r\nDate: " + time.Now().Format(time.RFC1123Z) + "\r\nMIME-Version: 1.0\r\nContent-Type: text/plain; charset=UTF-8\r\nContent-Transfer-Encoding: 8bit\r\n\r\n" + body + "\r\n"
	if _, err = writer.Write([]byte(message)); err != nil {
		return err
	}
	if err = writer.Close(); err != nil {
		return err
	}
	return client.Quit()
}
