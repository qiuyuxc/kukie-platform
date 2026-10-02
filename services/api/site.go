package main

import (
	"bytes"
	"database/sql"
	"errors"
	"fmt"
	"html/template"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

type sitePage struct {
	Site                                                                    siteManifest
	Head                                                                    template.HTML
	Title, Description, Canonical, Image, Kind, Query, Category, Tag, Month string
	Home, NoIndex                                                           bool
	Post                                                                    *post
	Notice                                                                  *notice
	Body                                                                    template.HTML
	Headings                                                                []siteHeading
	Summary                                                                 siteSummary
	Listing                                                                 postPage
	Previous, Next                                                          string
	Schema                                                                  any
	Comments                                                                []comment
}

func (site *blogSite) absolute(path string) string {
	if strings.HasPrefix(path, site.bundleOrigin+"/") {
		path = strings.TrimPrefix(path, site.bundleOrigin)
	}
	parsed, err := url.Parse(path)
	if err != nil {
		return site.manifest.BaseURL + "/"
	}
	base, _ := url.Parse(site.manifest.BaseURL)
	return base.ResolveReference(parsed).String()
}

func (app *server) siteHandler() http.Handler {
	readerAPI := app.siteReaderAPI()
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("X-Content-Type-Options", "nosniff")
		writer.Header().Set("X-Frame-Options", "DENY")
		writer.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
		writer.Header().Set("Cache-Control", "no-store")
		if app.site != nil && strings.HasPrefix(request.URL.Path, "/api/") {
			readerAPI.ServeHTTP(writer, request)
			return
		}
		if request.Method != http.MethodGet && request.Method != http.MethodHead {
			writer.Header().Set("Allow", "GET, HEAD")
			http.Error(writer, "Method not allowed", 405)
			return
		}
		if app.site == nil {
			http.NotFound(writer, request)
			return
		}
		site := *app.site
		if site.manifest.BaseURL == "" {
			origin, err := app.requestOrigin(request)
			if err != nil {
				http.Error(writer, "Invalid request host", http.StatusBadRequest)
				return
			}
			site.manifest.BaseURL = origin
		}
		path := request.URL.Path
		if path != "/" && !validSitePath(path) {
			http.NotFound(writer, request)
			return
		}
		if path == "/api/v1/site/banner" {
			app.siteBanner(writer, request)
			return
		}
		if strings.HasPrefix(path, "/media/") && !strings.Contains(strings.TrimPrefix(path, "/media/"), "/") {
			request.SetPathValue("id", strings.TrimPrefix(path, "/media/"))
			app.serveMedia(writer, request)
			return
		}
		if app.site.indexNowKey != "" && path == "/"+app.site.indexNowKey+".txt" {
			writer.Header().Set("Content-Type", "text/plain; charset=utf-8")
			if request.Method != "HEAD" {
				fmt.Fprint(writer, app.site.indexNowKey)
			}
			return
		}
		if path == "/sitemap.xml" || path == "/index.xml" || path == "/index.json" || path == "/robots.txt" {
			app.siteIndex(writer, request, &site)
			return
		}
		if app.site.assets[path] {
			app.serveSiteFile(writer, request, strings.TrimPrefix(path, "/"), true, site.manifest.BaseURL)
			return
		}
		if file, exists := app.site.static[path]; exists {
			app.serveSiteFile(writer, request, file, false, site.manifest.BaseURL)
			return
		}
		if _, exists := app.site.static[path+"/"]; exists {
			http.Redirect(writer, request, path+"/", 308)
			return
		}
		page, err := app.resolveSitePage(request, &site)
		if errors.Is(err, sql.ErrNoRows) {
			var slug string
			err = app.db.QueryRow("SELECT p.slug FROM post_aliases a JOIN posts p ON p.id=a.post_id WHERE a.path=? AND p.status='published'", path).Scan(&slug)
			if err == nil {
				http.Redirect(writer, request, site.absolute(postPath(slug)), 308)
				return
			}
			if errors.Is(err, sql.ErrNoRows) {
				http.NotFound(writer, request)
				return
			}
		}
		if err != nil {
			app.siteFailure(writer, err)
			return
		}
		if !strings.HasSuffix(path, "/") {
			target := path + "/"
			if request.URL.RawQuery != "" {
				target += "?" + request.URL.RawQuery
			}
			http.Redirect(writer, request, target, 308)
			return
		}
		app.renderSite(writer, request, page)
	})
}

