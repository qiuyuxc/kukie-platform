# Docker 部署

## 准备

服务器使用 AMD64（x86_64）架构，需要 Docker Engine 与 Compose 插件，支持 `docker compose` 命令。建议先在测试服务器验证。镜像启动不要求填写域名；本地可用 IP 和端口访问。公网部署使用 HTTPS 反向代理，默认不要将 8084、8085、8086 明文暴露到公网。

镜像不绑定网站、后台或 API 域名。网站和后台默认同源访问各自的 `/api/v1`，不需要填写 API 端点或来源白名单；App 仍通过外部可达的完整 API 入口连接同一套服务。

## 获取代码和镜像

获取部署文件：

```sh
git clone https://github.com/qiuyuxc/kukie-platform.git
cd kukie-platform/deploy
cp .env.example .env
chmod 600 .env
```

编辑 `.env`：

```dotenv
KUKIE_IMAGE=ghcr.io/qiuyuxc/kukie-platform:latest
KUKIE_SITE_URL=
KUKIE_CONSOLE_ORIGINS=
KUKIE_SECURE_COOKIE=1
KUKIE_INDEXNOW_KEY=
KUKIE_BIND=127.0.0.1
KUKIE_SITE_PORT=8086
KUKIE_CONSOLE_PORT=8085
KUKIE_API_PORT=8084
```

`KUKIE_SITE_URL` 可留空，此时页面、站点地图和 RSS 的链接跟随访问地址。要固定网站规范地址，可在运行时填写完整 origin，例如 `https://blog.example.com`，含协议、不含路径；修改后重建容器即可，不需要重新构建镜像。它不是访问域名白名单。启用 IndexNow 时必须填写，以免将访客传入的主机名用于后台推送。

`KUKIE_CONSOLE_ORIGINS` 只用于额外允许的浏览器跨域来源，默认留空。同源后台和原生 App 均不需要此项；浏览器 Cookie 写操作仍校验 CSRF，App 仍使用 Bearer Token。

HTTPS 反向代理保留 `KUKIE_SECURE_COOKIE=1`，此时页面地址使用 HTTPS；仅在本地 HTTP 直连测试时改为 `0`。代理必须保留浏览器请求的原始 `Host`；Caddy 示例默认如此，Nginx 可用 `proxy_set_header Host $http_host;`。服务不会采用任意客户端的 `Forwarded`、`X-Forwarded-Host` 或 `X-Forwarded-Proto`。

默认只启动 `kukie` 一个服务，三个端口都只绑定 `127.0.0.1`，由宿主机或面板的反向代理转发。国内机器先运行 `sh detect-region.sh` 可自动换成更快的镜像源，见下文「国内机器换镜像源」。

公开镜像不需要登录，直接检查配置并启动：

```sh
docker compose config --quiet
docker compose pull
docker compose up -d
docker compose ps
docker compose logs --tail=80 kukie caddy
```

如果自行限制了镜像访问权限，才需要有该包访问权限的 GitHub 账号和含 `read:packages` 权限的经典个人访问令牌。先通过标准输入登录，再执行拉取命令，不把令牌放进命令历史或配置文件：

```sh
read -r -s -p 'GHCR token: ' GHCR_TOKEN
printf '\n'
printf '%s' "$GHCR_TOKEN" | docker login ghcr.io -u YOUR_GITHUB_USERNAME --password-stdin
unset GHCR_TOKEN
docker compose pull
```

`read -s -p` 示例使用 Bash。Docker 会按本机凭据存储配置保存登录信息，请限制服务器账号访问权限。

需要容器自带 HTTPS 时，用 `edge` 配置文件把 Caddy 一起启动：

```sh
docker compose --profile edge up -d
```

Caddy 会在服务健康后启动并申请证书，占用 80/443，此时需要把 `.env` 里的 `KUKIE_SITE_HOST`、`KUKIE_CONSOLE_HOST`、`KUKIE_API_HOST` 填成三个域名。如果宿主机已有反向代理占用这两个端口，就不要启用 `edge`，改为把 `127.0.0.1:8086`、`8085`、`8084` 接到现有代理，参考 `deploy/go-site/Caddyfile.example`。

## 面板部署（1Panel）

1Panel 的「容器 → 编排」可以直接导入这份 `compose.yaml`，服务器上不需要敲命令。

1. 新建编排，项目名自定，例如 `kukie`。
2. 把 `deploy/compose.yaml` 的内容粘贴进编排编辑器。文件里没有顶层 `name`，项目名由面板决定；数据卷固定叫 `kukie_data`，不会随项目名变化，升级或重建编排都不会丢数据。
3. 在编排目录里放一份 `.env`（用面板的文件管理新建，内容照 `deploy/.env.example` 填），或用面板的环境变量编辑功能逐条填同样的变量。
4. 启动编排。默认只起 `kukie` 一个容器，不占 80/443。

