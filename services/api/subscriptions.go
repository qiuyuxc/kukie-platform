package main

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"
)

type subscription struct {
	Enabled   bool   `json:"enabled"`
	Frequency string `json:"frequency"`
	Available bool   `json:"available"`
	LastSent  int64  `json:"last_sent"`
	LastError string `json:"last_error"`
}

func subscriptionInterval(frequency string) time.Duration {
	switch frequency {
	case "immediate":
		return time.Minute
	case "daily":
		return 24 * time.Hour
	case "weekly":
		return 7 * 24 * time.Hour
	default:
		return 0
	}
}

func (app *server) getSubscription(writer http.ResponseWriter, request *http.Request, who identity) {
	writer.Header().Set("Cache-Control", "no-store")
	result := subscription{Frequency: "daily"}
	err := app.db.QueryRow("SELECT enabled,frequency,last_sent,last_error FROM subscriptions WHERE user_id=?", who.User.ID).Scan(&result.Enabled, &result.Frequency, &result.LastSent, &result.LastError)
	if err != sql.ErrNoRows && databaseFailure(writer, err) {
		return
	}
	config, err := app.smtpConfig()
	if databaseFailure(writer, err) {
		return
	}
	result.Available = config.Enabled
	respond(writer, 200, result)
}

func (app *server) saveSubscription(writer http.ResponseWriter, request *http.Request, who identity) {
	var input struct {
		Enabled   bool   `json:"enabled"`
		Frequency string `json:"frequency"`
	}
	if !readJSON(writer, request, &input) {
		return
	}
	interval := subscriptionInterval(input.Frequency)
	if interval == 0 {
		fail(writer, 400, "invalid_frequency", "请选择及时、每日或每周提醒")
		return
	}
	app.accountMu.Lock()
	defer app.accountMu.Unlock()
	config, err := app.smtpConfig()
	if databaseFailure(writer, err) {
		return
	}
	if input.Enabled && !config.Enabled {
		fail(writer, 503, "email_unavailable", "邮件服务尚未开启，暂时不能订阅")
		return
	}
	next := time.Now().Add(interval).Unix()
	_, err = app.db.Exec(`INSERT INTO subscriptions(user_id,enabled,frequency,cursor,next_at) VALUES(?,?,?,(SELECT COALESCE(MAX(seq),0) FROM post_events),?)
		ON CONFLICT(user_id) DO UPDATE SET
		cursor=CASE WHEN subscriptions.enabled=0 AND excluded.enabled=1 THEN excluded.cursor ELSE subscriptions.cursor END,
		next_at=CASE WHEN subscriptions.enabled<>excluded.enabled OR subscriptions.frequency<>excluded.frequency THEN excluded.next_at ELSE subscriptions.next_at END,
		enabled=excluded.enabled,frequency=excluded.frequency,last_error='',revision=subscriptions.revision+1`, who.User.ID, input.Enabled, input.Frequency, next)
	if databaseFailure(writer, err) {
		return
	}
	app.getSubscription(writer, request, who)
}

func (app *server) startMaintenance(ctx context.Context) <-chan struct{} {
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(time.Minute)
		defer ticker.Stop()
		for {
			if ctx.Err() != nil {
				return
			}
			if err := app.processSubscriptions(ctx, time.Now()); err != nil && ctx.Err() == nil {
				log.Print("Article reminders pending retry")
			}
			if err := app.cleanupMedia(ctx, time.Now()); err != nil && ctx.Err() == nil {
				log.Print("Deleted account media pending cleanup retry")
			}
			if err := app.processIndexNow(ctx, time.Now()); err != nil && ctx.Err() == nil {
				log.Print("IndexNow notifications pending retry")
			}
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
	return done
}

func (app *server) processSubscriptions(ctx context.Context, now time.Time) error {
	rows, err := app.db.Query("SELECT user_id FROM subscriptions WHERE enabled=1 AND next_at<=? ORDER BY next_at,user_id LIMIT 25", now.Unix())
	if err != nil {
		return err
	}
	var users []string
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			break
		}
		users = append(users, id)
	}
	rowError := rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	if rowError != nil {
		return rowError
	}
	var failure error
	for _, id := range users {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err = app.sendSubscription(ctx, id, now); err != nil {
			failure = err
		}
	}
	return failure
}

func (app *server) sendSubscription(ctx context.Context, id string, now time.Time) error {
	app.accountMu.Lock()
	defer app.accountMu.Unlock()
	var email, frequency string
	var cursor int64
	err := app.db.QueryRow("SELECT u.email,s.frequency,s.cursor FROM subscriptions s JOIN users u ON u.id=s.user_id WHERE s.user_id=? AND s.enabled=1 AND s.next_at<=?", id, now.Unix()).Scan(&email, &frequency, &cursor)
	if err == sql.ErrNoRows {
		return nil
	}
	if err != nil {
		return err
	}
	config, err := app.smtpConfig()
	if err != nil || !config.Enabled {
		return err
	}
	rows, err := app.db.Query("SELECT e.seq,p.id,p.title,p.description,p.status FROM post_events e JOIN posts p ON p.id=e.post_id WHERE e.seq>? ORDER BY e.seq LIMIT 100", cursor)
	if err != nil {
		return err
	}
	var updates []string
	seen := map[string]bool{}
	for rows.Next() {
		var postID, title, description, status string
		if err = rows.Scan(&cursor, &postID, &title, &description, &status); err != nil {
			break
		}
		if status == "published" && !seen[postID] {
			seen[postID] = true
			updates = append(updates, title+"\r\n"+description)
		}
	}
	rowError := rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	if rowError != nil {
		return rowError
	}
	if len(updates) == 0 {
		_, err = app.db.Exec("UPDATE subscriptions SET cursor=?,next_at=?,last_error='' WHERE user_id=?", cursor, now.Add(subscriptionInterval(frequency)).Unix(), id)
		return err
	}
	sendContext, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	body := "你订阅的小站有文章更新：\r\n\r\n" + strings.Join(updates, "\r\n\r\n") + "\r\n\r\n打开 Kukie App 查看完整文章。\r\n你可以在「我的 → 设置 → 文章更新」调整频率或关闭订阅。"
	if err = app.sendMail(sendContext, config, email, fmt.Sprintf("Kukie · %d 篇文章更新", len(updates)), body); err != nil {
		_, updateError := app.db.Exec("UPDATE subscriptions SET next_at=?,last_error='邮件发送失败，将稍后重试' WHERE user_id=?", now.Add(15*time.Minute).Unix(), id)
		if updateError != nil {
			return updateError
		}
		return err
	}
	_, err = app.db.Exec("UPDATE subscriptions SET cursor=?,next_at=?,last_sent=?,last_error='' WHERE user_id=?", cursor, now.Add(subscriptionInterval(frequency)).Unix(), now.Unix(), id)
	return err
}
