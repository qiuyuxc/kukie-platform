package main

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"database/sql"
	"fmt"
	"math/big"
	"net/http"
	"strings"
	"time"
)

func (app *server) accountProof(writer http.ResponseWriter, who identity, password string) (user, bool) {
	if app.limited(writer, "account-proof:"+who.User.ID, 12, 15*time.Minute) {
		return user{}, false
	}
	account, ok := app.activeAccount(writer, who)
	if !ok {
		return user{}, false
	}
	if !verifyPassword(password, account.Password) {
		fail(writer, 401, "invalid_password", "当前密码不正确")
		return user{}, false
	}
	return account, true
}

func (app *server) activeAccount(writer http.ResponseWriter, who identity) (user, bool) {
	account, err := scanUser(app.db.QueryRow("SELECT "+userColumns+" FROM users WHERE id=? AND EXISTS(SELECT 1 FROM sessions WHERE user_id=users.id AND hash=? AND expires>?)", who.User.ID, digest(who.Token), time.Now().Unix()))
	if err != nil {
		fail(writer, 401, "session_expired", "登录已过期，请重新登录")
		return user{}, false
	}
	return account, true
}

func (app *server) accountFactor(writer http.ResponseWriter, account user, code string) bool {
	if !account.TwoFactor {
		return true
	}
	if strings.TrimSpace(code) == "" {
		fail(writer, 401, "totp_required", "请输入两步验证码或一次性恢复码")
		return false
	}
	if !app.consumeFactor(account, code) {
		fail(writer, 401, "invalid_otp", "两步验证码无效、已使用或过期")
		return false
	}
	return true
}

func (app *server) changePassword(writer http.ResponseWriter, request *http.Request, who identity) {
	var input struct {
		Password    string `json:"password"`
		NewPassword string `json:"new_password"`
		OTP         string `json:"otp"`
	}
	if !readJSON(writer, request, &input) {
		return
	}
	if !validPassword(input.NewPassword) || input.Password == input.NewPassword {
		fail(writer, 400, "invalid_password", "新密码至少 12 个字符、最多 128 字节，且不能与旧密码相同")
		return
	}
	app.accountMu.Lock()
	defer app.accountMu.Unlock()
	account, ok := app.accountProof(writer, who, input.Password)
	if !ok || !app.accountFactor(writer, account, input.OTP) {
		return
	}
	password := hashPassword(input.NewPassword)
	tx, err := app.db.Begin()
	if databaseFailure(writer, err) {
		return
	}
	defer tx.Rollback()
	_, err = tx.Exec("UPDATE users SET password=?,totp_pending='',totp_pending_until=0 WHERE id=?", password, account.ID)
	if databaseFailure(writer, err) {
		return
	}
	err = revokeOtherLogins(tx, account.ID, who.Token)
	if databaseFailure(writer, err) {
		return
	}
	_, err = tx.Exec("DELETE FROM email_changes WHERE user_id=?", account.ID)
	if databaseFailure(writer, err) || databaseFailure(writer, tx.Commit()) {
		return
	}
	app.audit(who, "account.password", account.ID)
	respond(writer, 200, map[string]string{"message": "密码已更新，其他设备已退出登录"})
}

func (app *server) emailChangeCode(writer http.ResponseWriter, request *http.Request, who identity) {
	var input struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if !readJSON(writer, request, &input) {
		return
	}
	email := normalizeEmail(input.Email)
	if email == "" {
		fail(writer, 400, "invalid_email", "请填写有效的新邮箱")
		return
	}
	app.accountMu.Lock()
	defer app.accountMu.Unlock()
	account, ok := app.accountProof(writer, who, input.Password)
	if !ok {
		return
	}
	if email == account.Email {
		fail(writer, 400, "unchanged_email", "新邮箱与当前邮箱相同")
		return
	}
	if app.limited(writer, "email-change:"+account.ID, 1, time.Minute) || app.limited(writer, "email-change-ip:"+clientIP(request), 10, time.Hour) {
		return
	}
	config, err := app.smtpConfig()
	if databaseFailure(writer, err) {
		return
	}
	if !config.Enabled {
		fail(writer, 503, "email_unavailable", "邮件服务未开启，暂时不能换绑邮箱")
		return
	}
	var exists bool
	if databaseFailure(writer, app.db.QueryRow("SELECT EXISTS(SELECT 1 FROM users WHERE email=?)", email).Scan(&exists)) {
		return
	}
	if exists {
		fail(writer, 409, "email_exists", "此邮箱已被绑定，请使用其他邮箱")
		return
	}
	number, err := rand.Int(rand.Reader, big.NewInt(1000000))
	if databaseFailure(writer, err) {
		return
	}
	code := fmt.Sprintf("%06d", number.Int64())
	hash := app.keyed("email-change:" + account.ID + ":" + email + ":" + code)
	_, err = app.db.Exec("INSERT INTO email_changes(user_id,email,hash,expires,attempts) VALUES(?,?,?,?,0) ON CONFLICT(user_id) DO UPDATE SET email=excluded.email,hash=excluded.hash,expires=excluded.expires,attempts=0", account.ID, email, hash, time.Now().Add(10*time.Minute).Unix())
	if databaseFailure(writer, err) {
		return
	}
	ctx, cancel := context.WithTimeout(request.Context(), 20*time.Second)
	defer cancel()
	if app.sendMail(ctx, config, email, "Kukie 邮箱换绑验证码", "你的邮箱换绑验证码是："+code+"\r\n\r\n10 分钟内有效，仅用于当前账号的邮箱换绑。如果不是你发起的操作，请忽略此邮件。") != nil {
		_, _ = app.db.Exec("DELETE FROM email_changes WHERE user_id=? AND hash=?", account.ID, hash)
		fail(writer, 502, "email_failed", "验证码发送失败，请稍后重试")
		return
	}
	respond(writer, 200, map[string]string{"message": "验证码已发送到新邮箱，10 分钟内有效"})
}