反向代理和证书交给 1Panel 的「网站」功能：新建站点后添加反向代理，网站指向 `127.0.0.1:8086`，后台指向 `127.0.0.1:8085`，API 指向 `127.0.0.1:8084`。这样不需要启用 `edge` 配置文件。

首次初始化时，在面板的容器终端里执行：

```sh
cat /data/setup-token
```

如果面板在另一台机器上、需要通过受控内网访问端口，把 `KUKIE_BIND` 改成对应网卡地址（或 `0.0.0.0` 并用防火墙限制来源）。用 `http://IP:端口` 本地测试时把 `KUKIE_SECURE_COOKIE` 改成 `0`；不需要设置站点域名或 `KUKIE_CONSOLE_ORIGINS`。

## 原生 App 的 API 入口

保留 API 的 HTTPS 反代入口并指向 `127.0.0.1:8084`，完整转发 `/api/`、`/media/` 和 `/assets/`。示例使用 `api.example.com`，这是反代和 App 的连接地址，不是 Docker 镜像绑定的域名。

原生 Android App 构建时使用 `KUKIE_ANDROID_API`（或 Gradle 的 `-PapiBaseUrl`）指定此地址，登录后使用 Bearer Token。后台同源化不会将 API 变成容器内部专用，也不会改动会员账号或令牌协议。网站 `8086` 只开放网页所需的部分 API，不要直接把它当作 App 的完整 API 入口。API 地址若保持不变，已有 App 无需重建；地址改变则需同步更新 App 配置。

## 国内机器换镜像源

`ghcr.io` 在部分国内网络下很慢。`deploy/detect-region.sh` 会先判断部署机所在地区，再决定 `.env` 里的 `KUKIE_IMAGE` 用直连还是国内加速源：

```sh
cd deploy
sh detect-region.sh          # 探测并写入 .env
sh detect-region.sh --print  # 只看结果，不改文件
```

判定顺序：

1. 出口 IP 归属地。先问 `myip.ipip.net`，失败再问 `ipinfo.io/country`。
2. 归属地拿不到时，比较两个源的实际连通耗时，取更快的那个。
3. 选定后确认该源可用，不可用就回退直连。

归属地判断不准（例如海外机器被识别成国内）时，用 `KUKIE_REGION=global` 或 `KUKIE_REGION=cn` 强制指定。加速源默认是 `ghcr.nju.edu.cn`，可用 `KUKIE_GHCR_MIRROR` 换成别的。

不想跑脚本就直接改 `.env`：

```dotenv
KUKIE_IMAGE=ghcr.nju.edu.cn/qiuyuxc/kukie-platform:latest
```

这只影响拉镜像。Dockerfile 里的 apt、npm、Go 模块和 Hugo 下载地址都指向上游，镜像由 GitHub Actions 构建，国内源对构建速度没有帮助。

## 首次初始化

从服务器本地读取初始化密钥：

```sh
docker compose exec kukie cat /data/setup-token
```

打开后台 HTTPS 域名，填写该密钥、昵称、邮箱和至少 12 位密码。初始化完成后密钥文件删除，不能再次创建初始管理员。不要把密钥贴进公开聊天、截图或日志。

在后台设置 SMTP 后，读者才能通过网站评论区申请邮箱验证码并注册；SMTP 未启用时关闭新注册，已有账号仍能登录。先给自己的邮箱发测试邮件，再验证完整注册流程。验证码有效期 10 分钟。

评论登录支持两步验证码或恢复码，已有两步验证设置继续有效。网站域名只开放读者所需 API；管理接口不在网站端口开放。后台评论列表与网站读取同一数据库，删除后页面刷新即可看到变化。

## 持久化与备份

从旧版 `name: kukie` 编排升级时，先在 `.env` 加入 `COMPOSE_PROJECT_NAME=kukie`，再执行本节的停止、备份和启动命令；原来使用面板或 `-p` 指定其他项目名的，继续使用原名。新版不再在文件中固定项目名，不能让命令误指向新的项目。首次部署不需要此项。

`kukie_data` 卷挂载到 `/data`，保存数据库、加密主密钥和本地上传文件。容器以 UID/GID 10001 运行，代码只读，数据目录可写。**数据库和 `master.key` 必须一起备份**，缺少密钥会导致已加密的设置无法恢复。

首次迁入已有数据时，先停止旧服务，再将完整数据目录复制到空的目标卷中，并将所有权设为 `10001:10001`。不要让两个服务同时写同一 SQLite 数据目录。S3/WebDAV 文件不在本地卷里，需另行备份远端对象及相应配置。

以下命令在 `deploy/` 下运行，会短暂停止网站：

```sh
umask 077
mkdir -p backups
docker compose stop kukie
docker run --rm -v kukie_data:/data:ro -v "$PWD/backups:/backup" \
  debian:bookworm-slim tar -czf /backup/kukie-$(date +%Y%m%d-%H%M%S).tar.gz -C /data .
docker compose start kukie
```

