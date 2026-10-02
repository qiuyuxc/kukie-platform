# Kukie

自托管博客与内容工作台。Go 提供页面渲染和 API，SQLite 保存文章、账号与评论，Vue 提供管理后台。

- 发布、编辑、撤回与删除会立即更新网站，无需重新构建。
- 保留现有主题布局、暗色模式、搜索、归档、标签、图片与目录交互。
- 网站使用自有评论，支持邮箱注册、登录、可选两步验证和后台删除。
- 正文、分页与已有评论由服务端渲染，禁用 JavaScript 也能阅读。
- 镜像包含网站、后台和服务端，持久化数据独立存放。

## 部署

推荐使用 Docker Compose，也可以直接在 1Panel 等面板里导入编排。完整步骤见 [Docker 部署](docs/docker.md)，包括面板导入、国内镜像源、初始化、备份、升级和回滚。国内机器先跑 `deploy/detect-region.sh` 可自动选用更快的镜像源。

镜像：`ghcr.io/qiuyuxc/kukie-platform`，仅支持 `linux/amd64`（x86_64）。镜像不绑定站点域名，网站和后台默认同源调用各自的 API，无需配置 API 地址或来源白名单。完整 API 入口保留给原生 App，通过 HTTPS 反向代理对外提供。

`KUKIE_SITE_URL` 是可选的运行时公开地址，用于固定规范链接及 IndexNow；留空时页面链接跟随访问地址。换域名不需要重新构建镜像。HTTPS 反代保留 `KUKIE_SECURE_COOKIE=1`，HTTP 直连改为 `0`。

GitHub Actions 在主分支推送后执行测试、镜像运行检查并发布镜像。构建不会自动重启生产服务器，部署者自行选择升级时间。

## 本地开发

需要 Node.js 22.12+、Go 1.26+、C 编译器，以及 Hugo Extended 0.167.0。Hugo 只用于生成主题资源与静态附属页，不参与线上文章发布；运行容器不包含 Hugo 或 Node.js。

```sh
export KUKIE_SECURE_COOKIE=0
npm run build
npm start
```

网站位于 `127.0.0.1:8086`，后台位于 `127.0.0.1:8085`。本地数据保存在 `.local/kukie/`。首次在后台创建管理员前，从本地读取 `.local/kukie/setup-token`；没有默认账号或默认密码。

```sh
npm test
```

二进制部署与打包见 [服务端部署](docs/go-site.md)。

## 目录

| 路径 | 内容 |
| --- | --- |
| `services/api/` | 服务端、数据模型和测试 |
| `apps/console/` | 管理后台 |
| `apps/site/` | 动态页面模板与增量样式 |
| `themes/`、`assets/`、`layouts/` | 构建期主题资源 |
| `content/`、`static/`、`data/` | 初始内容及静态资源 |
| `deploy/` | 容器与 HTTPS 配置 |
| `scripts/` | 构建、打包和镜像验证 |

## 数据边界

初次启动时导入 `content/posts/`，之后数据库是文章内容的唯一来源。重新启动或升级镜像不会覆盖后台编辑，也不会重新导入已删除文章。新增文章应在后台创建。

站点附属页、主题布局与导航仍来自构建期资源，修改这些内容需重新构建镜像。既有第三方评论不会自动迁入，自有评论从接入后开始保存。
