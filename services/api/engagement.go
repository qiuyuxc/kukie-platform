package main

import (
	"database/sql"
	"net/http"
	"regexp"
	"time"
)

type engagement struct {
	Views      int64 `json:"views"`
	Likes      int64 `json:"likes"`
	Bookmarks  int64 `json:"bookmarks"`
	Liked      bool  `json:"liked"`
	Bookmarked bool  `json:"bookmarked"`
}

var readerIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{16,128}$`)

func (app *server) readEngagement(writer http.ResponseWriter, id, userID string) {
	writer.Header().Set("Cache-Control", "no-store")
	var result engagement
	err := app.db.QueryRow(`SELECT
		(SELECT COUNT(*) FROM post_views WHERE post_id=posts.id),
		(SELECT COUNT(*) FROM likes WHERE post_id=posts.id),
		(SELECT COUNT(*) FROM bookmarks WHERE post_id=posts.id),
		EXISTS(SELECT 1 FROM likes WHERE post_id=posts.id AND user_id=?),
		EXISTS(SELECT 1 FROM bookmarks WHERE post_id=posts.id AND user_id=?)
		FROM posts WHERE id=? AND status='published'`, userID, userID, id).Scan(&result.Views, &result.Likes, &result.Bookmarks, &result.Liked, &result.Bookmarked)
	if err == sql.ErrNoRows {
		notFound(writer)
		return
	}
	if databaseFailure(writer, err) {
		return
	}
	respond(writer, 200, result)
}

func (app *server) postEngagement(writer http.ResponseWriter, request *http.Request) {
	_, cookieError := request.Cookie("kukie_session")
	if request.Header.Get("Authorization") != "" || cookieError == nil {
		app.auth(app.engagementForReader, false)(writer, request)
		return
	}
	app.engagementForReader(writer, request, identity{})
}

func (app *server) engagementForReader(writer http.ResponseWriter, request *http.Request, who identity) {
	id := request.PathValue("id")
	if request.Method == http.MethodPost {
		if app.limited(writer, "post-view:"+clientIP(request), 120, time.Minute) {
			return
		}
		var input struct {
			ReaderID string `json:"reader_id"`
		}
		if !readJSON(writer, request, &input) {
			return
		}
		visitor := "user:" + who.User.ID
		if who.User.ID == "" {
			if !readerIDPattern.MatchString(input.ReaderID) {
				fail(writer, 400, "invalid_reader", "阅读标识无效，请重新打开文章")
				return
			}
			visitor = "guest:" + input.ReaderID
		}
		_, err := app.db.Exec("INSERT OR IGNORE INTO post_views(post_id,visitor,day) SELECT id,?,? FROM posts WHERE id=? AND status='published'", app.keyed("view:"+visitor), time.Now().UTC().Format("2006-01-02"), id)
		if databaseFailure(writer, err) {
			return
		}
	}
	app.readEngagement(writer, id, who.User.ID)
}

func (app *server) likePost(writer http.ResponseWriter, request *http.Request, who identity) {
	id := request.PathValue("id")
	var exists bool
	if databaseFailure(writer, app.db.QueryRow("SELECT EXISTS(SELECT 1 FROM posts WHERE id=? AND status='published')", id).Scan(&exists)) {
		return
	}
	if !exists {
		notFound(writer)
		return
	}
	var err error
	if request.Method == http.MethodDelete {
		_, err = app.db.Exec("DELETE FROM likes WHERE user_id=? AND post_id=?", who.User.ID, id)
	} else {
		_, err = app.db.Exec("INSERT OR IGNORE INTO likes(user_id,post_id) VALUES(?,?)", who.User.ID, id)
	}
	if databaseFailure(writer, err) {
		return
	}
	app.readEngagement(writer, id, who.User.ID)
}
