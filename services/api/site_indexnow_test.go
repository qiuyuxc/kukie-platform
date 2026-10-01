package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestIndexNowQueue(test *testing.T) {
	fixture := siteFixture(test)
	ctx := context.Background()
	now := time.Now()
	var count int
	if err := fixture.app.processIndexNow(ctx, now); err != nil {
		test.Fatal(err)
	}
	fixture.app.db.QueryRow("SELECT COUNT(*) FROM indexnow_queue").Scan(&count)
	if count != 1 {
		test.Fatal("disabled IndexNow lost pending events")
	}
	failures := true
	mutate := false
	requests := 0
	receiver := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requests++
		var input struct {
			Host, Key, KeyLocation string
			URLList                []string
		}
		if err := json.NewDecoder(request.Body).Decode(&input); err != nil {
			test.Error(err)
		}
		if input.Host != "blog.example.test" || input.Key != "test-key-123" || len(input.URLList) != 1 || !strings.HasPrefix(input.URLList[0], "https://blog.example.test/posts/") {
			test.Errorf("invalid payload: %+v", input)
		}
		if mutate {
			if _, err := fixture.app.db.Exec("UPDATE posts SET title='changed during send',revision=revision+1 WHERE id='site-post'"); err != nil {
				test.Error(err)
			}
		}
		if failures {
			writer.WriteHeader(429)
		} else {
			writer.WriteHeader(202)
		}
	}))
	defer receiver.Close()
	fixture.app.site.indexNowKey, fixture.app.site.indexNowEndpoint = "test-key-123", receiver.URL
	if err := fixture.app.processIndexNow(ctx, now); err == nil {
		test.Fatal("failed notification not retained")
	}
	var attempts int
	var next int64
	fixture.app.db.QueryRow("SELECT attempts,next_at FROM indexnow_queue").Scan(&attempts, &next)
	if attempts != 1 || next <= now.Unix() {
		test.Fatal("missing retry backoff")
	}
	if err := fixture.app.processIndexNow(ctx, now); err != nil || requests != 1 {
		test.Fatal("retried before deadline", err, requests)
	}
	failures, mutate = false, true
	if err := fixture.app.processIndexNow(ctx, now.Add(2*time.Minute)); err != nil {
		test.Fatal(err)
	}
	fixture.app.db.QueryRow("SELECT COUNT(*) FROM indexnow_queue").Scan(&count)
	if count != 1 {
		test.Fatal("concurrent edit was acknowledged incorrectly")
	}
	mutate = false
	if err := fixture.app.processIndexNow(ctx, now.Add(3*time.Minute)); err != nil {
		test.Fatal(err)
	}
	fixture.app.db.QueryRow("SELECT COUNT(*) FROM indexnow_queue").Scan(&count)
	if count != 0 {
		test.Fatal("successful notification not acknowledged")
	}
	if _, err := fixture.app.db.Exec("UPDATE posts SET status='draft',revision=revision+1 WHERE id='site-post'"); err != nil {
		test.Fatal(err)
	}
	fixture.app.db.QueryRow("SELECT COUNT(*) FROM indexnow_queue").Scan(&count)
	if count != 1 {
		test.Fatal("withdrawal not queued")
	}
	siteCall(test, fixture, "/test-key-123.txt", 200)
}

func TestIndexNowDeleteAndDraft(test *testing.T) {
	fixture := siteFixture(test)
	if _, err := fixture.app.db.Exec("DELETE FROM indexnow_queue; INSERT INTO notices(id,title,text,status,updated_at) VALUES('new','test','body','draft','2026-10-01'); UPDATE notices SET text='draft edit' WHERE id='new'"); err != nil {
		test.Fatal(err)
	}
	var count int
	fixture.app.db.QueryRow("SELECT COUNT(*) FROM indexnow_queue").Scan(&count)
	if count != 0 {
		test.Fatal("draft was submitted")
	}
	if _, err := fixture.app.db.Exec("UPDATE notices SET status='published' WHERE id='new'; DELETE FROM notices WHERE id='new'; UPDATE posts SET status='deleted' WHERE id='site-post'"); err != nil {
		test.Fatal(err)
	}
	fixture.app.db.QueryRow("SELECT COUNT(*) FROM indexnow_queue").Scan(&count)
	if count != 2 {
		test.Fatal("delete notifications missing", count)
	}
}
