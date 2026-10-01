package main

import (
	"bytes"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/microcosm-cc/bluemonday"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/parser"
	"gopkg.in/yaml.v3"
)

type post struct {
	ID          string   `json:"id"`
	Slug        string   `json:"slug"`
	Title       string   `json:"title"`
	Description string   `json:"description"`
	Markdown    string   `json:"markdown,omitempty"`
	HTML        string   `json:"html,omitempty"`
	Cover       string   `json:"cover"`
	Category    string   `json:"category"`
	Tags        []string `json:"tags"`
	Date        string   `json:"date"`
	Status      string   `json:"status"`
	Revision    int      `json:"revision"`
	UpdatedAt   string   `json:"updated_at"`
	Minutes     int      `json:"minutes"`
}

const postColumns = "id,slug,title,description,markdown,cover,category,tags,date,status,revision,updated_at"

func scanPost(row scanner) (post, error) {
	var item post
	var tags string
	err := row.Scan(&item.ID, &item.Slug, &item.Title, &item.Description, &item.Markdown, &item.Cover, &item.Category, &tags, &item.Date, &item.Status, &item.Revision, &item.UpdatedAt)
	item.Tags = []string{}
	_ = json.Unmarshal([]byte(tags), &item.Tags)
	item.Minutes = max(1, utf8.RuneCountInString(item.Markdown)/450)
	return item, err
}

var markdownRenderer = goldmark.New(goldmark.WithExtensions(extension.GFM), goldmark.WithParserOptions(parser.WithAutoHeadingID()))
var htmlPolicy = func() *bluemonday.Policy {
	policy := bluemonday.UGCPolicy()
	policy.AllowAttrs("id").OnElements("h1", "h2", "h3", "h4", "h5", "h6")
	policy.AllowAttrs("class").Matching(regexp.MustCompile(`^language-[a-zA-Z0-9_-]+$`)).OnElements("code")
	return policy
}()

