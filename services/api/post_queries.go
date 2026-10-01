package main

import (
	"net/http"
	"strconv"
	"strings"
	"unicode"
)

type postFilter struct {
	Page, Limit                         int
	Query, Category, Tag, Month, UserID string
	Admin, SearchBody                   bool
}

type postPage struct {
	Posts   []post `json:"posts"`
	Page    int    `json:"page"`
	Total   int    `json:"total"`
	HasMore bool   `json:"has_more"`
}

func (app *server) readPosts(filter postFilter) (postPage, error) {
	filter.Page = max(1, min(filter.Page, 1000000))
	filter.Limit = max(1, min(filter.Limit, 50))
	where := "status='published'"
	if filter.Admin {
		where = "status<>'deleted'"
	}
	arguments := []any{}
	if filter.Query != "" {
		where += " AND (title LIKE ? OR description LIKE ?"
		pattern := "%" + filter.Query + "%"
		arguments = append(arguments, pattern, pattern)
		if filter.SearchBody {
			where += " OR markdown LIKE ?"
			arguments = append(arguments, pattern)
		}
		where += ")"
	}
	if filter.Category != "" {
		where += " AND category=?"
		arguments = append(arguments, filter.Category)
	}
	if filter.Tag != "" {
		where += " AND EXISTS(SELECT 1 FROM json_each(posts.tags) WHERE value=?)"
		arguments = append(arguments, filter.Tag)
	}
	if filter.Month != "" {
		where += " AND substr(date,1,7)=?"
		arguments = append(arguments, filter.Month)
	}
	if filter.UserID != "" {
		where += " AND id IN (SELECT post_id FROM bookmarks WHERE user_id=?)"
		arguments = append(arguments, filter.UserID)
	}
	result := postPage{Posts: []post{}, Page: filter.Page}
	transaction, err := app.db.Begin()
	if err != nil {
		return result, err
	}
	defer transaction.Rollback()
	if err = transaction.QueryRow("SELECT COUNT(*) FROM posts WHERE "+where, arguments...).Scan(&result.Total); err != nil {
		return result, err
	}
	arguments = append(arguments, filter.Limit, (filter.Page-1)*filter.Limit)
	rows, err := transaction.Query("SELECT "+postColumns+" FROM posts WHERE "+where+" ORDER BY date DESC,id LIMIT ? OFFSET ?", arguments...)
	if err != nil {
		return result, err
	}
	defer rows.Close()
	for rows.Next() {
		item, scanErr := scanPost(rows)
		if scanErr != nil {
			return result, scanErr
		}
		if !filter.Admin {
			item.Markdown = ""
		}
		result.Posts = append(result.Posts, item)
	}
	if err = rows.Err(); err != nil {
		return result, err
	}
	result.HasMore = filter.Page*filter.Limit < result.Total
	return result, transaction.Commit()
}

func (app *server) readPostBySlug(slug string) (post, error) {
	return scanPost(app.db.QueryRow("SELECT "+postColumns+" FROM posts WHERE slug=? AND status='published'", slug))
}

func (app *server) postBySlug(writer http.ResponseWriter, request *http.Request) {
	item, err := app.readPostBySlug(request.URL.Query().Get("slug"))
	app.respondPost(writer, item, err)
}

func requestedPostFilter(request *http.Request) postFilter {
	page, _ := strconv.Atoi(request.URL.Query().Get("page"))
	limit, _ := strconv.Atoi(request.URL.Query().Get("limit"))
	if limit <= 0 {
		limit = 50
	}
	return postFilter{Page: page, Limit: limit, Query: strings.TrimSpace(request.URL.Query().Get("q")), Category: request.URL.Query().Get("category"), Tag: request.URL.Query().Get("tag")}
}

func validPostSlug(slug string) bool {
	if slug == "" || len(slug) > 200 || strings.Trim(slug, "/") != slug {
		return false
	}
	for _, segment := range strings.Split(slug, "/") {
		if segment == "" || segment == "." || segment == ".." {
			return false
		}
		for _, character := range segment {
			if !unicode.IsLetter(character) && !unicode.IsDigit(character) && character != '-' && character != '_' {
				return false
			}
		}
	}
	return true
}

func postPath(slug string) string { return "/posts/" + slug + "/" }