func (app *server) serveSiteFile(writer http.ResponseWriter, request *http.Request, name string, asset bool, origin string) {
	file, err := app.site.root.Open(name)
	if err != nil {
		app.siteFailure(writer, err)
		return
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		http.NotFound(writer, request)
		return
	}
	if asset {
		writer.Header().Set("Cache-Control", "public, max-age=3600")
	} else if strings.HasSuffix(name, ".html") {
		data, err := io.ReadAll(file)
		if err != nil {
			app.siteFailure(writer, err)
			return
		}
		http.ServeContent(writer, request, name, time.Time{}, bytes.NewReader(app.site.rebaseHTML(data, origin)))
		return
	}
	http.ServeContent(writer, request, name, info.ModTime(), file)
}

func (app *server) resolveSitePage(request *http.Request, site *blogSite) (sitePage, error) {
	page := sitePage{Site: site.manifest, Head: site.postHead, Description: site.manifest.Description, Image: site.absolute(site.manifest.DefaultCover), Kind: "list"}
	path := request.URL.Path
	clean := strings.TrimSuffix(path, "/")
	filter := requestedPostFilter(request)
	filter.Limit, filter.SearchBody = 10, true
	if value := request.URL.Query().Get("page"); value != "" {
		number, err := strconv.Atoi(value)
		if err != nil || number < 1 || number > 1000000 {
			return page, sql.ErrNoRows
		}
	}
	if strings.HasPrefix(clean, "/posts/") {
		item, err := app.readPostBySlug(strings.TrimPrefix(clean, "/posts/"))
		if err != nil {
			return page, err
		}
		page.Post, page.Title, page.Description, page.Kind = &item, item.Title, item.Description, "post"
		page.Comments, err = app.readComments(item.ID, false)
		if err != nil {
			return page, err
		}
		page.Body, page.Headings, err = siteMarkdown(item.Markdown)
		if err != nil {
			return page, err
		}
		if strings.TrimSpace(string(page.Body)) == "" {
			return page, errors.New("empty rendered article")
		}
		if item.Cover != "" && safeImageURL(item.Cover) {
			page.Image = site.absolute(item.Cover)
		}
		page.Canonical = site.absolute(postPath(item.Slug))
		page.Schema = map[string]any{"@context": "https://schema.org", "@type": "BlogPosting", "headline": item.Title, "description": item.Description, "url": page.Canonical, "mainEntityOfPage": page.Canonical, "image": page.Image, "datePublished": item.Date, "dateModified": item.UpdatedAt, "author": map[string]string{"@type": "Person", "name": site.manifest.Author}}
	} else if strings.HasPrefix(clean, "/notices/") {
		var item notice
		err := app.db.QueryRow("SELECT id,title,text,status,updated_at,revision FROM notices WHERE id=? AND status='published'", strings.TrimPrefix(clean, "/notices/")).Scan(&item.ID, &item.Title, &item.Text, &item.Status, &item.UpdatedAt, &item.Revision)
		if err != nil {
			return page, err
		}
		page.Notice, page.Title, page.Kind = &item, item.Title, "notice"
		page.Canonical = site.absolute("/notices/" + item.ID + "/")
	} else {
		switch {
		case clean == "" || strings.HasPrefix(clean, "/page/"):
			page.Home, page.Head, page.Title = true, site.homeHead, site.manifest.Title
			if clean != "" {
				number, err := strconv.Atoi(strings.TrimPrefix(clean, "/page/"))
				if err != nil || number < 1 || number > 1000000 {
					return page, sql.ErrNoRows
				}
				filter.Page = number
			}
		case clean == "/posts":
			page.Title = "文章"
		case clean == "/search":
			page.Title, page.NoIndex = "搜索", true
		case clean == "/categories" || strings.HasPrefix(clean, "/categories/"):
			page.Title = "分类"
			if strings.HasPrefix(clean, "/categories/") {
				filter.Category = strings.TrimPrefix(clean, "/categories/")
			}
		case clean == "/tags" || strings.HasPrefix(clean, "/tags/"):
			page.Title = "标签"
			if strings.HasPrefix(clean, "/tags/") {
				filter.Tag = strings.TrimPrefix(clean, "/tags/")
			}
		case clean == "/archives":
			page.Title = "归档"
			filter.Month = request.URL.Query().Get("month")
		case clean == "/notices":
			page.Title, page.Kind = "公告", "notices"
		default:
			return page, sql.ErrNoRows
		}
		if page.Home {
			filter.Query, filter.Category, filter.Tag = "", "", ""
		}
		page.Query, page.Category, page.Tag, page.Month = filter.Query, filter.Category, filter.Tag, filter.Month
		listing, err := app.readPosts(filter)
		if err != nil {
			return page, err
		}
		page.Listing = listing
		if listing.Page > 1 && len(listing.Posts) == 0 {
			return page, sql.ErrNoRows
		}
		values := url.Values{}
		if page.Query != "" {
			values.Set("q", page.Query)
		}
		if page.Category != "" {
			values.Set("category", page.Category)
			page.Title += " · " + page.Category
		}
		if page.Tag != "" {
			values.Set("tag", page.Tag)
			page.Title += " · " + page.Tag
		}
		if page.Month != "" {
			values.Set("month", page.Month)
			page.Title += " · " + page.Month
		}
		basePath := clean + "/"
		if page.Home {
			basePath = "/"
		}
		if strings.HasPrefix(clean, "/categories/") {
			basePath = "/categories/"
		}
		if strings.HasPrefix(clean, "/tags/") {
			basePath = "/tags/"
		}
		pageURL := func(number int) string {
			query := url.Values{}
			for key, value := range values {
				query[key] = value
			}
			if number > 1 {
				query.Set("page", strconv.Itoa(number))
			}
			if len(query) == 0 {
				return basePath
			}
			return basePath + "?" + query.Encode()
		}
		page.Canonical = site.absolute(pageURL(listing.Page))
		if listing.Page > 1 {
			page.Previous = pageURL(listing.Page - 1)
		}
		if listing.HasMore {
			page.Next = pageURL(listing.Page + 1)
		}
	}
	if page.Description == "" {
		page.Description = site.manifest.Description
	}
	summary, err := app.readSiteSummary()
	page.Summary = summary
	return page, err
}