func renderMarkdown(markdown string) string {
	var output bytes.Buffer
	if markdownRenderer.Convert([]byte(markdown), &output) != nil {
		return ""
	}
	return htmlPolicy.Sanitize(output.String())
}
func safeImageURL(value string) bool {
	if value == "" {
		return true
	}
	if strings.HasPrefix(value, "/") && !strings.HasPrefix(value, "//") && !strings.ContainsAny(value, "\\\r\n") {
		return true
	}
	parsed, err := url.Parse(value)
	return err == nil && (parsed.Scheme == "https" || parsed.Scheme == "http") && parsed.Host != "" && parsed.User == nil
}
func (app *server) importPosts() error {
	folder := filepath.Join(app.repository, "content", "posts")
	if _, err := os.Stat(folder); os.IsNotExist(err) {
		return nil
	}
	transaction, err := app.db.Begin()
	if err != nil {
		return err
	}
	defer transaction.Rollback()
	var imported string
	if err = transaction.QueryRow("SELECT value FROM settings WHERE name='hugo_import_complete'").Scan(&imported); err == nil {
		return nil
	} else if err != sql.ErrNoRows {
		return err
	}
	err = filepath.WalkDir(folder, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || entry.Type()&os.ModeSymlink != 0 || !strings.HasSuffix(entry.Name(), ".md") || strings.HasPrefix(entry.Name(), "_") {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		text := strings.ReplaceAll(string(data), "\r\n", "\n")
		if !strings.HasPrefix(text, "---\n") {
			return nil
		}
		parts := strings.SplitN(text[4:], "\n---", 2)
		if len(parts) != 2 {
			return nil
		}
		var metadata struct {
			Title       string   `yaml:"title"`
			Description string   `yaml:"description"`
			Date        string   `yaml:"date"`
			Cover       string   `yaml:"cover"`
			Categories  []string `yaml:"categories"`
			Tags        []string `yaml:"tags"`
			Draft       bool     `yaml:"draft"`
		}
		if err = yaml.Unmarshal([]byte(parts[0]), &metadata); err != nil {
			return err
		}
		slug, err := filepath.Rel(folder, path)
		if err != nil {
			return err
		}
		slug = strings.TrimSuffix(filepath.ToSlash(slug), ".md")
		sum := sha256.Sum256([]byte(slug))
		id := hex.EncodeToString(sum[:16])
		date := metadata.Date
		if len(date) >= 10 {
			date = date[:10]
		}
		if _, err = time.Parse("2006-01-02", date); err != nil {
			date = time.Now().Format("2006-01-02")
		}
		status := "published"
		if metadata.Draft {
			status = "draft"
		}
		category := "随笔"
		if len(metadata.Categories) > 0 {
			category = metadata.Categories[0]
		}
		if metadata.Tags == nil {
			metadata.Tags = []string{}
		}
		tags, _ := json.Marshal(metadata.Tags)
		_, err = transaction.Exec("INSERT OR IGNORE INTO posts(id,slug,title,description,markdown,cover,category,tags,date,status,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?)", id, slug, metadata.Title, metadata.Description, strings.TrimSpace(parts[1]), metadata.Cover, category, string(tags), date, status, time.Now().UTC().Format(time.RFC3339))
		return err
	})
	if err != nil {
		return err
	}
	if _, err = transaction.Exec("INSERT INTO settings(name,value) VALUES('hugo_import_complete',?)", time.Now().UTC().Format(time.RFC3339)); err != nil {
		return err
	}
	return transaction.Commit()
}
func (app *server) queryPosts(writer http.ResponseWriter, request *http.Request, admin bool, bookmarkUser string) {
	filter := requestedPostFilter(request)
	filter.Admin, filter.UserID = admin, bookmarkUser
	result, err := app.readPosts(filter)
	if databaseFailure(writer, err) {
		return
	}
	respond(writer, 200, result)
}
func (app *server) listPosts(writer http.ResponseWriter, request *http.Request) {
	app.queryPosts(writer, request, false, "")
}
func (app *server) adminPosts(writer http.ResponseWriter, request *http.Request, who identity) {
	app.queryPosts(writer, request, true, "")
}
func (app *server) listBookmarks(writer http.ResponseWriter, request *http.Request, who identity) {
	app.queryPosts(writer, request, false, who.User.ID)
}
func (app *server) getPost(writer http.ResponseWriter, request *http.Request) {
	item, err := scanPost(app.db.QueryRow("SELECT "+postColumns+" FROM posts WHERE id=? AND status='published'", request.PathValue("id")))
	app.respondPost(writer, item, err)
}
func (app *server) respondPost(writer http.ResponseWriter, item post, err error) {
	if err == sql.ErrNoRows {
		notFound(writer)
		return
	}
	if databaseFailure(writer, err) {
		return
	}
	item.HTML = renderMarkdown(item.Markdown)
	respond(writer, 200, map[string]any{"post": item})
}
func (app *server) savePost(writer http.ResponseWriter, request *http.Request, who identity) {
	var input post
	if !readJSON(writer, request, &input) {
		return
	}
	input.Title = strings.TrimSpace(input.Title)
	input.Category = strings.TrimSpace(input.Category)
	if input.Title == "" || utf8.RuneCountInString(input.Title) > 160 || strings.TrimSpace(input.Markdown) == "" || len(input.Markdown) > 1000000 || utf8.RuneCountInString(input.Description) > 300 || !safeImageURL(input.Cover) || (input.Status != "draft" && input.Status != "published") {
		fail(writer, 400, "invalid_post", "请填写有效标题、正文、摘要、封面与发布状态")
		return
	}
	if _, err := time.Parse("2006-01-02", input.Date); err != nil {
		fail(writer, 400, "invalid_date", "日期格式应为 YYYY-MM-DD")
		return
	}
	if input.Category == "" {
		input.Category = "随笔"
	}
	if utf8.RuneCountInString(input.Category) > 30 || len(input.Tags) > 20 {
		fail(writer, 400, "invalid_tags", "分类或标签过长")
		return
	}
	if input.Tags == nil {
		input.Tags = []string{}
	}
	for _, tag := range input.Tags {
		if strings.TrimSpace(tag) == "" || utf8.RuneCountInString(tag) > 60 {
			fail(writer, 400, "invalid_tags", "标签需为 1–60 字")
			return
		}
	}
	tags, _ := json.Marshal(input.Tags)
	updated := time.Now().UTC().Format(time.RFC3339)
	id := request.PathValue("id")
	if id == "" {
		id = randomToken()
		if input.Slug == "" {
			input.Slug = "app-" + id
		}
		if !validPostSlug(input.Slug) {
			fail(writer, 400, "invalid_slug", "文章地址仅支持文字、数字、连字符、下划线及分层路径")
			return
		}
		var exists int
		if databaseFailure(writer, app.db.QueryRow("SELECT (SELECT COUNT(*) FROM posts WHERE slug=?)+(SELECT COUNT(*) FROM post_aliases WHERE path=?)", input.Slug, postPath(input.Slug)).Scan(&exists)) {
			return
		}
		if exists > 0 {
			fail(writer, 409, "slug_conflict", "文章地址已被使用")
			return
		}
		result, err := app.db.Exec("INSERT INTO posts(id,slug,title,description,markdown,cover,category,tags,date,status,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(slug) DO NOTHING", id, input.Slug, input.Title, input.Description, input.Markdown, input.Cover, input.Category, string(tags), input.Date, input.Status, updated)
		if databaseFailure(writer, err) {
			return
		}
		if !changed(result) {
			fail(writer, 409, "slug_conflict", "文章地址已被使用")
			return
		}
	} else {
		if input.Slug != "" {
			var existing string
			err := app.db.QueryRow("SELECT slug FROM posts WHERE id=?", id).Scan(&existing)
			if err == sql.ErrNoRows {
				notFound(writer)
				return
			}
			if databaseFailure(writer, err) {
				return
			}
			if existing != input.Slug {
				fail(writer, 400, "immutable_slug", "文章地址创建后不可修改，以保持旧链接有效")
				return
			}
		}
		result, err := app.db.Exec("UPDATE posts SET title=?,description=?,markdown=?,cover=?,category=?,tags=?,date=?,status=?,updated_at=?,revision=revision+1 WHERE id=? AND revision=? AND status<>'deleted'", input.Title, input.Description, input.Markdown, input.Cover, input.Category, string(tags), input.Date, input.Status, updated, id, input.Revision)
		if databaseFailure(writer, err) {
			return
		}
		if !changed(result) {
			fail(writer, 409, "revision_conflict", "文章已被其他操作修改，请重新加载后编辑")
			return
		}
	}
	app.audit(who, "post.save", id)
	respond(writer, 200, map[string]string{"id": id})
}
func (app *server) deletePost(writer http.ResponseWriter, request *http.Request, who identity) {
	result, err := app.db.Exec("UPDATE posts SET status='deleted',revision=revision+1,updated_at=? WHERE id=? AND status<>'deleted'", time.Now().UTC().Format(time.RFC3339), request.PathValue("id"))
	if databaseFailure(writer, err) {
		return
	}
	if !changed(result) {
		notFound(writer)
		return
	}
	app.audit(who, "post.delete", request.PathValue("id"))
	respond(writer, 200, map[string]bool{"ok": true})
}
func (app *server) bookmark(writer http.ResponseWriter, request *http.Request, who identity) {
	id := request.PathValue("id")
	if request.Method == "DELETE" {
		_, err := app.db.Exec("DELETE FROM bookmarks WHERE user_id=? AND post_id=?", who.User.ID, id)
		if databaseFailure(writer, err) {
			return
		}
	} else {
		var count int
		if databaseFailure(writer, app.db.QueryRow("SELECT COUNT(*) FROM posts WHERE id=? AND status='published'", id).Scan(&count)) {
			return
		}
		if count == 0 {
			notFound(writer)
			return
		}
		_, err := app.db.Exec("INSERT OR IGNORE INTO bookmarks(user_id,post_id) VALUES(?,?)", who.User.ID, id)
		if databaseFailure(writer, err) {
			return
		}
	}
	respond(writer, 200, map[string]bool{"ok": true})
}

