package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"time"
)

func (app *server) configureIndexNow() error {
	key := os.Getenv("KUKIE_INDEXNOW_KEY")
	if key == "" {
		return nil
	}
	if app.site == nil {
		return errors.New("IndexNow requires an enabled site")
	}
	if !regexp.MustCompile(`^[a-zA-Z0-9-]{8,128}$`).MatchString(key) {
		return errors.New("invalid IndexNow key")
	}
	base, _ := url.Parse(app.site.manifest.BaseURL)
	if base.Scheme != "https" {
		return errors.New("enable IndexNow only on the verified public HTTPS origin")
	}
	app.site.indexNowKey, app.site.indexNowEndpoint = key, "https://api.indexnow.org/indexnow"
	return nil
}

func (app *server) processIndexNow(ctx context.Context, now time.Time) error {
	if app.site == nil || app.site.indexNowKey == "" {
		return nil
	}
	rows, err := app.db.QueryContext(ctx, "SELECT path,version,attempts FROM indexnow_queue WHERE next_at<=? ORDER BY next_at,path LIMIT 100", now.Unix())
	if err != nil {
		return err
	}
	type pendingURL struct {
		Path              string
		Version, Attempts int
	}
	pending := []pendingURL{}
	urls := []string{}
	for rows.Next() {
		var item pendingURL
		if err = rows.Scan(&item.Path, &item.Version, &item.Attempts); err != nil {
			rows.Close()
			return err
		}
		pending = append(pending, item)
		urls = append(urls, app.site.absolute(item.Path))
	}
	err = rows.Err()
	rows.Close()
	if err != nil || len(pending) == 0 {
		return err
	}
	base, _ := url.Parse(app.site.manifest.BaseURL)
	payload, err := json.Marshal(map[string]any{"host": base.Hostname(), "key": app.site.indexNowKey, "keyLocation": app.site.absolute("/" + app.site.indexNowKey + ".txt"), "urlList": urls})
	if err != nil {
		return err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, app.site.indexNowEndpoint, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json; charset=utf-8")
	client := &http.Client{Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, sendErr := client.Do(request)
	if sendErr == nil {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
		response.Body.Close()
		if response.StatusCode != 200 && response.StatusCode != 202 {
			sendErr = fmt.Errorf("IndexNow returned HTTP %d", response.StatusCode)
		}
	}
	for _, item := range pending {
		if sendErr == nil {
			_, err = app.db.ExecContext(ctx, "DELETE FROM indexnow_queue WHERE path=? AND version=?", item.Path, item.Version)
		} else {
			delay := min(24*time.Hour, time.Minute*time.Duration(1<<min(item.Attempts, 11)))
			_, err = app.db.ExecContext(ctx, "UPDATE indexnow_queue SET attempts=attempts+1,next_at=? WHERE path=? AND version=?", now.Add(delay).Unix(), item.Path, item.Version)
		}
		if err != nil {
			return err
		}
	}
	return sendErr
}
