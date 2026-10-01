---
title: EdgeOne CDN 节点优选自动化：从 IP 扫描到 DNS 自动更新
date: 2026-09-08T00:00:00+08:00
description: 一个用于 EdgeOne CDN 节点优选的小型自动化工具，通过并发扫描、HTTPS 健康检查和延迟排序筛选出当前表现较好的节点，并通过华为云 DNS API 自动更新解析记录。同时提供 Web 控制台、定时任务和实时扫描进度，让原本需要手动完成的节点优选流程变成一个可以长期运行的自动化服务。
cover: /img/covers/edgeone-cdn.webp
draft: false
categories:
    - CDN
tags:
    - EdgeOne CDN
    - DNS
    - 优选
---

> **AI 声明**：本文由 AI 辅助整理与撰写，用于记录本项目的设计思路、实现过程和使用方式.

## 为什么要做这个项目

我在使用腾讯 EdgeOne CDN 时遇到一个比较现实的问题：海外节点在部分地区访问质量不错，但整体体验并不稳定。同一个域名，有些节点返回 `200`，有些节点返回 `418`，用户拿到什么节点，基本就看运气。

手动筛选当然可以解决一部分问题，但流程很繁琐：先扫 IP，再看状态码，再测延迟，最后手动去 DNS 控制台改解析。每次优选都要重复一遍，时间一长就失去了维护价值。

所以这个项目的目标很明确：把「扫描、验证、排序、更新 DNS」这一整套流程自动化，同时提供一个简单可用的 Web 页面，方便放在服务器上长期运行。

## 方案核心

项目围绕一个健康检查地址展开：

```text
https://eo.com/10086.png
```

扫描时不是简单访问 IP，而是直接向候选 IP 发起 HTTPS 请求，同时保留真实的 HTTP `Host`。这里有一个关键细节：TLS SNI 需要使用 `example.com`，而不是 `sni.eo.com`。这样可以避开部分地区对原始 SNI 的干扰，同时后端仍然通过 `Host` 识别业务请求。

扫描器会并发测试 IP 段中的地址，只保留返回状态码为 `200` 的节点。随后按照延迟排序，自动取前 10 个结果。这样筛选出来的节点，既满足业务可用性，也兼顾了访问速度。

## 自动更新 DNS

拿到优选 IP 后，项目会通过华为云 API 自动更新 DNS 记录。

假设当前的目标记录是：

```text
ys.eo.com
```

Zone 是：

```text
eo.com
```

凭据保存在 `credentials.csv` 中，脚本通过 AK/SK 签名调用华为云 DNS 接口。凭据文件不会打进压缩包，也不会出现在文章和配置示例里。部署到服务器后，需要单独上传，并设置 `600` 权限。

整个自动任务默认每 30 分钟执行一次。每次任务会重新扫描、重新排序、重新更新 DNS，让解析结果跟随节点质量变化自动调整。

## Web 控制台

项目内置了一个轻量 Web 控制台，主要功能包括：

- **扫描参数配置**：域名、SNI、路径、状态码、超时时间、并发数、IP 段文件。
- **DNS 配置**：Zone、记录名、TTL、凭据文件路径。
- **自动任务配置**：Top N 数量、执行间隔、超时时间、启动后是否立即执行。
- **Zone 自动获取**：避免手动猜测华为云里的 Zone ID。
- **扫描进度条**：可以看到当前扫描进度，而不是黑盒等待。
- **延迟展示**：结果不只显示排名，也显示每个 IP 的实际响应时间。
- **手动触发**：方便调整参数后立即验证效果。

对于这种需要长期维护的小工具，页面最重要的不是华丽，而是让每一步都可控、可观察。

## 部署方式

项目已经打包为 `edgeone-dashboard.tar.gz`：

