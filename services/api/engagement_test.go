package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func publishedPost(test *testing.T, fixture fixture) string {
	test.Helper()
	result := call(test, fixture, "POST", "/api/v1/admin/posts", fixture.adminToken, map[string]any{
		"title": "互动测试", "markdown": "正文", "category": "随笔", "tags": []string{}, "date": "2026-10-01", "status": "published",
	}, 200)
	return result["id"].(string)
}

func TestArticleEngagement(test *testing.T) {
	fixture := newFixture(test)
	id := publishedPost(test, fixture)
	reader := createReader(test, fixture, "engagement@example.test")
	other := createReader(test, fixture, "another@example.test")
	path := "/api/v1/posts/" + id
	assertCount := func(result map[string]any, key string, expected float64) {
		test.Helper()
		if result[key] != expected {
			test.Fatalf("%s: expected %v, got %v", key, expected, result[key])
		}
	}
	test.Run("reads do not increment and guest views deduplicate", func(test *testing.T) {
		assertCount(call(test, fixture, "GET", path+"/engagement", "", nil, 200), "views", 0)
		call(test, fixture, "POST", path+"/views", "", map[string]string{"reader_id": "short"}, 400)
		for attempt := 0; attempt < 2; attempt++ {
			assertCount(call(test, fixture, "POST", path+"/views", "", map[string]string{"reader_id": "guest-installation-0001"}, 200), "views", 1)
		}
		assertCount(call(test, fixture, "POST", path+"/views", "", map[string]string{"reader_id": "guest-installation-0002"}, 200), "views", 2)
		if _, err := fixture.app.db.Exec("UPDATE post_views SET day='2026-01-01' WHERE visitor=?", fixture.app.keyed("view:guest:guest-installation-0001")); err != nil {
			test.Fatal(err)
		}
		assertCount(call(test, fixture, "POST", path+"/views", "", map[string]string{"reader_id": "guest-installation-0001"}, 200), "views", 3)
	})
	test.Run("logged in views deduplicate across installations", func(test *testing.T) {
		for _, installation := range []string{"first-installation", "second-installation"} {
			assertCount(call(test, fixture, "POST", path+"/views", reader, map[string]string{"reader_id": installation}, 200), "views", 4)
		}
	})
	test.Run("likes are authenticated idempotent and isolated", func(test *testing.T) {
		call(test, fixture, "PUT", "/api/v1/me/likes/"+id, "", nil, 401)
		for attempt := 0; attempt < 2; attempt++ {
			result := call(test, fixture, "PUT", "/api/v1/me/likes/"+id, reader, nil, 200)
			assertCount(result, "likes", 1)
			if result["liked"] != true {
				test.Fatal("own like not returned")
			}
		}
		for _, token := range []string{"", other} {
			result := call(test, fixture, "GET", path+"/engagement", token, nil, 200)
			assertCount(result, "likes", 1)
			if result["liked"] != false {
				test.Fatal("another reader's like leaked")
			}
		}
		assertCount(call(test, fixture, "PUT", "/api/v1/me/likes/"+id, other, nil, 200), "likes", 2)
		for attempt := 0; attempt < 2; attempt++ {
			assertCount(call(test, fixture, "DELETE", "/api/v1/me/likes/"+id, reader, nil, 200), "likes", 1)
		}
	})
	test.Run("bookmark totals reflect real accounts", func(test *testing.T) {
		for attempt := 0; attempt < 2; attempt++ {
			call(test, fixture, "PUT", "/api/v1/me/bookmarks/"+id, reader, nil, 200)
		}
		result := call(test, fixture, "GET", path+"/engagement", reader, nil, 200)
		assertCount(result, "bookmarks", 1)
		if result["bookmarked"] != true {
			test.Fatal("own bookmark not returned")
		}
		result = call(test, fixture, "GET", path+"/engagement", other, nil, 200)
		if result["bookmarked"] != false {
			test.Fatal("bookmark state leaked")
		}
		call(test, fixture, "DELETE", "/api/v1/me/bookmarks/"+id, reader, nil, 200)
		assertCount(call(test, fixture, "GET", path+"/engagement", reader, nil, 200), "bookmarks", 0)
	})
	test.Run("cookie mutations require CSRF", func(test *testing.T) {
		for _, mutation := range []struct{ method, path, body string }{
			{"PUT", "/api/v1/me/likes/" + id, "{}"},
			{"POST", path + "/views", `{"reader_id":"guest-installation-0003"}`},
		} {
			request := httptest.NewRequest(mutation.method, mutation.path, strings.NewReader(mutation.body))
			request.AddCookie(&http.Cookie{Name: "kukie_session", Value: reader})
			request.Header.Set("Origin", "http://localhost:8085")
			response := httptest.NewRecorder()
			fixture.handler.ServeHTTP(response, request)
			if response.Code != 403 {
				test.Fatalf("cookie mutation accepted: %d", response.Code)
			}
		}
	})
	test.Run("schema reruns preserve engagement", func(test *testing.T) {
		if _, err := fixture.app.db.Exec(schema); err != nil {
			test.Fatal(err)
		}
		result := call(test, fixture, "GET", path+"/engagement", other, nil, 200)
		assertCount(result, "views", 4)
		assertCount(result, "likes", 1)
	})
	test.Run("draft deleted and missing posts stay private", func(test *testing.T) {
		for _, status := range []string{"draft", "deleted"} {
			if _, err := fixture.app.db.Exec("UPDATE posts SET status=? WHERE id=?", status, id); err != nil {
				test.Fatal(err)
			}
			call(test, fixture, "GET", path+"/engagement", reader, nil, 404)
			call(test, fixture, "POST", path+"/views", reader, map[string]string{}, 404)
			call(test, fixture, "PUT", "/api/v1/me/likes/"+id, reader, nil, 404)
		}
		call(test, fixture, "GET", "/api/v1/posts/missing/engagement", "", nil, 404)
		var total int
		if err := fixture.app.db.QueryRow("SELECT COUNT(*) FROM post_views").Scan(&total); err != nil || total != 4 {
			test.Fatalf("private view recorded: %d, %v", total, err)
		}
	})
}