确认归档成功后，再复制到受保护的异机位置。归档内含账号、凭据密文和密钥，应当按敏感数据管理。不要在线只复制 `app.db`，WAL 中可能仍有未合并的数据；不要执行 `docker compose down -v`，它会删除数据卷。

恢复时先创建一个新卷，不覆盖现有卷：

```sh
docker volume create kukie_restore
docker run --rm -v kukie_restore:/data -v "$PWD/backups:/backup:ro" \
  debian:bookworm-slim sh -ec 'tar -xzf /backup/YOUR_BACKUP.tar.gz -C /data && chown -R 10001:10001 /data'
```

停止服务后，把 `compose.yaml` 中 `volumes.kukie_data.name` 改为 `kukie_restore`，运行 `docker compose up -d kukie`，验收登录、文章和图片后再决定如何处理旧卷。备份来自哪个版本，就先用该版本镜像恢复。

## 升级与回滚

升级前先确认项目名仍与正在运行的编排一致：旧版默认项目保留 `COMPOSE_PROJECT_NAME=kukie`，面板自定义项目保留原名。执行 `docker compose ps` 确认能看到已有容器后，再按以下步骤操作；不要误建第二套服务共用同一个数据卷。

生产环境建议把 `.env` 的 `KUKIE_IMAGE` 固定为 Actions 摘要中的 digest，例如 `ghcr.io/qiuyuxc/kukie-platform@sha256:...`，避免 `latest` 漂移。

1. 记录当前镜像 digest，完成数据备份。
2. 把 `.env` 的镜像地址改为新版本。
3. 执行 `docker compose pull kukie && docker compose up -d kukie`。
4. 检查 `docker compose ps`、日志、网站正文、后台登录和新评论。

程序回滚时将镜像地址改回旧 digest 并重建容器，不删除数据卷。数据库若已升级到旧程序不兼容的结构，需要用升级前备份在新卷恢复；这样会丢失备份之后的写入，应先保留现有数据并确认取舍。

## 自动构建

`.github/workflows/image.yml` 的执行顺序：

1. 执行 Go 全量测试、race 检查和 `go vet`。
2. 构建镜像，启动独立临时容器，检查网站、后台、接口隔离和重启持久化。
3. 验证通过后向 GHCR 发布 `linux/amd64` 镜像。

主分支发布 `latest` 与 `sha-完整提交号`，`v*` 标签发布同名镜像标签。PR 只测试和构建，不推送镜像。工作流使用 `GITHUB_TOKEN` 的 `packages: write` 权限，不需要把个人令牌放到 Actions secrets 中。发布不包含 SSH 或自动上线步骤。

更换网站域名只需调整 DNS、证书和反向代理。如果配置了 `.env` 的 `KUKIE_SITE_URL`，同步修改它并重建容器；使用内置 Caddy 时也要修改对应 `KUKIE_*_HOST`。不需要设置 Actions 的站点变量，也不需要重新构建镜像。浏览器账号 Cookie 不跨域名迁移，换域名后需重新登录；数据库和账号不会因此改变。

也可自行构建：

```sh
docker build --platform linux/amd64 -t kukie:local .
```

上述命令在仓库根目录执行；然后将 `deploy/.env` 的 `KUKIE_IMAGE` 改为 `kukie:local`，用 `docker compose up -d` 启动，不对本地镜像执行 pull。

## 可选 IndexNow

在 `.env` 设置公开 HTTPS 站点地址 `KUKIE_SITE_URL` 和符合 IndexNow 要求的 `KUKIE_INDEXNOW_KEY` 后重建容器。服务器会提供同名 `.txt` 验证文件，并在内容变动后提交网站 URL、失败后重试。未设置密钥时完全关闭。正式域名上线前不要启用，不要向真实收录服务发送测试文章。

## 排查

- `IndexNow requires KUKIE_SITE_URL`：填写公开 HTTPS 网站地址，或清空 `KUKIE_INDEXNOW_KEY` 暂停推送。
- 登录后马上变回未登录：正式环境通过 HTTPS 并保留 `KUKIE_SECURE_COOKIE=1`；HTTP 本地测试使用 `0`。同源请求被拒绝时检查反代是否保留原始 `Host`，不要用通配来源白名单绕过校验。
- 容器持续不健康：查看 `docker compose logs kukie`，确认卷权限、域名与资源包都正确。
- 镜像拉取 `denied`：确认账号拥有仓库及包权限，重新执行 GHCR 登录。
- 验证码发不出：核对 SMTP 的启用状态、发件地址、端口和 TLS 模式，后台测试邮件失败时先修复投递配置。
- 大量用户同时登录出现限流：当前未信任反代客户端 IP 请求头，反代后的 IP 限流会聚合到代理地址；不要通过直接信任任意 `X-Forwarded-For` 来绕开校验。

外部服务仍需部署者实测：域名证书、真实邮件投递、S3/WebDAV 与搜索收录，不由本地测试替代。