type notice struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	Text      string `json:"text"`
	Status    string `json:"status"`
	UpdatedAt string `json:"updated_at"`
	Revision  int    `json:"revision"`
}

func (app *server) queryNotices(writer http.ResponseWriter, admin bool) {
	where := " WHERE status='published'"
	if admin {
		where = ""
	}
	rows, err := app.db.Query("SELECT id,title,text,status,updated_at,revision FROM notices" + where + " ORDER BY updated_at DESC LIMIT 100")
	if databaseFailure(writer, err) {
		return
	}
	defer rows.Close()
	items := []notice{}
	for rows.Next() {
		var item notice
		if databaseFailure(writer, rows.Scan(&item.ID, &item.Title, &item.Text, &item.Status, &item.UpdatedAt, &item.Revision)) {
			return
		}
		items = append(items, item)
	}
	if databaseFailure(writer, rows.Err()) {
		return
	}
	respond(writer, 200, map[string]any{"notices": items})
}
func (app *server) listNotices(writer http.ResponseWriter, request *http.Request) {
	app.queryNotices(writer, false)
}
func (app *server) getNotice(writer http.ResponseWriter, request *http.Request) {
	var item notice
	err := app.db.QueryRow("SELECT id,title,text,status,updated_at,revision FROM notices WHERE id=? AND status='published'", request.PathValue("id")).Scan(&item.ID, &item.Title, &item.Text, &item.Status, &item.UpdatedAt, &item.Revision)
	if err == sql.ErrNoRows {
		notFound(writer)
		return
	}
	if databaseFailure(writer, err) {
		return
	}
	respond(writer, 200, map[string]any{"notice": item})
}
func (app *server) adminNotices(writer http.ResponseWriter, request *http.Request, who identity) {
	app.queryNotices(writer, true)
}
func (app *server) saveNotice(writer http.ResponseWriter, request *http.Request, who identity) {
	var input notice
	if !readJSON(writer, request, &input) {
		return
	}
	if strings.TrimSpace(input.Title) == "" || utf8.RuneCountInString(input.Title) > 160 || strings.TrimSpace(input.Text) == "" || utf8.RuneCountInString(input.Text) > 5000 || (input.Status != "draft" && input.Status != "published") {
		fail(writer, 400, "invalid_notice", "请填写公告标题、正文和状态")
		return
	}
	id := request.PathValue("id")
	updated := time.Now().UTC().Format(time.RFC3339)
	if id == "" {
		id = randomToken()
		_, err := app.db.Exec("INSERT INTO notices(id,title,text,status,updated_at) VALUES(?,?,?,?,?)", id, input.Title, input.Text, input.Status, updated)
		if databaseFailure(writer, err) {
			return
		}
	} else {
		result, err := app.db.Exec("UPDATE notices SET title=?,text=?,status=?,updated_at=?,revision=revision+1 WHERE id=? AND revision=?", input.Title, input.Text, input.Status, updated, id, input.Revision)
		if databaseFailure(writer, err) {
			return
		}
		if !changed(result) {
			fail(writer, 409, "revision_conflict", "公告已被修改，请刷新")
			return
		}
	}
	app.audit(who, "notice.save", id)
	respond(writer, 200, map[string]string{"id": id})
}
func (app *server) deleteNotice(writer http.ResponseWriter, request *http.Request, who identity) {
	result, err := app.db.Exec("DELETE FROM notices WHERE id=?", request.PathValue("id"))
	if databaseFailure(writer, err) {
		return
	}
	if !changed(result) {
		notFound(writer)
		return
	}
	app.audit(who, "notice.delete", request.PathValue("id"))
	respond(writer, 200, map[string]bool{"ok": true})
}

