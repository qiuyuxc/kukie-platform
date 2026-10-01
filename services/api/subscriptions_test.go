package main

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestSubscriptionDefaultsAndSMTPGate(test *testing.T) {
	fixture := newFixture(test)
	reader := createReader(test, fixture, "subscriptions@example.test")
	result := call(test, fixture, "GET", "/api/v1/me/subscription", reader, nil, 200)
	if result["enabled"] != false || result["frequency"] != "daily" {
		test.Fatal("subscription must default to off")
	}
	call(test, fixture, "PUT", "/api/v1/me/subscription", "", map[string]any{"enabled": true, "frequency": "daily"}, 401)
	call(test, fixture, "PUT", "/api/v1/me/subscription", reader, map[string]any{"enabled": true, "frequency": "invalid"}, 400)
	if err := fixture.app.storeSetting("smtp", smtpSettings{}); err != nil {
		test.Fatal(err)
	}
	call(test, fixture, "PUT", "/api/v1/me/subscription", reader, map[string]any{"enabled": true, "frequency": "daily"}, 503)
	result = call(test, fixture, "PUT", "/api/v1/me/subscription", reader, map[string]any{"enabled": false, "frequency": "weekly"}, 200)
	if result["available"] != false || result["enabled"] != false || result["frequency"] != "weekly" {
		test.Fatal("disabled subscription preferences were not saved")
	}
}

func TestSubscriptionFrequencyAndNoHistoricalMail(test *testing.T) {
	for _, frequency := range []string{"immediate", "daily", "weekly"} {
		test.Run(frequency, func(test *testing.T) {
			fixture := newFixture(test)
			reader := createReader(test, fixture, "timing@example.test")
			publishedPost(test, fixture)
			call(test, fixture, "PUT", "/api/v1/me/subscription", reader, map[string]any{"enabled": true, "frequency": frequency}, 200)
			var sent int
			var body string
			fixture.app.sendMail = func(ctx context.Context, config smtpSettings, to, subject, message string) error {
				if to != "timing@example.test" {
					test.Fatal("wrong recipient")
				}
				sent++
				body = message
				return nil
			}
			var due int64
			if err := fixture.app.db.QueryRow("SELECT next_at FROM subscriptions").Scan(&due); err != nil {
				test.Fatal(err)
			}
			if err := fixture.app.processSubscriptions(context.Background(), time.Unix(due, 0)); err != nil || sent != 0 {
				test.Fatalf("historical content was mailed: %d, %v", sent, err)
			}
			postID := publishedPost(test, fixture)
			input := map[string]any{"title": "更新后的标题", "description": "本次更新", "markdown": "新的正文", "category": "随笔", "date": "2026-10-01", "status": "published", "revision": 1}
			call(test, fixture, "PUT", "/api/v1/admin/posts/"+postID, fixture.adminToken, input, 200)
			if err := fixture.app.db.QueryRow("SELECT next_at FROM subscriptions").Scan(&due); err != nil {
				test.Fatal(err)
			}
			if err := fixture.app.processSubscriptions(context.Background(), time.Unix(due-1, 0)); err != nil || sent != 0 {
				test.Fatal("frequency window was ignored")
			}
			if err := fixture.app.processSubscriptions(context.Background(), time.Unix(due, 0)); err != nil || sent != 1 {
				test.Fatalf("due mail not delivered: %d, %v", sent, err)
			}
			if strings.Count(body, "更新后的标题") != 1 || !strings.Contains(body, "关闭订阅") {
				test.Fatal("article edits were not grouped or opt-out guidance missing")
			}
			if err := fixture.app.processSubscriptions(context.Background(), time.Unix(due+1, 0)); err != nil || sent != 1 {
				test.Fatal("repeated tick sent duplicate mail")
			}
			var next int64
			if err := fixture.app.db.QueryRow("SELECT next_at FROM subscriptions").Scan(&next); err != nil || next-due != int64(subscriptionInterval(frequency).Seconds()) {
				test.Fatal("next reminder time does not match the selected frequency")
			}
		})
	}
}

