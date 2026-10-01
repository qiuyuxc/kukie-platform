package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"html/template"
	"io"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"

	"golang.org/x/net/html"
)

type siteManifest struct {
	Version      int           `json:"version"`
	Title        string        `json:"title"`
	Description  string        `json:"description"`
	BaseURL      string        `json:"base_url"`
	Author       string        `json:"author"`
	Avatar       string        `json:"avatar"`
	AuthorAvatar template.HTML `json:"author_avatar"`
	Signature    template.HTML `json:"signature"`
	Sponsor      template.HTML `json:"sponsor"`
	License      struct {
		Enable         bool
		Type, Language string
	} `json:"license"`
	DefaultCover string            `json:"default_cover"`
	Head         string            `json:"head"`
	PostHead     string            `json:"post_head"`
	Navbar       template.HTML     `json:"navbar"`
	NavbarAttrs  template.HTMLAttr `json:"navbar_attrs"`
	Hero         template.HTML     `json:"hero"`
	Footer       template.HTML     `json:"footer"`
	Tail         template.HTML     `json:"tail"`
	StaticRoutes []string          `json:"static_routes"`
	Assets       []string          `json:"assets"`
	Aliases      []struct {
		Path string `json:"path"`
		Slug string `json:"slug"`
	} `json:"aliases"`
	Social []struct{ Name, Link, Icon string } `json:"social"`
}

type blogSite struct {
	manifest           siteManifest
	templates          *template.Template
	root               *os.Root
	assets             map[string]bool
	static             map[string]string
	homeHead, postHead template.HTML
	indexNowKey        string
	indexNowEndpoint   string
}

func siteOrigin(value string) (string, error) {
	parsed, err := url.Parse(value)
	if err != nil || (parsed.Scheme != "https" && parsed.Scheme != "http") || parsed.Host == "" || parsed.User != nil || (parsed.Path != "" && parsed.Path != "/") || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", errors.New("site URL must be an HTTP(S) origin")
	}
	return parsed.Scheme + "://" + parsed.Host, nil
}

func (app *server) loadSite(directory, baseURL string) error {
	data, err := os.ReadFile(filepath.Join(directory, "_server", "site.json"))
	if err != nil {
		return err
	}
	site := &blogSite{assets: map[string]bool{}, static: map[string]string{}}
	if err = json.Unmarshal(data, &site.manifest); err != nil {
		return err
	}
	if site.manifest.Version != 1 || site.manifest.Title == "" || site.manifest.Head == "" {
		return errors.New("invalid site manifest")
	}
	origin, err := siteOrigin(site.manifest.BaseURL)
	if err != nil {
		return err
	}
	if baseURL != "" {
		configured, originErr := siteOrigin(baseURL)
		if originErr != nil {
			return originErr
		}
		if configured != origin {
			return errors.New("KUKIE_SITE_URL differs from the bundle; rebuild for this origin")
		}
	}
	site.manifest.BaseURL = origin
	site.homeHead, site.postHead = metadataFreeHead(site.manifest.Head), metadataFreeHead(site.manifest.PostHead)
	functions := template.FuncMap{"postPath": postPath, "queryEscape": url.QueryEscape, "cover": func(value string) string {
		if value != "" && safeImageURL(value) {
			return value
		}
		return site.manifest.DefaultCover
	}, "odd": func(index int) bool { return index%2 != 0 }, "json": func(value any) template.JS { data, _ := json.Marshal(value); return template.JS(data) }}
	site.templates, err = template.New("base.html").Funcs(functions).ParseGlob(filepath.Join(directory, "_server", "templates", "*.html"))
	if err != nil {
		return err
	}
	site.root, err = os.OpenRoot(filepath.Join(directory, "public"))
	if err != nil {
		return err
	}
	success := false
	defer func() {
		if !success {
			site.root.Close()
		}
	}()
	for _, asset := range site.manifest.Assets {
		if validSitePath(asset) {
			site.assets[asset] = true
		}
	}
	for _, route := range site.manifest.StaticRoutes {
		if !validSitePath(route) {
			return errors.New("invalid static route")
		}
		file := strings.TrimPrefix(route, "/")
		if strings.HasSuffix(route, "/") {
			file += "index.html"
		}
		site.static[route] = file
	}
	transaction, err := app.db.Begin()
	if err != nil {
		return err
	}
	defer transaction.Rollback()
	for _, alias := range site.manifest.Aliases {
		if !validSitePath(alias.Path) {
			return errors.New("invalid legacy alias")
		}
		if _, err = transaction.Exec("INSERT OR IGNORE INTO post_aliases(path,post_id) SELECT ?,id FROM posts WHERE slug=?", alias.Path, alias.Slug); err != nil {
			return err
		}
	}
	if err = transaction.Commit(); err != nil {
		return err
	}
	app.site = site
	app.origins[origin] = true
	success = true
	return nil
}

func validSitePath(value string) bool {
	if !strings.HasPrefix(value, "/") || strings.HasPrefix(value, "//") || strings.ContainsAny(value, "\\?#%\r\n\x00") {
		return false
	}
	for _, part := range strings.Split(strings.Trim(value, "/"), "/") {
		if part == "" || strings.HasPrefix(part, ".") || part == "_server" {
			return false
		}
	}
	return path.Clean(value) == strings.TrimSuffix(value, "/")
}

func metadataFreeHead(source string) template.HTML {
	parser := html.NewTokenizer(strings.NewReader(source))
	var output bytes.Buffer
	skip := ""
	for {
		kind := parser.Next()
		if kind == html.ErrorToken {
			if parser.Err() == io.EOF {
				break
			}
			return ""
		}
		raw := string(parser.Raw())
		if kind == html.StartTagToken || kind == html.SelfClosingTagToken {
			token := parser.Token()
			attributes := map[string]string{}
			for _, attribute := range token.Attr {
				attributes[attribute.Key] = strings.ToLower(attribute.Val)
			}
			if token.Data == "title" || (token.Data == "script" && attributes["type"] == "application/ld+json") {
				skip = token.Data
				continue
			}
			if token.Data == "meta" && (attributes["name"] == "description" || strings.HasPrefix(attributes["name"], "twitter:") || strings.HasPrefix(attributes["property"], "og:") || strings.HasPrefix(attributes["property"], "article:")) {
				continue
			}
			if token.Data == "link" && (attributes["rel"] == "canonical" || attributes["rel"] == "alternate" || attributes["rel"] == "sitemap") {
				continue
			}
		}
		if skip != "" {
			if kind == html.EndTagToken && parser.Token().Data == skip {
				skip = ""
			}
			continue
		}
		output.WriteString(raw)
	}
	return template.HTML(output.String())
}
