package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/minio/minio-go/v7"
)

func (app *server) removeStoredImage(ctx context.Context, config storageSettings, path string) error {
	if path == "" || path == "." || path == ".." || filepath.Base(path) != path || strings.Contains(path, "\\") {
		return errors.New("invalid media path")
	}
	switch config.Active {
	case "local":
		err := os.Remove(filepath.Join(app.directory, "uploads", path))
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	case "s3":
		client, err := app.s3Client(config.S3)
		if err != nil {
			return err
		}
		return client.RemoveObject(ctx, config.S3.Bucket, path, minio.RemoveObjectOptions{})
	case "webdav":
		response, err := app.webdav(ctx, config.WebDAV, "DELETE", path, nil, "")
		if err != nil {
			return err
		}
		defer response.Body.Close()
		if response.StatusCode != 200 && response.StatusCode != 204 && response.StatusCode != 404 {
			return fmt.Errorf("media deletion status %d", response.StatusCode)
		}
		return nil
	default:
		return errors.New("unknown storage provider")
	}
}

func (app *server) cleanupMedia(ctx context.Context, now time.Time) error {
	rows, err := app.db.Query("SELECT id,path,config FROM media_cleanup WHERE next_at<=? ORDER BY next_at,id LIMIT 25", now.Unix())
	if err != nil {
		return err
	}
	type pendingMedia struct{ id, path, config string }
	var pending []pendingMedia
	for rows.Next() {
		var item pendingMedia
		if err = rows.Scan(&item.id, &item.path, &item.config); err != nil {
			break
		}
		pending = append(pending, item)
	}
	rowError := rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	if rowError != nil {
		return rowError
	}
	var failure error
	for _, item := range pending {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		var config storageSettings
		var data []byte
		data, err = app.unseal(item.config)
		if err == nil {
			err = json.Unmarshal(data, &config)
		}
		if err == nil {
			operation, cancel := context.WithTimeout(ctx, 20*time.Second)
			err = app.removeStoredImage(operation, config, item.path)
			cancel()
		}
		if err != nil {
			failure = err
			if _, updateError := app.db.Exec("UPDATE media_cleanup SET next_at=? WHERE id=?", now.Add(time.Hour).Unix(), item.id); updateError != nil {
				return updateError
			}
		} else if _, err = app.db.Exec("DELETE FROM media_cleanup WHERE id=?", item.id); err != nil {
			return err
		}
	}
	return failure
}