func (app *server) renderSite(writer http.ResponseWriter, request *http.Request, page sitePage) {
	var output bytes.Buffer
	if err := app.site.templates.ExecuteTemplate(&output, "base.html", page); err != nil {
		app.siteFailure(writer, err)
		return
	}
	writer.Header().Set("Content-Type", "text/html; charset=utf-8")
	if page.NoIndex {
		writer.Header().Set("X-Robots-Tag", "noindex, follow")
	}
	writer.WriteHeader(http.StatusOK)
	if request.Method != "HEAD" {
		_, _ = writer.Write(app.site.rebaseHTML(output.Bytes(), page.Site.BaseURL))
	}
}

func (app *server) siteFailure(writer http.ResponseWriter, err error) {
	log.Printf("Site request failed: %v", err)
	writer.Header().Set("Retry-After", "30")
	http.Error(writer, "页面暂时不可用，请稍后重试。", http.StatusServiceUnavailable)
}

func (app *server) siteBanner(writer http.ResponseWriter, request *http.Request) {
	notices, err := app.readSiteNotices()
	if err != nil {
		app.siteFailure(writer, err)
		return
	}
	if len(notices) == 0 {
		respond(writer, 200, map[string]any{"enabled": false})
		return
	}
	item := notices[0]
	respond(writer, 200, map[string]any{"enabled": true, "level": "info", "badge": "公告", "text": item.Title, "link": "/notices/" + item.ID + "/", "external": false, "updated": item.UpdatedAt})
}

func (app *server) configureSite() error {
	directory := os.Getenv("KUKIE_SITE_DIR")
	if directory == "" {
		return nil
	}
	return app.loadSite(directory, os.Getenv("KUKIE_SITE_URL"))
}