[下载 edgeone-dashboard.tar.gz](https://m.goy.cc.cd/edgeone-dashboard.tar.gz)

整套项目不依赖数据库、Node.js 或前端构建流程，只需要 Python 3.10+。下面按 Debian/Ubuntu 服务器写具体命令。

### 1. 本地上传压缩包

先在本机计算校验值，方便服务器端核对：

```bash
sha256sum edgeone-dashboard.tar.gz
```

把压缩包上传到服务器：

```bash
scp edgeone-dashboard.tar.gz root@你的服务器IP:/tmp/
```

如果你的本机不是 Linux/macOS，也可以用任意 SFTP 工具上传，目标路径保持 `/tmp/edgeone-dashboard.tar.gz` 即可。

### 2. 登录服务器并解压

```bash
ssh root@你的服务器IP
sudo mkdir -p /opt/edgeone
sudo tar -xzf /tmp/edgeone-dashboard.tar.gz -C /opt/edgeone --strip-components=1
cd /opt/edgeone
python3 --version
```

`python3 --version` 至少应该输出 `Python 3.10.0` 或更高版本。

### 3. 放置华为云凭据

在服务器上创建 `/opt/edgeone/credentials.csv`：

```bash
sudo nano /opt/edgeone/credentials.csv
```

文件格式如下：

```csv
Access Key Id,Secret Access Key
你的AccessKey,你的SecretKey
```

保存后设置权限：

```bash
sudo chmod 600 /opt/edgeone/credentials.csv
```

注意：这个文件不要提交 Git，不要放进压缩包，也不要放在公开目录。

### 4. 设置控制台访问 Token

生成一个随机 Token：

```bash
openssl rand -hex 32
```

复制输出结果，然后编辑 systemd 模板：

```bash
sudo nano /opt/edgeone/deploy/edgeone-dashboard.service
```

把这一行：

```ini
Environment=DASHBOARD_TOKEN=change-this-token
```

改成：

```ini
Environment=DASHBOARD_TOKEN=刚才生成的随机Token
```

如果使用 Nginx 反向代理，建议同时把 `dashboard_config.json` 里的监听地址改成仅本机访问：

```bash
sudo sed -i 's/"listen_host": "0.0.0.0"/"listen_host": "127.0.0.1"/' /opt/edgeone/dashboard_config.json
```

如果你确定不使用反向代理，可以暂时保持 `0.0.0.0`，但公网暴露时必须依赖 Token，并确保防火墙只开放必要端口。

### 5. 启动 systemd 服务

```bash
sudo cp /opt/edgeone/deploy/edgeone-dashboard.service /etc/systemd/system/edgeone-dashboard.service
sudo systemctl daemon-reload
sudo systemctl enable --now edgeone-dashboard
sudo systemctl status edgeone-dashboard --no-pager
```

查看运行日志：

```bash
journalctl -u edgeone-dashboard -f
```

如果服务正常，日志里应出现类似：

```text
dashboard listening on http://127.0.0.1:8080
```

也可以直接检查健康接口：

```bash
curl http://127.0.0.1:8080/healthz
```

### 6. 配置 Nginx HTTPS 反向代理

安装 Nginx 和 Certbot：

```bash
sudo apt update
sudo apt install -y nginx certbot python3-certbot-nginx
```

复制项目自带的反向代理配置：

```bash
sudo cp /opt/edgeone/deploy/nginx.conf /etc/nginx/sites-available/edgeone-dashboard
sudo ln -s /etc/nginx/sites-available/edgeone-dashboard /etc/nginx/sites-enabled/edgeone-dashboard
```

把配置里的域名改成自己的：

```bash
sudo nano /etc/nginx/sites-available/edgeone-dashboard
```

需要修改的位置：

```nginx
server_name edgeone.example.com;
```

改成：

```nginx
server_name dashboard.你的域名.com;
```

然后申请 HTTPS 证书：

```bash
sudo certbot --nginx -d dashboard.你的域名.com
```

检查并重载 Nginx：

```bash
sudo nginx -t
sudo systemctl reload nginx
```

最后在浏览器打开：

```text
https://dashboard.你的域名.com
```

页面会要求输入 Token，填入第 4 步生成的随机 Token 即可。

### 7. 验证 API 权限

如果想在服务器命令行验证 Token 是否生效：

```bash
curl -H "Authorization: Bearer 你的Token" http://127.0.0.1:8080/api/config
```

返回 `401` 说明 Token 不匹配；能返回 JSON 配置，说明服务正常。

## 安全边界

这个项目只应该用于测试自己拥有授权的服务和 IP 段。扫描不是无目的探测，而是针对 EdgeOne 业务相关的地址段做健康检查。

同时，安全细节需要严格遵守：

1. `credentials.csv` 不进入 Git，不进入压缩包。
2. 凭据文件权限设置为 `600`。
3. 公网访问必须开启 Token。
4. 生产环境建议通过 HTTPS 反向代理访问。
5. 扫描并发和 IP 段规模保持适度，避免无意义的大范围请求。

## 一点感受

这个项目最开始只是一个小脚本，解决的是「哪些 EdgeOne IP 能用」的问题。后来逐步加上延迟排序、自动更新 DNS、Web 页面、进度条、定时任务，最后变成了一个可以放到服务器上稳定运行的小系统。

它的价值不在于技术多复杂，而在于把重复劳动完全交给了程序。以前每次优选都要人工干预，现在只需要定期看一眼结果，解析会自动更新，节点质量变化也能及时反映出来。

对于使用 Anycast CDN 的用户来说，这种思路同样可以复用：先用健康检查确定可用性，再用延迟决定优先级，最后通过 DNS 自动调度。只要业务入口和授权范围明确，就能把一个不稳定的服务，优化成体验更加稳定的服务。