func (app *server) changeEmail(writer http.ResponseWriter, request *http.Request, who identity) {
	var input struct {
		Email    string `json:"email"`
		Password string `json:"password"`
		Code     string `json:"code"`
		OTP      string `json:"otp"`
	}
	if !readJSON(writer, request, &input) {
		return
	}
	email := normalizeEmail(input.Email)
	if email == "" {
		fail(writer, 400, "invalid_email", "请填写有效的新邮箱")
		return
	}
	app.accountMu.Lock()
	defer app.accountMu.Unlock()
	account, ok := app.accountProof(writer, who, input.Password)
	if !ok {
		return
	}
	var expected string
	var expires int64
	var attempts int
	err := app.db.QueryRow("SELECT hash,expires,attempts FROM email_changes WHERE user_id=? AND email=?", account.ID, email).Scan(&expected, &expires, &attempts)
	if err == sql.ErrNoRows {
		fail(writer, 400, "code_expired", "验证码已过期或尝试过多，请重新获取")
		return
	}
	if databaseFailure(writer, err) {
		return
	}
	if expires <= time.Now().Unix() || attempts >= 5 {
		fail(writer, 400, "code_expired", "验证码已过期或尝试过多，请重新获取")
		return
	}
	actual := app.keyed("email-change:" + account.ID + ":" + email + ":" + strings.TrimSpace(input.Code))
	if subtle.ConstantTimeCompare([]byte(expected), []byte(actual)) != 1 {
		_, err = app.db.Exec("UPDATE email_changes SET attempts=attempts+1 WHERE user_id=?", account.ID)
		if !databaseFailure(writer, err) {
			fail(writer, 400, "invalid_code", "邮箱验证码不正确")
		}
		return
	}
	if !app.accountFactor(writer, account, input.OTP) {
		return
	}
	tx, err := app.db.Begin()
	if databaseFailure(writer, err) {
		return
	}
	defer tx.Rollback()
	var used bool
	if databaseFailure(writer, tx.QueryRow("SELECT EXISTS(SELECT 1 FROM users WHERE email=? AND id<>?)", email, account.ID).Scan(&used)) {
		return
	}
	if used {
		fail(writer, 409, "email_exists", "此邮箱已被绑定，请使用其他邮箱")
		return
	}
	_, err = tx.Exec("UPDATE users SET email=?,totp_pending='',totp_pending_until=0 WHERE id=?", email, account.ID)
	if databaseFailure(writer, err) {
		return
	}
	_, err = tx.Exec("DELETE FROM email_changes WHERE user_id=?", account.ID)
	if databaseFailure(writer, err) {
		return
	}
	err = revokeOtherLogins(tx, account.ID, who.Token)
	if databaseFailure(writer, err) || databaseFailure(writer, tx.Commit()) {
		return
	}
	app.audit(who, "account.email", account.ID)
	account.Email = email
	respond(writer, 200, map[string]any{"message": "邮箱已换绑，其他设备已退出登录", "user": account})
}

func (app *server) deleteAccount(writer http.ResponseWriter, request *http.Request, who identity) {
	var input struct {
		Password string `json:"password"`
		OTP      string `json:"otp"`
		Confirm  string `json:"confirm"`
	}
	if !readJSON(writer, request, &input) {
		return
	}
	if input.Confirm != "注销我的账号" {
		fail(writer, 400, "confirmation_required", "请输入「注销我的账号」确认此操作")
		return
	}
	app.accountMu.Lock()
	defer app.accountMu.Unlock()
	account, ok := app.accountProof(writer, who, input.Password)
	if !ok {
		return
	}
	if account.Role == "admin" {
		fail(writer, 403, "admin_protected", "博主账号受保护，不能在 App 内注销")
		return
	}
	if !app.accountFactor(writer, account, input.OTP) {
		return
	}
	tx, err := app.db.Begin()
	if databaseFailure(writer, err) {
		return
	}
	defer tx.Rollback()
	for _, statement := range []struct {
		query string
		args  []any
	}{
		{"INSERT INTO media_cleanup(id,path,config) SELECT id,path,config FROM media WHERE owner_id=?", []any{account.ID}},
		{"DELETE FROM media WHERE owner_id=?", []any{account.ID}},
		{"DELETE FROM comments WHERE user_id=?", []any{account.ID}},
		{"DELETE FROM post_views WHERE visitor=?", []any{app.keyed("view:user:" + account.ID)}},
		{"DELETE FROM audit WHERE user_id=?", []any{account.ID}},
		{"DELETE FROM email_codes WHERE email=?", []any{account.Email}},
		{"DELETE FROM users WHERE id=?", []any{account.ID}},
	} {
		if _, err = tx.Exec(statement.query, statement.args...); databaseFailure(writer, err) {
			return
		}
	}
	if databaseFailure(writer, tx.Commit()) {
		return
	}
	http.SetCookie(writer, &http.Cookie{Name: "kukie_session", Path: "/", MaxAge: -1, HttpOnly: true, Secure: app.secureCookie, SameSite: http.SameSiteStrictMode})
	respond(writer, 200, map[string]string{"message": "账号已注销，头像访问已撤销，存储副本将在后台清理"})
}
