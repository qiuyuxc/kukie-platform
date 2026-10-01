package main

import (
	"bytes"
	"fmt"
	"html/template"
	"strings"
	"unicode"

	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/parser"
	"golang.org/x/net/html"
)

type siteTerm struct {
	Name  string
	Count int
}
type siteHeading struct {
	ID, Title string
	Level     int
}
type siteSummary struct {
	Total                    int
	Categories, Tags, Months []siteTerm
	Recent                   []post
	Featured                 []post
	Random                   []map[string]string
	Dates                    []string
	Notices                  []notice
}

func (app *server) readSiteTerms(query string) ([]siteTerm, error) {
	rows, err := app.db.Query(query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []siteTerm{}
	for rows.Next() {
		var item siteTerm
		if err = rows.Scan(&item.Name, &item.Count); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (app *server) readSiteSummary() (siteSummary, error) {
	var summary siteSummary
	page, err := app.readPosts(postFilter{Limit: 21})
	if err != nil {
		return summary, err
	}
	summary.Total, summary.Recent = page.Total, page.Posts[:min(7, len(page.Posts))]
	summary.Featured = page.Posts[:min(12, len(page.Posts))]
	for _, item := range page.Posts {
		summary.Random = append(summary.Random, map[string]string{"title": item.Title, "url": postPath(item.Slug), "date": item.Date, "dateTime": item.Date})
	}
	summary.Categories, err = app.readSiteTerms("SELECT category,COUNT(*) FROM posts WHERE status='published' GROUP BY category ORDER BY category")
	if err != nil {
		return summary, err
	}
	summary.Tags, err = app.readSiteTerms("SELECT value,COUNT(DISTINCT posts.id) FROM posts,json_each(posts.tags) WHERE status='published' GROUP BY value ORDER BY COUNT(DISTINCT posts.id) DESC,value")
	if err != nil {
		return summary, err
	}
	summary.Months, err = app.readSiteTerms("SELECT substr(date,1,7),COUNT(*) FROM posts WHERE status='published' GROUP BY substr(date,1,7) ORDER BY 1 DESC")
	if err != nil {
		return summary, err
	}
	dates, err := app.readSiteTerms("SELECT date,COUNT(*) FROM posts WHERE status='published' GROUP BY date ORDER BY date DESC LIMIT 366")
	if err != nil {
		return summary, err
	}
	for _, date := range dates {
		for range date.Count {
			summary.Dates = append(summary.Dates, date.Name)
		}
	}
	summary.Notices, err = app.readSiteNotices()
	return summary, err
}

func (app *server) readSiteNotices() ([]notice, error) {
	rows, err := app.db.Query("SELECT id,title,text,status,updated_at,revision FROM notices WHERE status='published' ORDER BY updated_at DESC,id DESC LIMIT 50")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []notice{}
	for rows.Next() {
		var item notice
		if err = rows.Scan(&item.ID, &item.Title, &item.Text, &item.Status, &item.UpdatedAt, &item.Revision); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

type headingIDs struct{ used map[string]bool }

func (ids *headingIDs) Put(value []byte) { ids.used[string(value)] = true }
func (ids *headingIDs) Generate(value []byte, kind ast.NodeKind) []byte {
	var name strings.Builder
	for _, character := range strings.ToLower(strings.TrimSpace(string(value))) {
		if unicode.IsLetter(character) || unicode.IsDigit(character) || character == '-' || character == '_' {
			name.WriteRune(character)
		} else if unicode.IsSpace(character) {
			name.WriteByte('-')
		}
	}
	base := name.String()
	if base == "" {
		base = "heading"
	}
	id := base
	for suffix := 1; ids.used[id]; suffix++ {
		id = fmt.Sprintf("%s-%d", base, suffix)
	}
	ids.used[id] = true
	return []byte(id)
}

func siteMarkdown(markdown string) (template.HTML, []siteHeading, error) {
	var output bytes.Buffer
	context := parser.NewContext(parser.WithIDs(&headingIDs{used: map[string]bool{}}))
	if err := markdownRenderer.Convert([]byte(markdown), &output, parser.WithContext(context)); err != nil {
		return "", nil, err
	}
	clean := htmlPolicy.Sanitize(output.String())
	document, err := html.Parse(strings.NewReader(clean))
	if err != nil {
		return "", nil, err
	}
	headings := []siteHeading{}
	var body *html.Node
	blocks := []*html.Node{}
	var text func(*html.Node) string
	text = func(node *html.Node) string {
		if node.Type == html.TextNode {
			return node.Data
		}
		var value strings.Builder
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			value.WriteString(text(child))
		}
		return value.String()
	}
	var visit func(*html.Node)
	visit = func(node *html.Node) {
		if node.Type == html.ElementNode && node.Data == "body" {
			body = node
		}
		if node.Type == html.ElementNode && node.Data == "pre" {
			blocks = append(blocks, node)
		}
		if node.Type == html.ElementNode && len(node.Data) == 2 && node.Data[0] == 'h' && node.Data[1] >= '2' && node.Data[1] <= '4' {
			for _, attribute := range node.Attr {
				if attribute.Key == "id" {
					headings = append(headings, siteHeading{ID: attribute.Val, Title: text(node), Level: int(node.Data[1] - '0')})
				}
			}
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			visit(child)
		}
	}
	visit(document)
	for _, block := range blocks {
		wrapper := &html.Node{Type: html.ElementNode, Data: "div", Attr: []html.Attribute{{Key: "class", Val: "highlight"}}}
		block.Parent.InsertBefore(wrapper, block)
		block.Parent.RemoveChild(block)
		wrapper.AppendChild(block)
	}
	output.Reset()
	if body != nil {
		for child := body.FirstChild; child != nil; child = child.NextSibling {
			if err := html.Render(&output, child); err != nil {
				return "", nil, err
			}
		}
	}
	return template.HTML(output.String()), headings, nil
}
