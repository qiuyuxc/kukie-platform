---
title: 告别手动调 API：我写了一个 Cloudflare Tunnel 可视化管理面板
date: 2026-07-11T00:00:00+08:00
description: 通过可视化面板管理 Cloudflare Tunnel，自动完成域名绑定、Ingress 配置与 SaaS 接入。
cover: /img/covers/tunnel-manager.webp
aliases:
  - /p/tunnel-manager/
categories:
    - CDN
tags:
    - CDN
    - Cloudflare
    - Tunnel
    - SaaS
---

> 本文由 AI 协助撰写与更新，全部事实已对照 Tunnel Manager v2.2.3 的源码与文档核对；如发现疏漏，欢迎在评论区或仓库 Issue 指出。

## 前言

使用 Cloudflare Tunnel 并不困难，真正容易出错的是后续的域名发布流程。

当一个服务需要同时用到 Cloudflare Tunnel、DNS CNAME、Cloudflare for SaaS Custom Hostnames 和优选 CNAME 时，每新增一个域名都要在多个页面之间来回切换：先改 Tunnel 的 ingress 规则，再建 DNS 记录，然后配 Custom Hostname，必要时还要设置 fallback origin。

步骤一多，问题就随之出现：域名填错、代理状态选错、回源地址不一致、catch-all 路由位置被破坏，或者只完成了一半配置却没及时发现。任何一步遗漏，最终表现都可能只是「HTTPS 打不开」，排查时却要逐项检查 Tunnel、DNS、证书和 SaaS 配置。

Tunnel Manager 把这套重复操作组合成一个可复用的自动化流程。它是一个自托管的 Cloudflare Tunnel 管理面板，通过 Web UI 或 Telegram Bot 完成隧道选择、路由管理、域名绑定、DNS 配置和 SaaS 回源设置。