func TestSubscriptionRetryAndPreferenceChanges(test *testing.T) {
	fixture := newFixture(test)
	reader := createReader(test, fixture, "retry@example.test")
	call(test, fixture, "PUT", "/api/v1/me/subscription", reader, map[string]any{"enabled": true, "frequency": "weekly"}, 200)
	publishedPost(test, fixture)
	call(test, fixture, "PUT", "/api/v1/me/subscription", reader, map[string]any{"enabled": true, "frequency": "immediate"}, 200)
	var attempts int
	fixture.app.sendMail = func(ctx context.Context, config smtpSettings, to, subject, body string) error {
		attempts++
		if attempts == 1 {
			return errors.New("temporary SMTP error")
		}
		return nil
	}
	now := time.Now().Add(2 * time.Minute)
	if err := fixture.app.processSubscriptions(context.Background(), now); err == nil || attempts != 1 {
		test.Fatal("expected simulated delivery failure")
	}
	var cursor int64
	if err := fixture.app.db.QueryRow("SELECT cursor FROM subscriptions").Scan(&cursor); err != nil || cursor != 0 {
		test.Fatal("failed delivery advanced the event cursor")
	}
	result := call(test, fixture, "GET", "/api/v1/me/subscription", reader, nil, 200)
	if result["last_error"] == "" {
		test.Fatal("delivery failure was hidden")
	}
	if _, err := fixture.app.db.Exec(schema); err != nil {
		test.Fatal(err)
	}
	if err := fixture.app.processSubscriptions(context.Background(), now.Add(14*time.Minute)); err != nil || attempts != 1 {
		test.Fatal("retry backoff was ignored")
	}
	if err := fixture.app.processSubscriptions(context.Background(), now.Add(16*time.Minute)); err != nil || attempts != 2 {
		test.Fatal("pending mail did not survive schema reload and retry")
	}
	call(test, fixture, "PUT", "/api/v1/me/subscription", reader, map[string]any{"enabled": false, "frequency": "immediate"}, 200)
	publishedPost(test, fixture)
	if err := fixture.app.processSubscriptions(context.Background(), now.Add(time.Hour)); err != nil || attempts != 2 {
		test.Fatal("unsubscribed reader still received mail")
	}
	call(test, fixture, "PUT", "/api/v1/me/subscription", reader, map[string]any{"enabled": true, "frequency": "immediate"}, 200)
	if err := fixture.app.processSubscriptions(context.Background(), now.Add(2*time.Hour)); err != nil || attempts != 2 {
		test.Fatal("resubscribe replayed content from the disabled period")
	}
}

func TestSubscriptionUsesCurrentEmailAndSkipsHiddenPosts(test *testing.T) {
	fixture := newFixture(test)
	reader := createReader(test, fixture, "original@example.test")
	id := readerUserID(test, fixture, reader)
	call(test, fixture, "PUT", "/api/v1/me/subscription", reader, map[string]any{"enabled": true, "frequency": "immediate"}, 200)
	postID := publishedPost(test, fixture)
	call(test, fixture, "DELETE", "/api/v1/admin/posts/"+postID, fixture.adminToken, nil, 200)
	var recipients []string
	fixture.app.sendMail = func(ctx context.Context, config smtpSettings, to, subject, body string) error {
		recipients = append(recipients, to)
		return nil
	}
	now := time.Now().Add(2 * time.Minute)
	if err := fixture.app.processSubscriptions(context.Background(), now); err != nil || len(recipients) != 0 {
		test.Fatal("unpublished content was sent")
	}
	if _, err := fixture.app.db.Exec("UPDATE users SET email='new-address@example.test' WHERE id=?", id); err != nil {
		test.Fatal(err)
	}
	publishedPost(test, fixture)
	if err := fixture.app.processSubscriptions(context.Background(), now.Add(2*time.Minute)); err != nil {
		test.Fatal(err)
	}
	if len(recipients) != 1 || recipients[0] != "new-address@example.test" {
		test.Fatal("notification went to a stale email address")
	}
	call(test, fixture, "DELETE", "/api/v1/me", reader, map[string]string{"password": readerPassword, "confirm": "注销我的账号"}, 200)
	publishedPost(test, fixture)
	if err := fixture.app.processSubscriptions(context.Background(), now.Add(time.Hour)); err != nil || len(recipients) != 1 {
		test.Fatal("deleted account received notifications")
	}
}

func TestMediaCleanupRetainsFailedWork(test *testing.T) {
	fixture := newFixture(test)
	sealed, err := fixture.app.seal([]byte(`{"active":"local"}`))
	if err != nil {
		test.Fatal(err)
	}
	if _, err := fixture.app.db.Exec("INSERT INTO media_cleanup(id,path,config) VALUES('queued','../master.key',?)", sealed); err != nil {
		test.Fatal(err)
	}
	now := time.Now()
	if err := fixture.app.cleanupMedia(context.Background(), now); err == nil {
		test.Fatal("unsafe object path was accepted")
	}
	var next int64
	if err := fixture.app.db.QueryRow("SELECT next_at FROM media_cleanup WHERE id='queued'").Scan(&next); err != nil || next <= now.Unix() {
		test.Fatal("failed cleanup work was lost")
	}
	if _, err := fixture.app.db.Exec("UPDATE media_cleanup SET path='already-removed.jpg',next_at=0 WHERE id='queued'"); err != nil {
		test.Fatal(err)
	}
	if err := fixture.app.cleanupMedia(context.Background(), now); err != nil {
		test.Fatal(err)
	}
	var count int
	if err := fixture.app.db.QueryRow("SELECT COUNT(*) FROM media_cleanup").Scan(&count); err != nil || count != 0 {
		test.Fatal("successful cleanup was not removed from queue")
	}
}
