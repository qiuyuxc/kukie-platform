package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/jpeg"
	"image/png"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	_ "golang.org/x/image/webp"
)

var imageWork = make(chan struct{}, 2)

func (app *server) endpoint(raw string) (*url.URL, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Hostname() == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || (parsed.Scheme != "https" && !(app.allowPrivateStorage && parsed.Scheme == "http")) {
		return nil, errors.New("存储端点必须为 HTTPS，不能带凭据、查询参数或片段；内网 HTTP 需服务器显式启用")
	}
	return parsed, nil
}
func (app *server) allowedIP(address netip.Addr) bool {
	address = address.Unmap()
	if address.IsLinkLocalUnicast() || address.IsLinkLocalMulticast() || address.IsUnspecified() || address.IsMulticast() {
		return false
	}
	if address.IsLoopback() || address.IsPrivate() {
		return app.allowPrivateStorage
	}
	return address.IsGlobalUnicast()
}
func (app *server) storageTransport() *http.Transport {
	return &http.Transport{TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12}, ResponseHeaderTimeout: 15 * time.Second, IdleConnTimeout: 30 * time.Second, MaxIdleConns: 10,
		DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(address)
			if err != nil {
				return nil, err
			}
			addresses, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
			if err != nil {
				return nil, err
			}
			if len(addresses) == 0 {
				return nil, errors.New("storage host has no addresses")
			}
			for _, resolved := range addresses {
				if !app.allowedIP(resolved) {
					return nil, errors.New("storage destination denied by network policy")
				}
			}
			return (&net.Dialer{Timeout: 10 * time.Second}).DialContext(ctx, network, net.JoinHostPort(addresses[0].String(), port))
		},
	}
}
func (app *server) s3Client(config s3Settings) (*minio.Client, error) {
	endpoint, err := app.endpoint(config.Endpoint)
	if err != nil {
		return nil, err
	}
	if endpoint.Path != "" && endpoint.Path != "/" {
		return nil, errors.New("S3 端点不能包含路径")
	}
	lookup := minio.BucketLookupAuto
	if config.PathStyle {
		lookup = minio.BucketLookupPath
	}
	return minio.New(endpoint.Host, &minio.Options{Creds: credentials.NewStaticV4(config.AccessKey, config.SecretKey, ""), Secure: endpoint.Scheme == "https", Region: config.Region, BucketLookup: lookup, Transport: app.storageTransport()})
}
func (app *server) webdav(ctx context.Context, config webdavSettings, method, key string, data io.Reader, mimeType string) (*http.Response, error) {
	endpoint, err := app.endpoint(config.URL)
	if err != nil {
		return nil, err
	}
	endpoint.Path = strings.TrimRight(endpoint.Path, "/") + "/" + key
	request, err := http.NewRequestWithContext(ctx, method, endpoint.String(), data)
	if err != nil {
		return nil, err
	}
	request.SetBasicAuth(config.Username, config.Password)
	if mimeType != "" {
		request.Header.Set("Content-Type", mimeType)
	}
	request.Header.Set("Depth", "0")
	client := &http.Client{Transport: app.storageTransport(), Timeout: 30 * time.Second, CheckRedirect: func(request *http.Request, via []*http.Request) error { return http.ErrUseLastResponse }}
	return client.Do(request)
}
func (app *server) saveStorage(writer http.ResponseWriter, request *http.Request, who identity) {
	var input storageSettings
	if !readJSON(writer, request, &input) {
		return
	}
	previous, err := app.storageConfig()
	if databaseFailure(writer, err) {
		return
	}
	if input.S3.SecretKey == "" {
		input.S3.SecretKey = previous.S3.SecretKey
	}
	if input.WebDAV.Password == "" {
		input.WebDAV.Password = previous.WebDAV.Password
	}
	input.S3.HasSecret = false
	input.WebDAV.HasPassword = false
	switch input.Active {
	case "local":
	case "s3":
		if _, err = app.s3Client(input.S3); err != nil || input.S3.Bucket == "" || input.S3.AccessKey == "" || input.S3.SecretKey == "" {
			fail(writer, 400, "invalid_storage", "请填写有效的 S3 端点、桶名称、Access Key 和 Secret Key")
			return
		}
	case "webdav":
		if _, err = app.endpoint(input.WebDAV.URL); err != nil || input.WebDAV.Username == "" || input.WebDAV.Password == "" {
			fail(writer, 400, "invalid_storage", "请填写有效的 WebDAV 目录 URL、用户名和密码")
			return
		}
	default:
		fail(writer, 400, "invalid_storage", "请选择本地、S3 或 WebDAV")
		return
	}
	if databaseFailure(writer, app.storeSetting("storage", input)) {
		return
	}
	app.audit(who, "settings.storage", input.Active)
	respond(writer, 200, map[string]bool{"ok": true})
}
func (app *server) testStorage(writer http.ResponseWriter, request *http.Request, who identity) {
	if app.limited(writer, "storage-test:"+who.User.ID, 5, time.Minute) {
		return
	}
	config, err := app.storageConfig()
	if databaseFailure(writer, err) {
		return
	}
	ctx, cancel := context.WithTimeout(request.Context(), 20*time.Second)
	defer cancel()
	switch config.Active {
	case "local":
		err = os.MkdirAll(filepath.Join(app.directory, "uploads"), 0700)
	case "s3":
		var client *minio.Client
		client, err = app.s3Client(config.S3)
		if err == nil {
			var exists bool
			exists, err = client.BucketExists(ctx, config.S3.Bucket)
			if err == nil && !exists {
				err = errors.New("bucket missing")
			}
		}
	case "webdav":
		var response *http.Response
		response, err = app.webdav(ctx, config.WebDAV, "PROPFIND", "", nil, "")
		if err == nil {
			defer response.Body.Close()
			if response.StatusCode != 207 && response.StatusCode != 200 {
				err = fmt.Errorf("WebDAV status %d", response.StatusCode)
			}
		}
	}
	if err != nil {
		fail(writer, 502, "storage_failed", "存储连接未通过，请核对地址、凭据、目录及服务器网络策略")
		return
	}
	respond(writer, 200, map[string]string{"message": "连接与目录读取检查通过；写入权限将在实际上传时验证"})
}
func (app *server) putImage(ctx context.Context, config storageSettings, key, mimeType string, data []byte) error {
	switch config.Active {
	case "local":
		directory := filepath.Join(app.directory, "uploads")
		if err := os.MkdirAll(directory, 0700); err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(directory, key), data, 0600)
	case "s3":
		client, err := app.s3Client(config.S3)
		if err != nil {
			return err
		}
		_, err = client.PutObject(ctx, config.S3.Bucket, "kukie/"+key, bytes.NewReader(data), int64(len(data)), minio.PutObjectOptions{ContentType: mimeType, DisableMultipart: true})
		return err
	case "webdav":
		response, err := app.webdav(ctx, config.WebDAV, "PUT", key, bytes.NewReader(data), mimeType)
		if err != nil {
			return err
		}
		defer response.Body.Close()
		if response.StatusCode != 200 && response.StatusCode != 201 && response.StatusCode != 204 {
			return fmt.Errorf("WebDAV upload status %d", response.StatusCode)
		}
		return nil
	}
	return errors.New("unknown storage")
}
func (app *server) openImage(ctx context.Context, config storageSettings, key string) (io.ReadCloser, error) {
	switch config.Active {
	case "local":
		return os.Open(filepath.Join(app.directory, "uploads", filepath.Base(key)))
	case "s3":
		client, err := app.s3Client(config.S3)
		if err != nil {
			return nil, err
		}
		object, err := client.GetObject(ctx, config.S3.Bucket, "kukie/"+key, minio.GetObjectOptions{})
		if err != nil {
			return nil, err
		}
		if _, err = object.Stat(); err != nil {
			object.Close()
			return nil, err
		}
		return object, nil
	case "webdav":
		response, err := app.webdav(ctx, config.WebDAV, "GET", key, nil, "")
		if err != nil {
			return nil, err
		}
		if response.StatusCode != 200 {
			response.Body.Close()
			return nil, errors.New("WebDAV read failed")
		}
		return response.Body, nil
	}
	return nil, errors.New("unknown storage")
}
func (app *server) upload(writer http.ResponseWriter, request *http.Request, who identity, avatar bool) {
	select {
	case imageWork <- struct{}{}:
		defer func() { <-imageWork }()
	case <-request.Context().Done():
		return
	}
	if app.limited(writer, "upload:"+who.User.ID, 15, time.Minute) {
		return
	}
	request.Body = http.MaxBytesReader(writer, request.Body, 6<<20)
	if err := request.ParseMultipartForm(6 << 20); err != nil {
		fail(writer, 400, "invalid_upload", "请选择 5 MB 内的图片")
		return
	}
	if request.MultipartForm != nil {
		defer request.MultipartForm.RemoveAll()
	}
	file, header, err := request.FormFile("file")
	if err != nil {
		fail(writer, 400, "invalid_upload", "缺少图片文件")
		return
	}
	defer file.Close()
	if header.Size > 5<<20 {
		fail(writer, 400, "invalid_upload", "图片不能超过 5 MB")
		return
	}
	data, err := io.ReadAll(io.LimitReader(file, 5<<20+1))
	if err != nil || len(data) > 5<<20 {
		fail(writer, 400, "invalid_upload", "图片读取失败或过大")
		return
	}
	dimensions, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || dimensions.Width < 1 || dimensions.Height < 1 || dimensions.Width > 8000 || dimensions.Height > 8000 || dimensions.Width*dimensions.Height > 16000000 || (format != "jpeg" && format != "png" && format != "webp") {
		fail(writer, 400, "invalid_image", "仅支持有效 JPG、PNG、WebP，最多 1600 万像素")
		return
	}
	decoded, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		fail(writer, 400, "invalid_image", "无法解码图片")
		return
	}
	if avatar {
		side := min(dimensions.Width, dimensions.Height)
		offsetX := (dimensions.Width - side) / 2
		offsetY := (dimensions.Height - side) / 2
		thumb := image.NewRGBA(image.Rect(0, 0, 256, 256))
		for row := 0; row < 256; row++ {
			for column := 0; column < 256; column++ {
				thumb.Set(column, row, decoded.At(offsetX+column*side/256, offsetY+row*side/256))
			}
		}
		decoded = thumb
		format = "jpeg"
	}
	var output bytes.Buffer
	extension := ".png"
	mimeType := "image/png"
	if format == "jpeg" {
		err = jpeg.Encode(&output, decoded, &jpeg.Options{Quality: 88})
		extension = ".jpg"
		mimeType = "image/jpeg"
	} else {
		err = png.Encode(&output, decoded)
	}
	if err != nil || output.Len() > 10<<20 {
		fail(writer, 400, "image_too_large", "图片编码后过大，请缩小后再试")
		return
	}
	config, err := app.storageConfig()
	if databaseFailure(writer, err) {
		return
	}
	id := randomToken()
	path := id + extension
	ctx, cancel := context.WithTimeout(request.Context(), 35*time.Second)
	defer cancel()
	if err = app.putImage(ctx, config, path, mimeType, output.Bytes()); err != nil {
		fail(writer, 502, "upload_failed", "存储未接受图片，请联系管理员检查连接与写入权限")
		return
	}
	snapshot, _ := json.Marshal(config)
	sealed, err := app.seal(snapshot)
	if databaseFailure(writer, err) {
		return
	}
	_, err = app.db.Exec("INSERT INTO media(id,path,config,mime,size,owner_id,created_at) VALUES(?,?,?,?,?,?,?)", id, path, sealed, mimeType, output.Len(), who.User.ID, time.Now().UTC().Format(time.RFC3339))
	if databaseFailure(writer, err) {
		return
	}
	address := "/media/" + id
	if avatar {
		_, err = app.db.Exec("UPDATE users SET avatar=? WHERE id=?", address, who.User.ID)
		if databaseFailure(writer, err) {
			return
		}
	}
	respond(writer, 201, map[string]string{"url": address})
}
func (app *server) uploadAvatar(writer http.ResponseWriter, request *http.Request, who identity) {
	app.upload(writer, request, who, true)
}
func (app *server) uploadMedia(writer http.ResponseWriter, request *http.Request, who identity) {
	app.upload(writer, request, who, false)
}
func (app *server) serveMedia(writer http.ResponseWriter, request *http.Request) {
	var path, sealed, mimeType string
	var size int64
	if app.db.QueryRow("SELECT path,config,mime,size FROM media WHERE id=?", request.PathValue("id")).Scan(&path, &sealed, &mimeType, &size) != nil {
		notFound(writer)
		return
	}
	data, err := app.unseal(sealed)
	if databaseFailure(writer, err) {
		return
	}
	var config storageSettings
	if databaseFailure(writer, json.Unmarshal(data, &config)) {
		return
	}
	ctx, cancel := context.WithTimeout(request.Context(), 30*time.Second)
	defer cancel()
	reader, err := app.openImage(ctx, config, path)
	if err != nil {
		fail(writer, 502, "media_unavailable", "图片暂时不可用")
		return
	}
	defer reader.Close()
	writer.Header().Set("Content-Type", mimeType)
	writer.Header().Set("Cache-Control", "public, max-age=3600")
	writer.Header().Set("Content-Security-Policy", "default-src 'none'")
	writer.Header().Set("Content-Length", fmt.Sprint(size))
	_, _ = io.Copy(writer, io.LimitReader(reader, size))
}
