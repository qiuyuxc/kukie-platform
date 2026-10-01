package main

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestSharedPosts(test *testing.T) {
	fixture := newFixture(test)
	for index := range 53 {
		status := "published"
		if index == 52 {
			status = "draft"
		}
		_, err := fixture.app.db.Exec("INSERT INTO posts(id,slug,title,markdown,category,tags,date,status,updated_at) VALUES(?,?,?,?,?,?,?,?,?)", fmt.Sprint(index), fmt.Sprintf("nested/post-%d", index), "标题", "正文独有词", "技术", `["中文","Go"]`, "2026-09-30", status, "2026-09-30")
		if err != nil {
			test.Fatal(err)
		}
	}
	result := call(test, fixture, "GET", "/api/v1/posts", "", nil, 200)
	if len(result["posts"].([]any)) != 50 || result["total"] != float64(52) || result["has_more"] != true {
		test.Fatal(result)
	}
	page, err := fixture.app.readPosts(postFilter{Page: 2, Limit: 10, Tag: "中文", SearchBody: true, Query: "正文独有词"})
	if err != nil || len(page.Posts) != 10 || page.Total != 52 || page.Posts[0].Markdown != "" {
		test.Fatalf("%+v %v", page, err)
	}
	page, err = fixture.app.readPosts(postFilter{Limit: 50, Query: "正文独有词"})
	if err != nil || page.Total != 0 {
		test.Fatalf("App search semantics changed: %+v %v", page, err)
	}
	call(test, fixture, "GET", "/api/v1/posts/by-slug?slug=nested/post-52", "", nil, 404)
	detail := call(test, fixture, "GET", "/api/v1/posts/by-slug?slug=nested/post-0", "", nil, 200)
	if detail["post"].(map[string]any)["html"] == "" {
		test.Fatal("missing body")
	}
	for _, invalid := range []string{"../x", "a//b", "a/..", "/x", "x/", "a%2fb", "x?y", "x\\y"} {
		if validPostSlug(invalid) {
			test.Fatal(invalid)
		}
	}
	if !validPostSlug("中文/nested-post_1") {
		test.Fatal("nested slug rejected")
	}
}

func TestHugoImportOnce(test *testing.T) {
	fixture := newFixture(test)
	folder := filepath.Join(fixture.app.repository, "content", "posts")
	if err := os.MkdirAll(folder, 0700); err != nil {
		test.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(folder, "old.md"), []byte("---\ntitle: Original\ndate: 2026-09-30\n---\nOld body"), 0600); err != nil {
		test.Fatal(err)
	}
	if err := fixture.app.importPosts(); err != nil {
		test.Fatal(err)
	}
	if _, err := fixture.app.db.Exec("UPDATE posts SET title='Edited',status='deleted' WHERE slug='old'"); err != nil {
		test.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(folder, "new.md"), []byte("---\ntitle: New\n---\nNew body"), 0600); err != nil {
		test.Fatal(err)
	}
	if err := fixture.app.db.Close(); err != nil {
		test.Fatal(err)
	}
	reopened, err := openServer(fixture.app.directory, fixture.app.repository)
	if err != nil {
		test.Fatal(err)
	}
	defer reopened.db.Close()
	fixture.app = reopened
	if err := fixture.app.importPosts(); err != nil {
		test.Fatal(err)
	}
	var title, status string
	var count int
	if err := fixture.app.db.QueryRow("SELECT title,status FROM posts WHERE slug='old'").Scan(&title, &status); err != nil {
		test.Fatal(err)
	}
	fixture.app.db.QueryRow("SELECT COUNT(*) FROM posts").Scan(&count)
	if title != "Edited" || status != "deleted" || count != 1 {
		test.Fatalf("import overwrote edits: %s %s %d", title, status, count)
	}
}
