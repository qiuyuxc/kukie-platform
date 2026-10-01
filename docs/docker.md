# Docker 部署

## 准备

服务器使用 AMD64（x86_64）架构，需要 Docker Engine 与 Compose 插件，支持 `docker compose` 命令。建议先在测试服务器验证。准备网站、后台、API 三个域名，将 DNS 指向服务器，放行 TCP 80/443；HTTP/3 可额外放行 UDP 443。不要将 8084、8085、8086 暴露到公网。

镜像构建时嵌入网站域名。默认值是 `https://www.kukie.cn`，部署时 `KUKIE_SITE_URL` 必须一致。后台和 API 域名只在部署配置中设置。

## 获取代码和镜像

在有仓库访问权限的账号下执行：

```sh
gh repo clone qiuyuxc/kukie-platform
cd kukie-platform/deploy
cp .env.example .env
chmod 600 .env
```

编辑 `.env`：

```dotenv
KUKIE_IMAGE=ghcr.io/qiuyuxc/kukie-platform:latest
KUKIE_SITE_URL=https://www.kukie.cn
KUKIE_SITE_HOST=www.kukie.cn
KUKIE_CONSOLE_HOST=console.example.com
KUKIE_API_HOST=api.example.com
KUKIE_INDEXNOW_KEY=
```

把示例后台和 API 域名改成自己的域名。`KUKIE_SITE_HOST` 必须等于 `KUKIE_SITE_URL` 中的主机名，不带协议或路径。

拉取私有镜像需要有该包访问权限的 GitHub 账号，以及含 `read:packages` 权限的经典个人访问令牌。通过标准输入登录，不把令牌放进命令历史或配置文件：

```sh
read -r -s -p 'GHCR token: ' GHCR_TOKEN
printf '\n'
printf '%s' "$GHCR_TOKEN" | docker login ghcr.io -u YOUR_GITHUB_USERNAME --password-stdin
unset GHCR_TOKEN
docker compose config --quiet
docker compose pull
docker compose up -d
docker compose ps
docker compose logs --tail=80 kukie caddy
```

`read -s -p` 示例使用 Bash。Docker 会按本机凭据存储配置保存登录信息，请限制服务器账号访问权限。

Caddy 在服务健康后启动并申请 HTTPS 证书。如果已有反向代理占用 80/443，可只启动 `docker compose up -d kukie`，按照 `deploy/go-site/Caddyfile.example` 将三个本机端口接到现有代理。

## 首次初始化

从服务器本地读取初始化密钥：

```sh
docker compose exec kukie cat /data/setup-token
```

打开后台 HTTPS 域名，填写该密钥、昵称、邮箱和至少 12 位密码。初始化完成后密钥文件删除，不能再次创建初始管理员。不要把密钥贴进公开聊天、截图或日志。

在后台设置 SMTP 后，读者才能通过网站评论区申请邮箱验证码并注册；SMTP 未启用时关闭新注册，已有账号仍能登录。先给自己的邮箱发测试邮件，再验证完整注册流程。验证码有效期 10 分钟。

评论登录支持两步验证码或恢复码，已有两步验证设置继续有效。网站域名只开放读者所需 API；管理接口不在网站端口开放。后台评论列表与网站读取同一数据库，删除后页面刷新即可看到变化。

## 持久化与备份

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

更换网站域名：在仓库 Settings → Secrets and variables → Actions → Variables 设置 `KUKIE_SITE_URL`，如 `https://blog.example.com`；然后手动运行构建。部署时同步修改 `.env` 中的网站 URL 和主机名。如果构建域名与运行配置不一致，服务会拒绝启动，避免生成错误链接与 SEO 元信息。

也可自行构建：

```sh
docker build --platform linux/amd64 --build-arg KUKIE_SITE_URL=https://blog.example.com -t kukie:local .
```

上述命令在仓库根目录执行；然后将 `deploy/.env` 的 `KUKIE_IMAGE` 改为 `kukie:local`，用 `docker compose up -d` 启动，不对本地镜像执行 pull。

## 可选 IndexNow

在 `.env` 设置符合 IndexNow 要求的 `KUKIE_INDEXNOW_KEY` 后重建容器。服务器会提供同名 `.txt` 验证文件，并在内容变动后提交网站 URL、失败后重试。未设置时完全关闭。正式域名上线前不要启用，不要向真实收录服务发送测试文章。

## 排查

- `load site: ... differs from the bundle`：构建与运行域名不一致，使用正确变量重建镜像。
- 登录后马上变回未登录：正式环境必须通过 HTTPS，保留 `KUKIE_SECURE_COOKIE=1`，后台来源配置要与浏览器地址一致。
- 容器持续不健康：查看 `docker compose logs kukie`，确认卷权限、域名与资源包都正确。
- 镜像拉取 `denied`：确认账号拥有仓库及包权限，重新执行 GHCR 登录。
- 验证码发不出：核对 SMTP 的启用状态、发件地址、端口和 TLS 模式，后台测试邮件失败时先修复投递配置。
- 大量用户同时登录出现限流：当前未信任反代客户端 IP 请求头，反代后的 IP 限流会聚合到代理地址；不要通过直接信任任意 `X-Forwarded-For` 来绕开校验。

外部服务仍需部署者实测：域名证书、真实邮件投递、S3/WebDAV 与搜索收录，不由本地测试替代。