func TestNoticeDetailAndRevisions(test *testing.T) {
	fixture := newFixture(test)
	text := strings.Repeat("长公告不需要压缩内容。\n", 300)
	input := map[string]any{"title": "一封长信", "text": text, "status": "draft"}
	id := call(test, fixture, "POST", "/api/v1/admin/notices", fixture.adminToken, input, 200)["id"].(string)
	path := "/api/v1/notices/" + id
	call(test, fixture, "GET", path, "", nil, 404)
	input["status"] = "published"
	input["revision"] = 1
	call(test, fixture, "PUT", "/api/v1/admin/notices/"+id, fixture.adminToken, input, 200)
	result := call(test, fixture, "GET", path, "", nil, 200)["notice"].(map[string]any)
	if result["text"] != text || result["revision"] != float64(2) {
		test.Fatal("full notice or read-state revision missing")
	}
	input["text"] = text + "补充。"
	input["revision"] = 2
	call(test, fixture, "PUT", "/api/v1/admin/notices/"+id, fixture.adminToken, input, 200)
	result = call(test, fixture, "GET", path, "", nil, 200)["notice"].(map[string]any)
	if result["revision"] != float64(3) {
		test.Fatal("edited notice must become unread again")
	}
	call(test, fixture, "DELETE", "/api/v1/admin/notices/"+id, fixture.adminToken, nil, 200)
	call(test, fixture, "GET", path, "", nil, 404)
}

func TestCommentAuthorLabels(test *testing.T) {
	fixture := newFixture(test)
	id := publishedPost(test, fixture)
	reader := createReader(test, fixture, "labels@example.test")
	path := "/api/v1/posts/" + id + "/comments"
	adminComment := call(test, fixture, "POST", path, fixture.adminToken, map[string]string{"text": "博主的回复"}, 201)["id"]
	call(test, fixture, "POST", path, reader, map[string]string{"text": "读者的留言"}, 201)
	call(test, fixture, "POST", path, reader, map[string]string{"text": "伪造称号", "author_label": "博主"}, 400)
	for _, endpoint := range []string{path, "/api/v1/admin/comments"} {
		result := call(test, fixture, "GET", endpoint, fixture.adminToken, nil, 200)
		for _, entry := range result["comments"].([]any) {
			item := entry.(map[string]any)
			if item["id"] == adminComment {
				if item["author_label"] != "博主" {
					test.Fatal("blogger label missing")
				}
			} else if item["author_label"] != nil {
				test.Fatal("ordinary readers should have no title")
			}
		}
	}
}
