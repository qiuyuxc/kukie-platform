package main

import (
	"encoding/json"
	"encoding/xml"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func siteFixture(test *testing.T) fixture {
	test.Helper()
	return siteFixtureAtOrigin(test, "https://blog.example.test")
}

func siteFixtureAtOrigin(test *testing.T, origin string) fixture {
	test.Helper()
	fixture := newFixture(test)
	directory := test.TempDir()
	if err := os.MkdirAll(filepath.Join(directory, "_server", "templates"), 0700); err != nil {
		test.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(directory, "public"), 0700); err != nil {
		test.Fatal(err)
	}
	files, err := filepath.Glob("../../apps/site/templates/*.html")
	if err != nil || len(files) == 0 {
		test.Fatal("missing templates", err)
	}
	for _, file := range files {
		data, err := os.ReadFile(file)
		if err != nil {
			test.Fatal(err)
		}
		if err = os.WriteFile(filepath.Join(directory, "_server", "templates", filepath.Base(file)), data, 0600); err != nil {
			test.Fatal(err)
		}
	}
	manifest := map[string]any{"version": 1, "title": "测试小站", "base_url": "https://blog.example.test/", "head": `<meta charset="utf-8"><title>STALE TITLE</title><meta name="description" content="STALE DESC"><link rel="canonical" href="https://old.invalid/"><script type="application/ld+json">{"url":"STALE SCHEMA"}</script>`, "post_head": `<meta charset="utf-8"><title>STALE POST</title>`, "assets": []string{"/safe.css", "/escape.css"}, "author": "测试作者", "default_cover": "/cover.png", "aliases": []map[string]string{{"path": "/legacy.html", "slug": "nested/中文"}}}
	manifest["navbar"] = `<nav><a href="https://blog.example.test/">首页</a></nav>`
	manifest["static_routes"] = []string{"/about/"}
	data, _ := json.Marshal(manifest)
	if err = os.WriteFile(filepath.Join(directory, "_server", "site.json"), data, 0600); err != nil {
		test.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(directory, "public", "safe.css"), []byte("body{}"), 0600); err != nil {
		test.Fatal(err)
	}
	if err = os.Mkdir(filepath.Join(directory, "public", "about"), 0700); err != nil {
		test.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(directory, "public", "about", "index.html"), []byte(`<!DOCTYPE html><link rel="canonical" href="https://blog.example.test/about/"><a href="https://blog.example.test/">首页</a><a href="https://blog.example.test.external.test/">外部链接</a>`), 0600); err != nil {
		test.Fatal(err)
	}
	if err = os.Symlink(filepath.Join(directory, "_server", "site.json"), filepath.Join(directory, "public", "escape.css")); err != nil {
		test.Fatal(err)
	}
	_, err = fixture.app.db.Exec("INSERT INTO posts(id,slug,title,description,markdown,category,tags,date,status,updated_at) VALUES('site-post','nested/中文','动态标题','动态摘要','## 中文标题\n\n新正文\n\n## 中文标题\n\n<script>alert(1)</script>','技术','[\"中文标签\"]','2026-09-30','published','2026-09-30T12:00:00Z')")
	if err != nil {
		test.Fatal(err)
	}
	if err = fixture.app.loadSite(directory, origin); err != nil {
		test.Fatal(err)
	}
	test.Cleanup(func() { fixture.app.site.root.Close() })
	return fixture
}

func siteCall(test *testing.T, fixture fixture, path string, status int) *httptest.ResponseRecorder {
	test.Helper()
	request := httptest.NewRequest("GET", path, nil)
	request.Host = "attacker.invalid"
	writer := httptest.NewRecorder()
	fixture.app.siteHandler().ServeHTTP(writer, request)
	if writer.Code != status {
		test.Fatalf("%s: got %d want %d: %.500s", path, writer.Code, status, writer.Body.String())
	}
	return writer
}

func TestSiteRenderingAndPublication(test *testing.T) {
	fixture := siteFixture(test)
	for _, path := range []string{"/", "/posts/", "/categories/?category=技术", "/tags/?tag=中文标签", "/archives/?month=2026-09", "/search/?q=新正文", "/posts/nested/中文/"} {
		response := siteCall(test, fixture, path, 200)
		body := response.Body.String()
		if !strings.Contains(body, "动态标题") || strings.Contains(body, "STALE") || strings.Contains(body, "attacker.invalid") || strings.Contains(body, "ZgotmplZ") {
			test.Fatalf("bad rendered page: %s", path)
		}
		if response.Header().Get("Cache-Control") != "no-store" {
			test.Fatal("dynamic page was cacheable")
		}
	}
	body := siteCall(test, fixture, "/posts/nested/中文/", 200).Body.String()
	body, err := url.PathUnescape(body)
	if err != nil {
		test.Fatal(err)
	}
	for _, text := range []string{`id="中文标题"`, `href="#中文标题"`, `id="中文标题-1"`, "新正文", "BlogPosting", "https://blog.example.test/posts/nested/"} {
		if !strings.Contains(body, text) {
			test.Fatalf("missing %s", text)
		}
	}
	if strings.Contains(body, "<script>alert(1)</script>") {
		test.Fatal("unsafe markdown")
	}
	redirect := siteCall(test, fixture, "/legacy.html", 308)
	if !strings.Contains(redirect.Header().Get("Location"), "/posts/nested/") {
		test.Fatal("invalid legacy redirect")
	}
	for _, path := range []string{"/index.xml", "/sitemap.xml"} {
		response := siteCall(test, fixture, path, 200)
		var document any
		if err := xml.Unmarshal(response.Body.Bytes(), &document); err != nil {
			test.Fatal(path, err)
		}
		if !strings.Contains(response.Body.String(), "/posts/nested/") {
			test.Fatal("post missing in index", path)
		}
	}
	for revision, status := range []string{"draft", "published", "deleted"} {
		_, err := fixture.app.db.Exec("UPDATE posts SET title=?,status=?,revision=revision+1 WHERE id='site-post'", fmt.Sprintf("edited-%d", revision), status)
		if err != nil {
			test.Fatal(err)
		}
		want := 404
		if status == "published" {
			want = 200
		}
		siteCall(test, fixture, "/posts/nested/中文/", want)
		for _, path := range []string{"/", "/search/?q=新正文", "/index.json", "/sitemap.xml", "/index.xml"} {
			body := siteCall(test, fixture, path, 200).Body.String()
			if strings.Contains(body, "/posts/nested/") != (status == "published") {
				test.Fatalf("stale publication: %s %s", status, path)
			}
		}
	}
	siteCall(test, fixture, "/legacy.html", 404)
}

func TestSiteBoundaries(test *testing.T) {
	fixture := siteFixture(test)
	for _, path := range []string{"/_server/site.json", "/.env", "/app.db", "/downloads/kukie-debug.apk", "/safe.css/", "/unknown/", "/posts/missing/", "/posts/nested/../中文/", "/%2e%2e/_server/site.json", "/posts/%252e%252e/", "/?page=9999999", "/?page=-1", "/?page=2"} {
		siteCall(test, fixture, path, 404)
	}
	siteCall(test, fixture, "/safe.css", 200)
	siteCall(test, fixture, "/escape.css", 503)
	siteCall(test, fixture, "/search/?q=不存在", 200)
	writer := httptest.NewRecorder()
	fixture.app.siteHandler().ServeHTTP(writer, httptest.NewRequest("HEAD", "/posts/nested/中文/", nil))
	if writer.Code != 200 || writer.Body.Len() != 0 {
		test.Fatal("HEAD returned a body")
	}
	writer = httptest.NewRecorder()
	fixture.app.siteHandler().ServeHTTP(writer, httptest.NewRequest("POST", "/", nil))
	if writer.Code != http.StatusMethodNotAllowed {
		test.Fatal("unsupported method accepted")
	}
	fixture.app.db.Close()
	siteCall(test, fixture, "/", 503)
	siteCall(test, fixture, "/posts/nested/中文/", 503)
}

func TestSiteNotices(test *testing.T) {
	fixture := siteFixture(test)
	_, err := fixture.app.db.Exec("INSERT INTO notices(id,title,text,status,updated_at) VALUES('visible','公告标题','完整公告正文 <script>bad()</script>','published','2026-10-01T00:00:00Z'),('hidden','隐私公告','不公开','draft','2026-10-01T00:00:00Z')")
	if err != nil {
		test.Fatal(err)
	}
	for _, path := range []string{"/", "/notices/", "/notices/visible/", "/api/v1/site/banner"} {
		body := siteCall(test, fixture, path, 200).Body.String()
		if !strings.Contains(body, "公告标题") || strings.Contains(body, "隐私公告") || strings.Contains(body, "<script>bad()") {
			test.Fatal("notice disclosure", path)
		}
	}
	siteCall(test, fixture, "/notices/hidden/", 404)
}

func TestBuiltSiteBundle(test *testing.T) {
	directory := os.Getenv("KUKIE_TEST_SITE_DIR")
	if directory == "" {
		test.Skip("set KUKIE_TEST_SITE_DIR for the real Hugo bundle")
	}
	fixture := newFixture(test)
	repository, err := filepath.Abs("../..")
	if err != nil {
		test.Fatal(err)
	}
	fixture.app.repository = repository
	if err = fixture.app.importPosts(); err != nil {
		test.Fatal(err)
	}
	if err = fixture.app.loadSite(directory, "https://runtime.example.test"); err != nil {
		test.Fatal(err)
	}
	defer fixture.app.site.root.Close()
	for _, path := range []string{"/", "/posts/cloudflare/subtrack-tutorial/", "/about/", "/links/", "/tags/", "/search/?q=Cloudflare"} {
		body := siteCall(test, fixture, path, 200).Body.String()
		if !strings.Contains(body, "https://runtime.example.test/") || strings.Contains(body, fixture.app.site.bundleOrigin+"/") {
			test.Fatalf("built page %s retains its build origin", path)
		}
	}
	siteCall(test, fixture, "/p/tunnel-manager/", 308)
	for _, asset := range fixture.app.site.manifest.Assets {
		siteCall(test, fixture, asset, 200)
	}
}

func TestSiteAdminPublicationLifecycle(test *testing.T) {
	fixture := siteFixture(test)
	input := post{Slug: "notes/新文章", Title: "发布验收", Markdown: "## 正文标题\n\n网站同步验收独有正文", Description: "新文章摘要", Category: "技术", Tags: []string{"验收"}, Date: "2026-10-01", Status: "draft"}
	created := call(test, fixture, "POST", "/api/v1/admin/posts", fixture.adminToken, input, 200)
	id := created["id"].(string)
	input.Revision = 1
	path := postPath(input.Slug)
	checkPublished := func(published bool) {
		test.Helper()
		status := 404
		if published {
			status = 200
		}
		siteCall(test, fixture, path, status)
		for _, route := range []string{"/", "/search/?q=网站同步验收独有正文", "/index.json", "/sitemap.xml", "/index.xml"} {
			body := siteCall(test, fixture, route, 200).Body.String()
			needle := "发布验收"
			if route == "/sitemap.xml" {
				needle = "/posts/notes/"
			}
			if strings.Contains(body, needle) != published {
				test.Fatalf("publication mismatch on %s: published=%v", route, published)
			}
		}
	}
	checkPublished(false)
	input.Status = "published"
	call(test, fixture, "PUT", "/api/v1/admin/posts/"+id, fixture.adminToken, input, 200)
	input.Revision++
	checkPublished(true)
	input.Title = "发布验收修改"
	input.Markdown += "\n\n修改后的正文"
	call(test, fixture, "PUT", "/api/v1/admin/posts/"+id, fixture.adminToken, input, 200)
	input.Revision++
	if !strings.Contains(siteCall(test, fixture, path, 200).Body.String(), "修改后的正文") {
		test.Fatal("published edit did not reach the site")
	}
	checkPublished(true)
	conflict := input
	conflict.Slug = "notes/changed"
	call(test, fixture, "PUT", "/api/v1/admin/posts/"+id, fixture.adminToken, conflict, 400)
	call(test, fixture, "POST", "/api/v1/admin/posts", fixture.adminToken, input, 409)
	input.Status = "draft"
	call(test, fixture, "PUT", "/api/v1/admin/posts/"+id, fixture.adminToken, input, 200)
	input.Revision++
	checkPublished(false)
	input.Status = "published"
	call(test, fixture, "PUT", "/api/v1/admin/posts/"+id, fixture.adminToken, input, 200)
	checkPublished(true)
	call(test, fixture, "DELETE", "/api/v1/admin/posts/"+id, fixture.adminToken, nil, 200)
	checkPublished(false)
}

func TestSiteMarkdownAndMetadata(test *testing.T) {
	fixture := siteFixture(test)
	markdown := "## 中文标题\n\n![图片](/safe.png)\n\n```go\nfmt.Println(\"hello\")\n```\n\n[恶意链接](javascript:alert(1))"
	_, err := fixture.app.db.Exec("UPDATE posts SET title=?,description=?,markdown=? WHERE id='site-post'", `</title><script>alert(7)</script>`, `\"><img src=x onerror=alert(8)>`, markdown)
	if err != nil {
		test.Fatal(err)
	}
	body := siteCall(test, fixture, "/posts/nested/中文/", 200).Body.String()
	for _, expected := range []string{`class="highlight"`, `class="language-go"`, `src="/safe.png"`, `&lt;/title&gt;`, `BlogPosting`} {
		if !strings.Contains(body, expected) {
			test.Fatalf("missing safe rendering: %s", expected)
		}
	}
	for _, forbidden := range []string{`<script>alert(7)</script>`, `<img src=x onerror=alert(8)>`, `href="javascript:`} {
		if strings.Contains(body, forbidden) {
			test.Fatalf("unsafe rendering: %s", forbidden)
		}
	}
	if _, err = fixture.app.db.Exec("UPDATE posts SET markdown='' WHERE id='site-post'"); err != nil {
		test.Fatal(err)
	}
	siteCall(test, fixture, "/posts/nested/中文/", 503)
}
