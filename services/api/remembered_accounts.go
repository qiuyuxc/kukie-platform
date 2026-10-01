package main

import (
	"database/sql"
	"errors"
	"net/http"
	"strings"
	"time"
)

func revokeOtherLogins(tx *sql.Tx, userID, currentToken string) error {
	if _, err := tx.Exec("DELETE FROM sessions WHERE user_id=? AND hash<>?", userID, digest(currentToken)); err != nil {
		return err
	}
	_, err := tx.Exec("DELETE FROM remembered_accounts WHERE user_id=?", userID)
	return err
}

func (app *server) quickLogin(writer http.ResponseWriter, request *http.Request) {
	if app.limited(writer, "quick-login-ip:"+clientIP(request), 30, 15*time.Minute) {
		return
	}
	var input struct {
		Token string `json:"remember_token"`
		OTP   string `json:"otp"`
	}
	if !readJSON(writer, request, &input) {
		return
	}
	if len(input.Token) != 43 {
		fail(writer, 401, "quick_login_expired", "快捷登录已失效，请使用密码登录")
		return
	}
	app.accountMu.Lock()
	defer app.accountMu.Unlock()
	var userID string
	var expires int64
	err := app.db.QueryRow("SELECT user_id,expires FROM remembered_accounts WHERE hash=? AND expires>?", digest(input.Token), time.Now().Unix()).Scan(&userID, &expires)
	if errors.Is(err, sql.ErrNoRows) {
		fail(writer, 401, "quick_login_expired", "快捷登录已失效，请使用密码登录")
		return
	}
	if databaseFailure(writer, err) {
		return
	}
	if app.limited(writer, "quick-login-user:"+userID, 15, 15*time.Minute) {
		return
	}
	account, err := scanUser(app.db.QueryRow("SELECT "+userColumns+" FROM users WHERE id=?", userID))
	if errors.Is(err, sql.ErrNoRows) {
		fail(writer, 401, "quick_login_expired", "快捷登录已失效，请使用密码登录")
		return
	}
	if databaseFailure(writer, err) {
		return
	}
	if !app.accountFactor(writer, account, strings.TrimSpace(input.OTP)) {
		return
	}
	tx, err := app.db.Begin()
	if databaseFailure(writer, err) {
		return
	}
	defer tx.Rollback()
	next, token := randomToken(), randomToken()
	changed, err := tx.Exec("UPDATE remembered_accounts SET hash=? WHERE hash=? AND expires>?", digest(next), digest(input.Token), time.Now().Unix())
	if databaseFailure(writer, err) {
		return
	}
	count, err := changed.RowsAffected()
	if databaseFailure(writer, err) {
		return
	}
	if count != 1 {
		fail(writer, 401, "quick_login_expired", "快捷登录已失效，请使用密码登录")
		return
	}
	_, err = tx.Exec("DELETE FROM sessions WHERE expires<=?", time.Now().Unix())
	if databaseFailure(writer, err) {
		return
	}
	_, err = tx.Exec("INSERT INTO sessions(hash,user_id,expires) VALUES(?,?,?)", digest(token), userID, time.Now().Add(7*24*time.Hour).Unix())
	if databaseFailure(writer, err) || databaseFailure(writer, tx.Commit()) {
		return
	}
	respond(writer, 200, map[string]any{"user": account, "token": token, "expires_in": 604800, "remember_token": next, "remember_expires_at": expires})
}

func (app *server) forgetAccount(writer http.ResponseWriter, request *http.Request) {
	if app.limited(writer, "forget-ip:"+clientIP(request), 30, 15*time.Minute) {
		return
	}
	var input struct {
		Token string `json:"remember_token"`
	}
	if !readJSON(writer, request, &input) {
		return
	}
	if len(input.Token) != 43 {
		fail(writer, 400, "invalid_token", "快捷登录凭据无效")
		return
	}
	app.accountMu.Lock()
	defer app.accountMu.Unlock()
	_, err := app.db.Exec("DELETE FROM remembered_accounts WHERE hash=?", digest(input.Token))
	if databaseFailure(writer, err) {
		return
	}
	respond(writer, 200, map[string]bool{"ok": true})
}
