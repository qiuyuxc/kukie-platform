package main

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/mail"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/pquerna/otp/totp"
	"golang.org/x/crypto/argon2"
)

type user struct {
	ID          string `json:"id"`
	Email       string `json:"email"`
	Name        string `json:"name"`
	Role        string `json:"role"`
	Bio         string `json:"bio"`
	Avatar      string `json:"avatar"`
	CreatedAt   string `json:"created_at"`
	TwoFactor   bool   `json:"two_factor"`
	Password    string `json:"-"`
	Secret      string `json:"-"`
	LastCounter int64  `json:"-"`
}
type identityKey struct{}
type identity struct {
	User   user
	Token  string
	Cookie bool
}
type authenticated func(http.ResponseWriter, *http.Request, identity)

var passwordWork = make(chan struct{}, 2)

func hashPassword(password string) string {
	passwordWork <- struct{}{}
	defer func() { <-passwordWork }()
	salt := randomBytes(16)
	derived := argon2.IDKey([]byte(password), salt, 2, 64*1024, 2, 32)
	return "argon2id$" + base64.RawStdEncoding.EncodeToString(salt) + "$" + base64.RawStdEncoding.EncodeToString(derived)
}
func verifyPassword(password, encoded string) bool {
	passwordWork <- struct{}{}
	defer func() { <-passwordWork }()
	parts := strings.Split(encoded, "$")
	if len(parts) != 3 || parts[0] != "argon2id" || len(password) > 128 {
		return false
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[1])
	if err != nil || len(salt) != 16 {
		return false
	}
	expected, err := base64.RawStdEncoding.DecodeString(parts[2])
	if err != nil || len(expected) != 32 {
		return false
	}
	actual := argon2.IDKey([]byte(password), salt, 2, 64*1024, 2, 32)
	return subtle.ConstantTimeCompare(actual, expected) == 1
}
func validPassword(password string) bool {
	return utf8.RuneCountInString(password) >= 12 && len(password) <= 128
}
func validName(name string) bool {
	length := utf8.RuneCountInString(strings.TrimSpace(name))
	return length >= 2 && length <= 24
}
func normalizeEmail(email string) string {
	email = strings.ToLower(strings.TrimSpace(email))
	address, err := mail.ParseAddress(email)
	if err != nil || address.Address != email || len(email) > 254 || strings.ContainsAny(email, "\r\n") {
		return ""
	}
	return email
}
func digest(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}
func (app *server) keyed(value string) string {
	mac := hmac.New(sha256.New, app.key)
	mac.Write([]byte(value))
	return hex.EncodeToString(mac.Sum(nil))
}
func (app *server) seal(value []byte) (string, error) {
	block, err := aes.NewCipher(app.key)
	if err != nil {
		return "", err
	}
	mode, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonce := randomBytes(mode.NonceSize())
	return base64.RawStdEncoding.EncodeToString(mode.Seal(nonce, nonce, value, []byte("kukie/v1"))), nil
}
func (app *server) unseal(value string) ([]byte, error) {
	data, err := base64.RawStdEncoding.DecodeString(value)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(app.key)
	if err != nil {
		return nil, err
	}
	mode, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	if len(data) < mode.NonceSize() {
		return nil, errors.New("invalid sealed value")
	}
	return mode.Open(nil, data[:mode.NonceSize()], data[mode.NonceSize():], []byte("kukie/v1"))
}

const userColumns = "id,email,name,role,bio,avatar,created_at,password,totp_secret,totp_last"

type scanner interface{ Scan(...any) error }