type comment struct {
	ID          string `json:"id"`
	PostID      string `json:"post_id"`
	PostTitle   string `json:"post_title"`
	Name        string `json:"name"`
	Avatar      string `json:"avatar"`
	AuthorLabel string `json:"author_label,omitempty"`
	Text        string `json:"text"`
	CreatedAt   string `json:"created_at"`
}

func (app *server) queryComments(writer http.ResponseWriter, request *http.Request, admin bool) {
	items, err := app.readComments(request.PathValue("id"), admin)
	if databaseFailure(writer, err) {
		return
	}
	respond(writer, 200, map[string]any{"comments": items})
}

func (app *server) readComments(postID string, admin bool) ([]comment, error) {
	where := " WHERE p.status='published' AND c.post_id=?"
	arguments := []any{postID}
	if admin {
		where = ""
		arguments = nil
	}
	rows, err := app.db.Query("SELECT c.id,c.post_id,p.title,u.name,u.avatar,CASE WHEN u.role='admin' THEN '博主' ELSE '' END,c.text,c.created_at FROM comments c JOIN users u ON c.user_id=u.id JOIN posts p ON p.id=c.post_id"+where+" ORDER BY c.created_at DESC,c.id DESC LIMIT 100", arguments...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []comment{}
	for rows.Next() {
		var item comment
		if err := rows.Scan(&item.ID, &item.PostID, &item.PostTitle, &item.Name, &item.Avatar, &item.AuthorLabel, &item.Text, &item.CreatedAt); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}
func (app *server) listComments(writer http.ResponseWriter, request *http.Request) {
	app.queryComments(writer, request, false)
}
func (app *server) adminComments(writer http.ResponseWriter, request *http.Request, who identity) {
	app.queryComments(writer, request, true)
}
func (app *server) createComment(writer http.ResponseWriter, request *http.Request, who identity) {
	if app.limited(writer, "comment:"+who.User.ID, 10, time.Minute) {
		return
	}
	var input struct {
		Text string `json:"text"`
	}
	if !readJSON(writer, request, &input) {
		return
	}
	text := strings.TrimSpace(input.Text)
	if text == "" || utf8.RuneCountInString(text) > 1000 {
		fail(writer, 400, "invalid_comment", "评论需为 1–1000 字")
		return
	}
	id := randomToken()
	result, err := app.db.Exec("INSERT INTO comments(id,post_id,user_id,text,created_at) SELECT ?,id,?,?,? FROM posts WHERE id=? AND status='published'", id, who.User.ID, text, time.Now().UTC().Format(time.RFC3339), request.PathValue("id"))
	if databaseFailure(writer, err) {
		return
	}
	if !changed(result) {
		notFound(writer)
		return
	}
	respond(writer, 201, map[string]string{"id": id})
}
func (app *server) deleteComment(writer http.ResponseWriter, request *http.Request, who identity) {
	result, err := app.db.Exec("DELETE FROM comments WHERE id=?", request.PathValue("id"))
	if databaseFailure(writer, err) {
		return
	}
	if !changed(result) {
		notFound(writer)
		return
	}
	app.audit(who, "comment.delete", request.PathValue("id"))
	respond(writer, 200, map[string]bool{"ok": true})
}
func (app *server) listUsers(writer http.ResponseWriter, request *http.Request, who identity) {
	rows, err := app.db.Query("SELECT " + userColumns + " FROM users ORDER BY created_at DESC LIMIT 100")
	if databaseFailure(writer, err) {
		return
	}
	defer rows.Close()
	users := []user{}
	for rows.Next() {
		account, err := scanUser(rows)
		if databaseFailure(writer, err) {
			return
		}
		users = append(users, account)
	}
	if databaseFailure(writer, rows.Err()) {
		return
	}
	respond(writer, 200, map[string]any{"users": users})
}
