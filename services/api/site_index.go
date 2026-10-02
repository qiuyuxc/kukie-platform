package main

import (
	"encoding/xml"
	"fmt"
	"net/http"
	"sort"
	"time"
)

type sitemapEntry struct {
	Location string `xml:"loc"`
	Modified string `xml:"lastmod,omitempty"`
}
type sitemapDocument struct {
	XMLName   xml.Name       `xml:"urlset"`
	Namespace string         `xml:"xmlns,attr"`
	URLs      []sitemapEntry `xml:"url"`
}
type feedEntry struct {
	Title       string `xml:"title"`
	Link        string `xml:"link"`
	GUID        string `xml:"guid"`
	Description string `xml:"description"`
	Date        string `xml:"pubDate"`
}
type feedChannel struct {
	Title       string      `xml:"title"`
	Link        string      `xml:"link"`
	Description string      `xml:"description"`
	Items       []feedEntry `xml:"item"`
}
type feedDocument struct {
	XMLName xml.Name    `xml:"rss"`
	Version string      `xml:"version,attr"`
	Channel feedChannel `xml:"channel"`
}

func (app *server) siteIndex(writer http.ResponseWriter, request *http.Request, site *blogSite) {
	if request.URL.Path == "/robots.txt" {
		writer.Header().Set("Content-Type", "text/plain; charset=utf-8")
		if request.Method != "HEAD" {
			fmt.Fprintf(writer, "User-agent: *\nAllow: /\nDisallow: /search/\nDisallow: /api/\nDisallow: /_server/\nSitemap: %s/sitemap.xml\n", site.manifest.BaseURL)
		}
		return
	}
	query := "SELECT " + postColumns + " FROM posts WHERE status='published' ORDER BY date DESC,id"
	if request.URL.Path == "/index.xml" {
		query += " LIMIT 50"
	}
	rows, err := app.db.Query(query)
	if err != nil {
		app.siteFailure(writer, err)
		return
	}
	posts := []post{}
	for rows.Next() {
		item, scanErr := scanPost(rows)
		if scanErr != nil {
			rows.Close()
			app.siteFailure(writer, scanErr)
			return
		}
		posts = append(posts, item)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		app.siteFailure(writer, err)
		return
	}
	if request.URL.Path == "/index.json" {
		items := []map[string]any{}
		for _, item := range posts {
			items = append(items, map[string]any{"title": item.Title, "url": postPath(item.Slug), "excerpt": item.Description, "date": item.Date, "tags": item.Tags, "categories": []string{item.Category}, "cover": item.Cover})
		}
		if request.Method == "HEAD" {
			writer.Header().Set("Content-Type", "application/json; charset=utf-8")
			return
		}
		respond(writer, 200, items)
		return
	}
	var document any
	contentType := "application/xml; charset=utf-8"
	if request.URL.Path == "/index.xml" {
		feed := feedDocument{Version: "2.0", Channel: feedChannel{Title: site.manifest.Title, Link: site.manifest.BaseURL + "/", Description: site.manifest.Description}}
		for _, item := range posts {
			date, _ := time.Parse("2006-01-02", item.Date)
			link := site.absolute(postPath(item.Slug))
			feed.Channel.Items = append(feed.Channel.Items, feedEntry{Title: item.Title, Link: link, GUID: link, Description: item.Description, Date: date.Format(time.RFC1123Z)})
		}
		document, contentType = feed, "application/rss+xml; charset=utf-8"
	} else {
		sitemap := sitemapDocument{Namespace: "http://www.sitemaps.org/schemas/sitemap/0.9"}
		paths := []string{"/", "/posts/", "/categories/", "/tags/", "/archives/", "/notices/"}
		for path := range site.static {
			paths = append(paths, path)
		}
		sort.Strings(paths)
		for _, path := range paths {
			sitemap.URLs = append(sitemap.URLs, sitemapEntry{Location: site.absolute(path)})
		}
		for _, item := range posts {
			modified := item.UpdatedAt
			if _, err := time.Parse(time.RFC3339, modified); err != nil {
				modified = item.Date
			}
			sitemap.URLs = append(sitemap.URLs, sitemapEntry{Location: site.absolute(postPath(item.Slug)), Modified: modified})
		}
		notices, err := app.readSiteNotices()
		if err != nil {
			app.siteFailure(writer, err)
			return
		}
		for _, item := range notices {
			sitemap.URLs = append(sitemap.URLs, sitemapEntry{Location: site.absolute("/notices/" + item.ID + "/"), Modified: item.UpdatedAt})
		}
		document = sitemap
	}
	data, err := xml.Marshal(document)
	if err != nil {
		app.siteFailure(writer, err)
		return
	}
	writer.Header().Set("Content-Type", contentType)
	if request.Method != "HEAD" {
		fmt.Fprint(writer, xml.Header)
		_, _ = writer.Write(data)
	}
}