> 本文对应 **v2.2.3**。项目在 v2.0.0 之后有几处结构性变化：存储从 JSON 文件迁到 SQLite、加入多用户与用户组权限、Cloudflare 凭据改以 OAuth 为主、Telegram Bot 从单一全局 Bot 变成每人一个。读过早期版本的话，这些地方都已经不一样了。
>
> 逐项配置说明见文档站 [docs.kukie.cn](https://docs.kukie.cn)（[English](https://docs.kukie.cn/en/)），本文侧重「为什么这样设计」。

## 技术原理：主域名、辅助域名和 Tunnel 如何协作

**Tunnel ingress** 决定某个 Hostname 的请求转发到哪个本地服务：

```yaml
ingress:
  - hostname: app.example.com
    service: http://localhost:3000
  - hostname: origin.example.net
    service: http://localhost:3000
  - service: http_status:404
```

最后一条不带 Hostname 的是兜底规则。面板更新配置时会先去掉当前域名已有的规则，再把新规则插到兜底之前——既避免重复绑定，也避免破坏 catch-all 的顺序。

**DNS CNAME** 负责把流量送进隧道：辅助域名用开启代理的 CNAME 指向 `<Tunnel-ID>.cfargotunnel.com`，主域名则指向优选 CNAME 并关闭代理。

**Custom Hostname** 把对外访问域名接入指定 Zone 并配置自定义回源。面板会查询同名记录，存在则更新、不存在则创建，证书走 HTTP DCV。

所以它实现的不是「加一条 DNS」，而是一整条链路：校验请求 → 读取当前用户的隧道与转发地址 → 查询两侧域名所属 Zone → 更新 ingress → 写入 DNS → 创建或更新 Custom Hostname。

### 两种绑定模式

不是每个站点都需要优选线路，所以绑定时按组选模式：

| 资源            | 简化直连                   | 优选模式                                 |
| --------------- | -------------------------- | ---------------------------------------- |
| 主域名 DNS      | 橙云 CNAME 直接指向 Tunnel | 灰云 CNAME 指向优选 CNAME                |
| 辅助域名 DNS    | 不需要                     | 橙云 CNAME 指向 Tunnel                   |
| Custom Hostname | 不创建                     | 主机名为主域名，Custom Origin 为辅助域名 |
| Tunnel ingress  | 只加主域名                 | 主域名与辅助域名都加                     |

简化直连只做两件事：加 ingress、建一条代理开启的 CNAME，少了一个需要维护的辅助主机名。

## 设计思路

```text
Vue 3 + TypeScript + Naive UI
              ↓ HTTP API
Go + chi router + SQLite
              ↓ REST API
Cloudflare Tunnels / DNS / Custom Hostnames
```

前后端构建进同一个镜像，Go 服务既提供 `/api` 也托管前端静态文件，部署只需一个容器，没有额外的 Node 运行时。

### 存储：从 JSON 文件到 SQLite

早期版本用一个带读写锁的 JSON 文件存全部状态。当数据只有「隧道 ID、转发地址、优选 CNAME、密码摘要、Bot 配置」这几项时这很合理：依赖少，备份就是复制一个文件。

引入多用户后这个模型不够用了——用户、用户组、会话、邀请码、监控项目、探测目标、告警记录之间有明确关系，还要按用户过滤查询。v2.0.0 因此迁到 SQLite（`modernc.org/sqlite`，纯 Go 驱动，不引入 CGO），并带版本化 schema 迁移。旧装升级无需手工操作：首次启动自动导入 `data/config.json`，原文件留作备份。

监控心跳例外，仍写在独立的 `heartbeats.json` 里定时刷盘——写入频繁、结构单一、丢一点不影响正确性，没必要每次都走数据库事务。

### 凭据：OAuth 优先，静态 Token 兼容

早期版本要求启动时必须提供 `CF_API_TOKEN` 和 `CF_ACCOUNT_ID`。现在默认路径是 OAuth 2.0 授权码流程（附加 S256 PKCE）：面板里点一次授权，令牌自动刷新，一个账号可以授权多个 Cloudflare 账户并随时切换。访问令牌与刷新令牌用 `APP_ENCRYPTION_KEY` 做 AES-GCM 加密后落盘，Client Secret 只留在服务端。静态 Token 仍可用，只是不会自动刷新。

有一点是刻意为之：**普通用户没有连接自己的授权时，不会回落使用管理员的静态凭据。** 早期版本会回落，导致未授权用户能看到管理员的隧道和 DNS，这在 v2.2.1 作为安全问题修掉了。

### 多用户与权限

面板不再是单管理员模型：支持邮箱注册（开放注册、邮箱验证码、邀请码「关闭 / 选填 / 必填」都可配），每个用户有自己的 Cloudflare 连接、隧道选择与监控项目且彼此不可见，权限按用户组授予（隧道、域名绑定、DNS、监控、Cloudflare 授权五项）。会话存在数据库里，有效期 12 小时，服务重启不会把所有人踢下线——这一点和早期版本相反，那时会话在进程内存里，重启即失效。

### 幂等更新与批量绑定

绑定同一个域名可以反复提交，用于修改转发地址而不必先手动删旧配置：后端会按最长后缀匹配 Zone、删除同名 ingress 再插入、DNS 与 Custom Hostname 都是「有则更新、无则创建」。

批量绑定允许每组独立设置模式、转发地址、优选 CNAME 与两侧域名，逐组返回成功或失败，一组失败不影响后续。但它不是事务：某组执行到中间失败，之前写入 Cloudflare 的步骤不会自动回滚。

### 服务监控与公开状态页

面板也顺手回答「发布出去的东西现在还活着吗」：HTTP（GET / POST）、TCP、ICMP 三种探测，一个监控可挂多个目标，间隔最短 30 秒，仪表盘展示近 24 小时延迟柱图。告警只在状态**变化**时发邮件——异常一封、恢复一封，持续异常不重复轰炸，每次通知都记录在告警列表里。

结果可以开成免登录状态页，访问方式有系统令牌、自定义短路径（`/status/your-name`）和独立自定义域名三种。自定义域名做了 Host 隔离：**通过它只能打开对应状态页、公开数据接口和页面所需静态资源，管理后台、登录接口、其他 API 和其他状态页都返回 404。** 把状态页分享出去，不会顺带暴露管理入口。

保存自定义域名时面板会自动配置 Cloudflare，并复制面板域名 ingress 的服务与源站参数——但会移除 `httpHostHeader`，因为状态页靠访客原始 `Host` 识别该显示哪个监控，固定回源 Host 会让请求匹配不到页面。自动配置失败不撤销已保存的设置，接口返回 `domain_warning`，界面同时给出 DNS、Custom Hostname、ingress 三组可复制的手动检查信息。

### Telegram Bot

REST API 和 Bot 调用同一个 DomainService，网页和 Telegram 走的是同一套配置流程，不会形成两份独立状态。Bot 的归属关系变了：现在**每个用户一个自己的 Bot**，只操作自己账号下的资源；管理员早期的全局 Bot 会在启动时自动迁移为个人 Bot。长轮询不需要公网地址，Webhook 更快但要求公网 HTTPS，验签密钥由系统生成且只存后端。Telegram API 端点是面板级配置，可以填自建反代——官方地址在部分网络下无法直连。

命令覆盖两类操作：绑定（`/直连域名`、`/优选绑定`，旧的 `/绑定域名` 仍兼容）和 DNS（`/DNS列表`、`/DNS详情`、`/DNS添加`、`/DNS修改`、`/DNS删除`）。删除需要二次确认：先拿一个五分钟有效的确认码，再用 `/确认删除` 提交。

### 安全模型

面板持有能改 Tunnel 和 DNS 的凭据，所以按运维系统而不是普通网站设计：密码用 Argon2id 哈希（历史 SHA-256 登录时自动迁移）；支持标准 TOTP 双因素，绑定完成时一次性展示 10 枚防重放恢复码；TOTP Secret、Cloudflare 令牌、SMTP 密码、Bot Token 都用 `APP_ENCRYPTION_KEY` 做 AES-GCM 加密；登录注册可选接入 Cloudflare Turnstile；普通接口接受会话或 `API_KEY`，但 **2FA 管理接口只接受会话**，不能用 API Key 绕过。

代价要说清楚：`APP_ENCRYPTION_KEY` 必须和数据目录一起备份。密钥丢了，已启用 2FA 的账户无法登录，OAuth 需要重新授权，而且没有自助找回通道——程序拒绝在拿不到正确密钥的情况下解绑 2FA，正是为了避免任何拿到数据文件的人都能静默关掉你的第二因素。

## 部署

两条路径：拉预编译镜像最省事，源码构建适合要改代码或想控制构建源的人。

### 方法一：拉预编译镜像（推荐）

不需要克隆仓库，一个 `docker-compose.yml` 加一个 `.env` 就能跑：

```yaml
services:
  tunnel-manager:
    image: ghcr.io/qiuyuxc/tunnel-manager:latest
    container_name: tunnel-manager
    restart: unless-stopped
    ports:
      - "8080:8080"
    environment:
      - CF_OAUTH_CLIENT_ID=${CF_OAUTH_CLIENT_ID}
      - CF_OAUTH_CLIENT_SECRET=${CF_OAUTH_CLIENT_SECRET}
      - CF_OAUTH_REDIRECT_URI=${CF_OAUTH_REDIRECT_URI}
      - CF_OAUTH_SCOPES=${CF_OAUTH_SCOPES}
      - CF_API_TOKEN=${CF_API_TOKEN}
      - CF_ACCOUNT_ID=${CF_ACCOUNT_ID}
      - API_KEY=${API_KEY}
      - ADMIN_PASSWORD=${ADMIN_PASSWORD}
      - APP_ENCRYPTION_KEY=${APP_ENCRYPTION_KEY}
    volumes:
      - ./data:/app/data
```

Compose 会自动读取同目录下的 `.env` 做变量替换，凭据写在那里、不要写进 yml 提交到仓库。

镜像带语义化版本标签，生产环境建议锁版本而不是跟 `latest`：

```text
ghcr.io/qiuyuxc/tunnel-manager:v2.2.3    # 锁到具体版本
ghcr.io/qiuyuxc/tunnel-manager:2.2       # 跟随 2.2.x 补丁
ghcr.io/qiuyuxc/tunnel-manager:sha-<短哈希>  # 回溯到任意提交
```

国内网络直接拉 ghcr.io 往往很慢，把镜像前缀换成高校镜像站是目前最快的路径，实测比官方源快得多：

```yaml
    image: ghcr.nju.edu.cn/qiuyuxc/tunnel-manager:latest
```

镜像站代理的是 ghcr.io 的仓库内容，标签与摘要都不变，切回官方源随时可以。

启动与更新：

```bash
docker compose up -d                      # 启动
docker compose pull && docker compose up -d   # 更新镜像并重启
```

### 方法二：源码构建

克隆仓库后执行安装脚本：

```bash
chmod +x install.sh
./install.sh
```

脚本会依次完成：检查 Docker、Compose 与 Git；询问用国内镜像源还是官方源；引导填写 Cloudflare OAuth 客户端或静态 Token；生成 `.env` 与随机 `APP_ENCRYPTION_KEY`；创建 `data` 持久化目录；构建镜像并启动。

安装过程中会问这几项，其中后两项可以留空：

```text
CF_OAUTH_CLIENT_ID / CF_OAUTH_CLIENT_SECRET / CF_OAUTH_REDIRECT_URI
  或 CF_API_TOKEN + CF_ACCOUNT_ID（静态凭据路径）
API_KEY：可选，供外部脚本调用
ADMIN_PASSWORD：可选，留空随机生成
```

也可以不用脚本，手动改成构建型 compose：

```yaml
services:
  tunnel-manager:
    build:
      context: .
      args:
        NPM_REGISTRY: ${NPM_REGISTRY:-https://registry.npmjs.org}
        GOPROXY: ${GOPROXY:-https://proxy.golang.org,direct}
        ALPINE_MIRROR: ${ALPINE_MIRROR:-https://dl-cdn.alpinelinux.org/alpine}
    container_name: tunnel-manager
    restart: unless-stopped
    ports:
      - "8080:8080"
    # environment 与 volumes 同方法一
```

那三个 `build.args` 只影响构建期的下载速度，不进入运行时。改了代码后重建：

```bash
DOCKER_BUILDKIT=1 docker compose build --no-cache
docker compose up -d
```

不想装 Docker 也有路走：每个版本的 Releases 都附了 linux-amd64 二进制包，解包后一个静态二进制加一份前端文件即可运行，配 systemd 常驻。步骤不在这里展开，见文档站的[二进制部署](https://docs.kukie.cn/guide/binary-deploy)。

### 环境变量

| 变量                       | 说明                                                           |
| -------------------------- | -------------------------------------------------------------- |
| `CF_OAUTH_CLIENT_ID`     | OAuth Client ID，也可以改在管理后台填                          |
| `CF_OAUTH_CLIENT_SECRET` | Client Secret，只留在服务端，不会下发到浏览器                  |
| `CF_OAUTH_REDIRECT_URI`  | 回调地址，必须与 Cloudflare 登记值完全一致；留空按请求地址推导 |
| `CF_OAUTH_SCOPES`        | 可选，空格分隔；留空使用 OAuth 客户端自身配置的 scopes         |
| `CF_API_TOKEN`           | 静态 Token 兼容路径，未连接 OAuth 时使用                       |
| `CF_ACCOUNT_ID`          | 静态 Token 对应的 Account ID                                   |
| `APP_ENCRYPTION_KEY`     | Base64 编码的 32 字节密钥；留空会自动生成并存入数据库          |
| `ADMIN_PASSWORD`         | 首次启动的管理员密码，留空随机生成                             |
| `API_KEY`                | 自动化调用用的 Key；不设置则 API Key 方式整体禁用              |
| `STORE_PATH`             | SQLite 数据库路径                                              |
| `PORT`                   | HTTP 端口，默认 `8080`                                       |
| `STATIC_DIR`             | 前端静态文件目录                                               |

创建 OAuth 客户端时有个容易漏的地方：必须勾上 Account Settings Read、Cloudflare Tunnel Edit、Zone Read、DNS Edit、SSL and Certificates Edit 五项权限，否则授权会拿到一个零权限令牌，任何 API 都调不通。回调地址填 `https://你的面板域名/api/cloudflare/oauth/callback`，反向代理场景记得传 `X-Forwarded-Proto` 与 `X-Forwarded-Host`。

### 启动后确认

```bash
docker compose ps
docker compose logs -f
```

首次启动的管理员密码打印在日志里（这一行是中文的）：

```bash
docker compose logs | grep 密
```

健康检查会返回当前版本，可以用它确认服务真的起来了：

```bash
curl http://127.0.0.1:8080/api/health
# {"status":"ok","version":"v2.2.3"}
```

### 数据持久化与备份

Compose 把宿主机 `./data` 映射到容器内 `/app/data`：

| 文件 / 目录         | 内容                                                                              |
| ------------------- | --------------------------------------------------------------------------------- |
| 数据库文件          | 用户与用户组、会话、隧道选择、绑定记录、监控配置、加密后的 OAuth 令牌与 Bot Token |
| `heartbeats.json` | 监控心跳历史，24 小时延迟图的数据源                                               |
| `uploads/`        | 状态页图片与站点图标                                                              |

镜像里的默认库名是 `data/config.json`——这是从 JSON 时代留下来的文件名，为了让老装升级后仍读到同一个文件，它现在的内容是 SQLite。二进制部署时默认叫 `data/tunnel-manager.db`。

备份要同时保存两处，缺一不可：

```bash
tar czf tunnel-manager-backup.tar.gz data/ .env
```

只备数据不备 `.env` 里的 `APP_ENCRYPTION_KEY`，恢复后无法解密 2FA 与 OAuth 令牌；只备密钥不备数据则无从恢复。两者都含敏感信息，不要提交到公开仓库，也别和数据放在同一台机器上。`docker compose down` 不会删除挂载出来的 `./data`。

## 上手流程

连接 Cloudflare（OAuth 授权或填静态 Token）→ 在「隧道管理」选一个已连接的 Tunnel，或直接在面板里新建（创建后会给出 cloudflared 连接令牌和运行命令）→ 填转发地址 → 选绑定模式并提交域名 → 验证。

两个容易踩的点：转发地址必须能从运行 `cloudflared` 的环境访问，面板容器能访问不代表 `cloudflared` 能访问；提交成功只代表 Cloudflare API 接受了配置，DNS 生效、证书签发和边缘同步仍需等待，可以先用 `nslookup` 和 `curl -I` 确认，再去控制台看 Custom Hostname 的证书状态。

自动化调用用 `X-API-Key` 请求头，绑定接口是 `POST /api/domain/bind`，请求体带 `mode`（`simple` / `preferred`）、`main_domain`、`aux_domain`、`preferred_cname`；批量是 `POST /api/domain/bind-batch`，`items` 里每项可以有自己的模式与转发地址。为兼容旧客户端，不传 `mode` 时按优选处理。

## 它适合什么场景

直接用 Cloudflare Dashboard 功能最完整、无需维护额外服务，适合偶尔配一个域名。Terraform 声明式、可审查、适合团队与基础设施版本管理，但引入成本更高，临时调整也要走发布流程。

Tunnel Manager 在两者之间：比手动控制台更适合重复绑定，比 Terraform 更强调即时操作和低部署门槛，提供 Web 和 Telegram 两种入口，只覆盖 Tunnel、DNS 与 SaaS 这条工作流。

适合个人站长、家庭实验室，以及管理少量服务器与多个域名的开发者。不适合多客户多账户的托管平台、需要审批流与审计日志的企业，或者要求数据库高可用与多实例部署的环境——它是单实例、单库设计。

## 最近做的性能优化

v2.2.3 顺手处理了一个长期存在的问题：面板自带四个字重的 MiSans，每个约 4.7 MB 全量字库，首屏光字体就近 19 MB。现在改成核心子集加 unicode-range 分片按需加载，核心子集已包含界面全部文字，首屏字体降到 373 KB，只有用户内容里的生僻字才按需补拉分片。同时后端启用 gzip，主 JS 从 450 KB 降到 149 KB，字体文件也带上了长效缓存头。

响应式断点也做了收敛：此前散落着 11 种断点值，缩放窗口时各页面各自错位重排，现在统一为 480 / 640 / 768 / 1024 四档。

## 总结

Tunnel Manager 解决的是一个具体而重复的问题：把 Cloudflare Tunnel ingress、DNS CNAME、优选域名、Custom Hostname 和 fallback origin 串成一条可操作的域名发布流程。

它最有价值的地方不是「有一个管理面板」，而是把多个 Cloudflare 模块之间容易遗漏的依赖关系固化在后端逻辑里。偶尔新增一个域名，Cloudflare Dashboard 已经够用；基础设施必须声明式管理并接受严格审查，Terraform 更合适；如果你正在维护多个 Tunnel 域名，想用一个轻量、自托管的面板或 Telegram Bot 快速完成发布，这类工具才真正体现价值。

项目地址：[github.com/qiuyuxc/tunnel-manager](https://github.com/qiuyuxc/tunnel-manager)