func scanUser(row scanner) (user, error) {
	var account user
	err := row.Scan(&account.ID, &account.Email, &account.Name, &account.Role, &account.Bio, &account.Avatar, &account.CreatedAt, &account.Password, &account.Secret, &account.LastCounter)
	account.TwoFactor = account.Secret != ""
	return account, err
}
func (app *server) auth(next authenticated, admin bool) http.HandlerFunc {
	return func(writer http.ResponseWriter, request *http.Request) {
		token := strings.TrimPrefix(request.Header.Get("Authorization"), "Bearer ")
		cookieAuth := request.Header.Get("Authorization") == ""
		if cookieAuth {
			if cookie, err := request.Cookie("kukie_session"); err == nil {
				token = cookie.Value
			}
		}
		if len(token) != 43 {
			fail(writer, 401, "unauthorized", "请先登录")
			return
		}
		var userID string
		if err := app.db.QueryRow("SELECT user_id FROM sessions WHERE hash=? AND expires>?", digest(token), time.Now().Unix()).Scan(&userID); err != nil {
			fail(writer, 401, "session_expired", "登录已过期，请重新登录")
			return
		}
		account, err := scanUser(app.db.QueryRow("SELECT "+userColumns+" FROM users WHERE id=?", userID))
		if err != nil {
			fail(writer, 401, "unauthorized", "请重新登录")
			return
		}
		if admin && account.Role != "admin" {
			fail(writer, 403, "admin_required", "此操作需要管理员权限")
			return
		}
		if cookieAuth && request.Method != "GET" && request.Method != "HEAD" {
			if !app.origins[request.Header.Get("Origin")] || subtle.ConstantTimeCompare([]byte(request.Header.Get("X-CSRF-Token")), []byte(app.keyed("csrf:"+token))) != 1 {
				fail(writer, 403, "csrf_failed", "请求验证失败，请刷新页面")
				return
			}
		}
		who := identity{User: account, Token: token, Cookie: cookieAuth}
		next(writer, request.WithContext(context.WithValue(request.Context(), identityKey{}, who)), who)
	}
}
func (app *server) session(writer http.ResponseWriter, account user, client string, remember bool, previous string) {
	token := randomToken()
	tx, err := app.db.Begin()
	if databaseFailure(writer, err) {
		return
	}
	defer tx.Rollback()
	_, err = tx.Exec("DELETE FROM sessions WHERE expires<=?", time.Now().Unix())
	if databaseFailure(writer, err) {
		return
	}
	_, err = tx.Exec("INSERT INTO sessions(hash,user_id,expires) VALUES(?,?,?)", digest(token), account.ID, time.Now().Add(7*24*time.Hour).Unix())
	if databaseFailure(writer, err) {
		return
	}
	result := map[string]any{"user": account, "expires_in": 604800}
	if client == "android" {
		result["token"] = token
		_, err = tx.Exec("DELETE FROM remembered_accounts WHERE expires<=? OR (user_id=? AND hash=?)", time.Now().Unix(), account.ID, digest(previous))
		if databaseFailure(writer, err) {
			return
		}
		if remember {
			credential := randomToken()
			expires := time.Now().Add(30 * 24 * time.Hour).Unix()
			_, err = tx.Exec("INSERT INTO remembered_accounts(hash,user_id,expires) VALUES(?,?,?)", digest(credential), account.ID, expires)
			if databaseFailure(writer, err) {
				return
			}
			_, err = tx.Exec("DELETE FROM remembered_accounts WHERE user_id=? AND hash NOT IN (SELECT hash FROM remembered_accounts WHERE user_id=? ORDER BY expires DESC, rowid DESC LIMIT 20)", account.ID, account.ID)
			if databaseFailure(writer, err) {
				return
			}
			result["remember_token"] = credential
			result["remember_expires_at"] = expires
		}
	}
	if databaseFailure(writer, tx.Commit()) {
		return
	}
	if client != "android" {
		http.SetCookie(writer, &http.Cookie{Name: "kukie_session", Value: token, Path: "/", MaxAge: 604800, Secure: app.secureCookie, HttpOnly: true, SameSite: http.SameSiteStrictMode})
		result["csrf"] = app.keyed("csrf:" + token)
	}
	respond(writer, 200, result)
}
func (app *server) setupStatus(writer http.ResponseWriter, request *http.Request) {
	var count int
	if err := app.db.QueryRow("SELECT COUNT(*) FROM users WHERE role='admin'").Scan(&count); err != nil {
		fail(writer, 500, "database_error", "无法读取初始化状态")
		return
	}
	respond(writer, 200, map[string]bool{"required": count == 0})
}
func (app *server) setup(writer http.ResponseWriter, request *http.Request) {
	if app.limited(writer, "setup:"+clientIP(request), 5, 15*time.Minute) {
		return
	}
	var input struct {
		Token    string `json:"token"`
		Email    string `json:"email"`
		Name     string `json:"name"`
		Password string `json:"password"`
	}
	if !readJSON(writer, request, &input) {
		return
	}
	if app.setupToken == "" || subtle.ConstantTimeCompare([]byte(input.Token), []byte(app.setupToken)) != 1 {
		fail(writer, 403, "setup_denied", "初始化密钥无效")
		return
	}
	email := normalizeEmail(input.Email)
	if email == "" || !validName(input.Name) || !validPassword(input.Password) {
		fail(writer, 400, "invalid_account", "请使用有效邮箱、2–24 字昵称和 12–128 位密码")
		return
	}
	password := hashPassword(input.Password)
	tx, err := app.db.Begin()
	if err != nil {
		fail(writer, 500, "database_error", "无法初始化")
		return
	}
	defer tx.Rollback()
	var count int
	if err = tx.QueryRow("SELECT COUNT(*) FROM users WHERE role='admin'").Scan(&count); err != nil || count > 0 {
		fail(writer, 409, "setup_complete", "管理员已初始化")
		return
	}
	account := user{ID: randomToken(), Email: email, Name: strings.TrimSpace(input.Name), Role: "admin", CreatedAt: time.Now().UTC().Format(time.RFC3339)}
	_, err = tx.Exec("INSERT INTO users(id,email,name,password,role,created_at) VALUES(?,?,?,?,?,?)", account.ID, account.Email, account.Name, password, account.Role, account.CreatedAt)
	if err != nil {
		fail(writer, 409, "email_exists", "邮箱已存在")
		return
	}
	if err = tx.Commit(); err != nil {
		fail(writer, 500, "database_error", "初始化失败")
		return
	}
	_ = os.Remove(filepath.Join(app.directory, "setup-token"))
	app.session(writer, account, "web", false, "")
}
func (app *server) register(writer http.ResponseWriter, request *http.Request) {
	if app.limited(writer, "register:"+clientIP(request), 10, 15*time.Minute) {
		return
	}
	var input struct {
		Email    string `json:"email"`
		Name     string `json:"name"`
		Password string `json:"password"`
		Code     string `json:"code"`
		Client   string `json:"client"`
		Remember bool   `json:"remember"`
	}
	if !readJSON(writer, request, &input) {
		return
	}
	email := normalizeEmail(input.Email)
	if email == "" || !validName(input.Name) || !validPassword(input.Password) {
		fail(writer, 400, "invalid_account", "邮箱无效，或昵称不在 2–24 字、密码不在 12–128 位范围")
		return
	}
	password := hashPassword(input.Password)
	tx, err := app.db.Begin()
	if err != nil {
		fail(writer, 500, "database_error", "无法注册")
		return
	}
	defer tx.Rollback()
	var expected string
	var expiry int64
	var attempts int
	err = tx.QueryRow("SELECT hash,expires,attempts FROM email_codes WHERE email=?", email).Scan(&expected, &expiry, &attempts)
	if err != nil || expiry <= time.Now().Unix() || attempts >= 5 {
		fail(writer, 400, "invalid_code", "验证码无效或已过期，请重新获取")
		return
	}
	if subtle.ConstantTimeCompare([]byte(expected), []byte(app.keyed("email:"+email+":"+input.Code))) != 1 {
		_, _ = tx.Exec("UPDATE email_codes SET attempts=attempts+1 WHERE email=?", email)
		_ = tx.Commit()
		fail(writer, 400, "invalid_code", "验证码错误")
		return
	}
	account := user{ID: randomToken(), Email: email, Name: strings.TrimSpace(input.Name), Role: "reader", CreatedAt: time.Now().UTC().Format(time.RFC3339)}
	_, err = tx.Exec("INSERT INTO users(id,email,name,password,created_at) VALUES(?,?,?,?,?)", account.ID, email, account.Name, password, account.CreatedAt)
	if err != nil {
		fail(writer, 409, "email_exists", "此邮箱已注册，请登录")
		return
	}
	if _, err = tx.Exec("DELETE FROM email_codes WHERE email=?", email); err != nil {
		fail(writer, 500, "database_error", "无法完成注册")
		return
	}
	if err = tx.Commit(); err != nil {
		fail(writer, 500, "database_error", "无法完成注册")
		return
	}
	app.session(writer, account, input.Client, input.Remember, "")
}
func (app *server) login(writer http.ResponseWriter, request *http.Request) {
	if app.limited(writer, "login-ip:"+clientIP(request), 30, 15*time.Minute) {
		return
	}
	var input struct {
		Email    string `json:"email"`
		Password string `json:"password"`
		OTP      string `json:"otp"`
		Client   string `json:"client"`
		Remember bool   `json:"remember"`
		Previous string `json:"remember_token"`
	}
	if !readJSON(writer, request, &input) {
		return
	}
	email := normalizeEmail(input.Email)
	if app.limited(writer, "login-email:"+email, 15, 15*time.Minute) {
		return
	}
	app.accountMu.Lock()
	defer app.accountMu.Unlock()
	account, err := scanUser(app.db.QueryRow("SELECT "+userColumns+" FROM users WHERE email=?", email))
	if err != nil {
		_ = hashPassword("dummy-password")
		fail(writer, 401, "invalid_credentials", "邮箱或密码不正确")
		return
	}
	if !verifyPassword(input.Password, account.Password) {
		fail(writer, 401, "invalid_credentials", "邮箱或密码不正确")
		return
	}
	if account.TwoFactor {
		if input.OTP == "" {
			fail(writer, 401, "totp_required", "请输入验证器的 6 位验证码或一次性恢复码")
			return
		}
		if !app.consumeFactor(account, input.OTP) {
			fail(writer, 401, "invalid_otp", "两步验证码无效、已使用或过期")
			return
		}
	}
	app.session(writer, account, input.Client, input.Remember, input.Previous)
}
func (app *server) logout(writer http.ResponseWriter, request *http.Request, who identity) {
	var input struct {
		Forget   bool   `json:"forget"`
		Previous string `json:"remember_token"`
	}
	if !who.Cookie && request.ContentLength != 0 && !readJSON(writer, request, &input) {
		return
	}
	app.accountMu.Lock()
	defer app.accountMu.Unlock()
	tx, err := app.db.Begin()
	if databaseFailure(writer, err) {
		return
	}
	defer tx.Rollback()
	_, err = tx.Exec("DELETE FROM sessions WHERE hash=?", digest(who.Token))
	if databaseFailure(writer, err) {
		return
	}
	if input.Forget {
		_, err = tx.Exec("DELETE FROM remembered_accounts WHERE user_id=? AND hash=?", who.User.ID, digest(input.Previous))
		if databaseFailure(writer, err) {
			return
		}
	}
	if databaseFailure(writer, tx.Commit()) {
		return
	}
	http.SetCookie(writer, &http.Cookie{Name: "kukie_session", Path: "/", MaxAge: -1, HttpOnly: true, Secure: app.secureCookie, SameSite: http.SameSiteStrictMode})
	respond(writer, 200, map[string]bool{"ok": true})
}
func (app *server) me(writer http.ResponseWriter, request *http.Request, who identity) {
	respond(writer, 200, map[string]any{"user": who.User, "csrf": app.keyed("csrf:" + who.Token)})
}
func (app *server) updateProfile(writer http.ResponseWriter, request *http.Request, who identity) {
	var input struct {
		Name string `json:"name"`
		Bio  string `json:"bio"`
	}
	if !readJSON(writer, request, &input) {
		return
	}
	if !validName(input.Name) || utf8.RuneCountInString(input.Bio) > 100 {
		fail(writer, 400, "invalid_profile", "昵称需为 2–24 字，简介不超过 100 字")
		return
	}
	if _, err := app.db.Exec("UPDATE users SET name=?,bio=? WHERE id=?", strings.TrimSpace(input.Name), strings.TrimSpace(input.Bio), who.User.ID); err != nil {
		fail(writer, 500, "database_error", "保存失败")
		return
	}
	account, err := scanUser(app.db.QueryRow("SELECT "+userColumns+" FROM users WHERE id=?", who.User.ID))
	if err != nil {
		fail(writer, 500, "database_error", "读取失败")
		return
	}
	respond(writer, 200, map[string]any{"user": account})
}
func (app *server) setupTOTP(writer http.ResponseWriter, request *http.Request, who identity) {
	if app.limited(writer, "factor:"+who.User.ID, 10, 10*time.Minute) {
		return
	}
	var input struct {
		Password string `json:"password"`
	}
	if !readJSON(writer, request, &input) {
		return
	}
	app.accountMu.Lock()
	defer app.accountMu.Unlock()
	account, ok := app.activeAccount(writer, who)
	if !ok {
		return
	}
	who.User = account
	if !verifyPassword(input.Password, who.User.Password) {
		fail(writer, 401, "invalid_password", "密码不正确")
		return
	}
	if who.User.TwoFactor {
		fail(writer, 409, "already_enabled", "两步验证已开启")
		return
	}
	key, err := totp.Generate(totp.GenerateOpts{Issuer: "Kukie", AccountName: who.User.Email})
	if err != nil {
		fail(writer, 500, "totp_error", "无法生成验证密钥")
		return
	}
	sealed, err := app.seal([]byte(key.Secret()))
	if err != nil {
		fail(writer, 500, "totp_error", "无法保存密钥")
		return
	}
	_, err = app.db.Exec("UPDATE users SET totp_pending=?,totp_pending_until=? WHERE id=? AND totp_secret=''", sealed, time.Now().Add(10*time.Minute).Unix(), who.User.ID)
	if err != nil {
		fail(writer, 500, "database_error", "无法开始设置")
		return
	}
	respond(writer, 200, map[string]string{"secret": key.Secret(), "uri": key.URL()})
}
func validCounter(secret, code string, now time.Time, last int64) int64 {
	for _, offset := range []int64{0, -1, 1} {
		counter := now.Unix()/30 + offset
		if counter <= last {
			continue
		}
		expected, err := totp.GenerateCode(secret, time.Unix(counter*30, 0))
		if err == nil && subtle.ConstantTimeCompare([]byte(code), []byte(expected)) == 1 {
			return counter
		}
	}
	return 0
}
func (app *server) consumeFactor(account user, code string) bool {
	secret, err := app.unseal(account.Secret)
	if err != nil {
		return false
	}
	counter := validCounter(string(secret), strings.TrimSpace(code), time.Now(), account.LastCounter)
	if counter > 0 {
		result, err := app.db.Exec("UPDATE users SET totp_last=? WHERE id=? AND totp_last<? AND totp_secret=?", counter, account.ID, counter, account.Secret)
		if err != nil {
			return false
		}
		count, _ := result.RowsAffected()
		return count == 1
	}
	result, err := app.db.Exec("DELETE FROM recovery_codes WHERE user_id=? AND hash=?", account.ID, app.keyed("recovery:"+strings.ToUpper(strings.TrimSpace(code))))
	if err != nil {
		return false
	}
	count, _ := result.RowsAffected()
	return count == 1
}
func (app *server) enableTOTP(writer http.ResponseWriter, request *http.Request, who identity) {
	if app.limited(writer, "factor:"+who.User.ID, 10, 10*time.Minute) {
		return
	}
	var input struct {
		Code string `json:"code"`
	}
	if !readJSON(writer, request, &input) {
		return
	}
	app.accountMu.Lock()
	defer app.accountMu.Unlock()
	if _, ok := app.activeAccount(writer, who); !ok {
		return
	}
	tx, err := app.db.Begin()
	if err != nil {
		fail(writer, 500, "database_error", "无法开启")
		return
	}
	defer tx.Rollback()
	var pending string
	var expiry int64
	err = tx.QueryRow("SELECT totp_pending,totp_pending_until FROM users WHERE id=? AND totp_secret=''", who.User.ID).Scan(&pending, &expiry)
	secret, openErr := app.unseal(pending)
	if err != nil || openErr != nil || expiry <= time.Now().Unix() {
		fail(writer, 400, "setup_expired", "请重新开始两步验证设置")
		return
	}
	counter := validCounter(string(secret), input.Code, time.Now(), 0)
	if counter == 0 {
		fail(writer, 400, "invalid_otp", "验证码不正确")
		return
	}
	codes := make([]string, 8)
	for index := range codes {
		value := strings.ToUpper(hex.EncodeToString(randomBytes(8)))
		codes[index] = value[:8] + "-" + value[8:]
		if _, err = tx.Exec("INSERT INTO recovery_codes(user_id,hash) VALUES(?,?)", who.User.ID, app.keyed("recovery:"+codes[index])); err != nil {
			fail(writer, 500, "database_error", "恢复码生成失败")
			return
		}
	}
	if _, err = tx.Exec("UPDATE users SET totp_secret=totp_pending,totp_pending='',totp_pending_until=0,totp_last=? WHERE id=?", counter, who.User.ID); err != nil {
		fail(writer, 500, "database_error", "无法开启")
		return
	}
	if err = revokeOtherLogins(tx, who.User.ID, who.Token); err != nil {
		fail(writer, 500, "database_error", "无法更新会话")
		return
	}
	if err = tx.Commit(); err != nil {
		fail(writer, 500, "database_error", "无法开启")
		return
	}
	respond(writer, 200, map[string]any{"enabled": true, "recovery_codes": codes})
}
func (app *server) disableTOTP(writer http.ResponseWriter, request *http.Request, who identity) {
	if app.limited(writer, "factor:"+who.User.ID, 10, 10*time.Minute) {
		return
	}
	var input struct {
		Password string `json:"password"`
		Code     string `json:"code"`
	}
	if !readJSON(writer, request, &input) {
		return
	}
	app.accountMu.Lock()
	defer app.accountMu.Unlock()
	account, ok := app.activeAccount(writer, who)
	if !ok {
		return
	}
	who.User = account
	if !who.User.TwoFactor || !verifyPassword(input.Password, who.User.Password) || !app.consumeFactor(who.User, input.Code) {
		fail(writer, 401, "invalid_factor", "密码或两步验证码不正确")
		return
	}
	tx, err := app.db.Begin()
	if err != nil {
		fail(writer, 500, "database_error", "无法关闭")
		return
	}
	defer tx.Rollback()
	if _, err = tx.Exec("UPDATE users SET totp_secret='',totp_pending='',totp_pending_until=0,totp_last=0 WHERE id=?", who.User.ID); err != nil {
		fail(writer, 500, "database_error", "无法关闭")
		return
	}
	if _, err = tx.Exec("DELETE FROM recovery_codes WHERE user_id=?", who.User.ID); err != nil {
		fail(writer, 500, "database_error", "无法关闭")
		return
	}
	if err = revokeOtherLogins(tx, who.User.ID, who.Token); err != nil {
		fail(writer, 500, "database_error", "无法更新会话")
		return
	}
	if err = tx.Commit(); err != nil {
		fail(writer, 500, "database_error", "无法关闭")
		return
	}
	respond(writer, 200, map[string]bool{"enabled": false})
}
func (app *server) audit(who identity, action, target string) {
	_, _ = app.db.Exec("INSERT INTO audit(user_id,action,target,created_at) VALUES(?,?,?,?)", who.User.ID, action, target, time.Now().UTC().Format(time.RFC3339))
}

func databaseFailure(writer http.ResponseWriter, err error) bool {
	if err == nil {
		return false
	}
	fail(writer, 500, "database_error", "操作未完成，请稍后重试")
	return true
}
func notFound(writer http.ResponseWriter) {
	fail(writer, 404, "not_found", "内容不存在或已下架")
}
func changed(result sql.Result) bool {
	count, err := result.RowsAffected()
	return err == nil && count == 1
}
func requiredError(field string) error { return fmt.Errorf("请填写有效的%s", field) }
