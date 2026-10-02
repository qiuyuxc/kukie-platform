# 二进制部署

日常部署推荐阅读 `docs/docker.md`。本方式用于不运行容器的服务器，生成的程序绑定构建机操作系统与架构；不要把 Termux 构建产物直接复制到普通 Linux。

## 构建发布包

构建机需要 Node.js 22.12+、Go 1.26+、C 编译工具链、Hugo Extended 0.167.0 与 tar。

```sh
npm run site:package
```

产物位于 `.local/releases/`。压缩包包含程序、后台资源、网站模板与主题资源、初次导入文章、静态图片及部署示例，不包含数据库或凭据。打包脚本每次建立新目录，避免混入旧产物。

## 服务器布局

```text
/srv/kukie/releases/<version>/
/srv/kukie/current -> releases/<version>
/srv/kukie/data/
/etc/kukie/kukie.env
```

创建专用 `kukie` 系统用户，将归档解压到新的版本目录；代码由 root 管理，数据目录交给 `kukie` 用户，权限设为 0700。

将包内 `deploy/kukie.env.example` 复制到 `/etc/kukie/kukie.env`，将 `deploy/kukie.service.example` 安装到 systemd。环境文件设置持久数据路径；网站地址和后台来源无需填写。可选的 `KUKIE_SITE_URL` 只在运行时固定网站公开地址，换域名无需重新打包。把 `current` 链接指向新版本，systemd 通过 `/bin/sh` 运行发布包里的 `deploy/run.sh`。

```sh
sudo systemctl daemon-reload
sudo systemctl enable --now kukie
sudo systemctl status kukie
sudo journalctl -u kukie -n 80 --no-pager
```

默认仅监听本机 8084（API）、8085（后台）、8086（网站）。使用 `deploy/Caddyfile.example` 配置 HTTPS 反代。不要使用 root 运行应用，不要公开数据目录或把整个发布目录作为静态文件根目录。

反代须保留原始 `Host`，HTTPS 反代使用 `KUKIE_SECURE_COOKIE=1`；HTTP 直连使用 `0`。不信任任意客户端传入的 `Forwarded` 或 `X-Forwarded-*` 作为站点地址。后台同源请求无需白名单，App 通过 8084 的 HTTPS 反代入口使用完整 API 和 Bearer Token。网站 8086 的 API 只包含网页读者所需的子集，不能替代 App 入口。

首次初始化密钥保存在 `/srv/kukie/data/setup-token`，在服务器本地读取后填写到后台。创建管理员后该文件删除。SMTP、评论、图片存储和可选两步验证均在后台管理。

## 内容与维护

文章仅在第一次启动时导入，此后使用后台编辑，升级不会覆盖数据库。网站正文、索引和评论动态读取数据库；附属静态页与主题修改需要重新构建发布包。

更新前停止服务并备份完整数据目录，至少包含 `app.db`、可能存在的 WAL 文件、`master.key` 和 `uploads/`。不要只备份数据库而漏掉密钥。外部存储的图片需要在存储服务侧备份。

每次更新解压到新目录，切换 `current` 后重启服务，检查健康接口、网站与后台。程序回滚只切换旧版本目录，不覆盖数据库；遇到不兼容的数据迁移时，用升级前完整备份在另一个数据目录恢复，再明确切换。

```sh
curl --fail http://127.0.0.1:8084/api/v1/health
curl --fail http://127.0.0.1:8085/
curl --fail http://127.0.0.1:8086/
```

首次切换域名前，在独立数据目录和端口验证页面、旧链接、登录与评论。原站可以保留作为切换前的回退入口，但新服务里新增的文章不会同步回旧静态站。
