---
title: 用 Cloudflare Workers + Telegram 搭一个不花钱的私人图床
date: 2026-09-02T00:00:00+08:00
description: 本文介绍如何使用 Cloudflare Workers + D1 + Telegram 搭建一个免费的私人图床，将图片存储在 Telegram 聊天记录中，D1 保存文件索引，由 Workers 提供 API、鉴权和图片直链代理。全程支持 GitHub + Cloudflare Git 集成部署，无需服务器和命令行，同时介绍自定义域名、Bot 自动上传、本地开发及常见坑点。
cover: /img/covers/cloudflare-telegram-image-host.webp
draft: false
categories:
    - Cloudflare
tags:
    - Cloudflare
    - Telegram
---

> **AI 声明**：本文由 AI 辅助撰写。部署步骤与配置参数基于本项目的真实代码整理，免费额度数据取自 Cloudflare 官方文档（2026 年 9 月）。但额度政策会变，动手时请以官方文档和实际报错为准。

图床通常只有两个选择：给别人的服务交钱，或者自己开一台机器跑 24 小时。这个项目走的是第三条路——图片本体扔进 Telegram 的聊天记录，索引存在 Cloudflare D1，前后端一起跑在 Workers 上。本文教你用**纯后台操作**把它部署起来：代码推上 GitHub，Cloudflare 连接仓库后自动构建部署，以后每次 push 就自动更新。

## 先说说存储：为什么是 Telegram，而不是 R2

Cloudflare 自家的对象存储 R2 其实相当慷慨，免费额度是每月 **10 GB 存储、100 万次写操作、1000 万次读操作，出站流量完全免费**——最后这条是 R2 相对 S3 的杀手锏，图床这种读多写少的场景几乎不可能超。如果你想要一个更"正统"的方案，R2 是更好的选择：延迟低、语义清晰、和 Workers 是原生绑定。

选 Telegram 的理由只有一个：**没有容量上限**。R2 的 10 GB 大约是三四千张照片，超了就要按 $0.015/GB-月 付费；而 Telegram 对 Bot 上传的文件不设总量限制，代价是需要多一跳 API 调用，而且下载地址一小时就过期（后面会讲怎么处理）。

所以这是一个明确的取舍：**想省心用 R2，想无限容量用 Telegram。** 本文讲的是后者。

至于另外两块，都在免费额度内：Workers 每天 10 万次请求，D1 提供 5 GB 存储和每天 500 万行读取。对一个人的图床来说，够用到不必考虑账单。

## 一、它是怎么工作的

整套后端只有一个 Worker 脚本（项目里叫 `works.js`，就是 `wrangler.toml` 里 `main` 指向的入口，名字不太直观但确实是线上跑的那份代码），对外暴露三条路径：

- `/api/*` —— 登录、列表、上传、删除，需要会话 Cookie
- `/dl/<file_id>` —— 公开图片直链，无需登录
- `/webhook` —— 接收 Telegram Bot 推送的图片

其余请求交给静态资源，也就是构建好的 React 单页应用。

上传时，Worker 把图片用 `sendDocument` 发进你指定的 Telegram 聊天，只把返回的 `file_id` 和元数据写进 D1。别人访问直链时，Worker 先查 D1 确认这张图还在，再用 `getFile` 换取真实下载地址并代理回去。前面提到 Telegram 给的地址一小时过期，所以 Worker 会把它缓存 50 分钟，避免每次访问都多打一次 API。

这个设计有个关键好处：**你的 Bot Token 永远不会出现在直链里**，外部只能看到你自己的域名。

## 二、准备工作

需要四样东西：Node.js 22 或更高版本、一个 Cloudflare 账号、一个 Telegram Bot、一个 GitHub 账号。

**创建 Bot**：在 Telegram 里找 [@BotFather](https://t.me/BotFather)，发送 `/newbot`，按提示起名，拿到形如 `123456:ABC-DEF...` 的 Token。

**准备存储聊天**：强烈建议新建一个群，把 Bot 拉进去，然后**提升为管理员并勾选「删除消息」权限**。原因很实际：Telegram 只允许 Bot 删除 48 小时内的私聊消息，而群管理员可以删除任意时间的消息。如果用私聊当存储，几天后你在网页上删掉的图片，其实只是从数据库消失了，Telegram 那边还留着。

**拿到 chat_id**：在群里随便发一条消息，然后访问

```
https://api.telegram.org/bot<你的Token>/getUpdates
```

在返回的 JSON 里找 `message.chat.id`，群组是一串负数，通常以 `-100` 开头。记下它。注意这一步要在设置 webhook 之前做，两者不能共存。

## 三、后台部署

下面的操作全部在 Cloudflare 后台完成，不需要装 wrangler、不需要碰命令行。整个流程分五步：建数据库 → 推送代码 → 连接仓库 → 建表 → 填密钥。

### 第一步：创建 D1 数据库

登录 Cloudflare 后台，进入 **Storage & Databases → D1**，点 **Create database**，名字填 `image-host`，创建。

创建完成后，在数据库列表里点进 `image-host`，找到它的 **UUID**（数据库详情页能看到）。复制它，后面要用。这一步是整篇教程里唯一需要"记住一个 ID"的地方，原因最后会解释。

### 第二步：准备并推送代码

先把这个项目仓库 fork 到你自己的 GitHub 名下：

**→ [github.com/qiuyuxc/Mono-image-host](https://github.com/qiuyuxc/Mono-image-host)**

打开仓库页，点右上角的 **Fork** 按钮，在「Owner」里选你自己，确认创建。这样一个属于你的副本就出现在你名下了（不想用网页 fork 的话，`git clone` 下来新建一个仓库再推上去也行，效果相同）。

然后做一件小事：打开你名下这份仓库里的 `wrangler.toml`，找到 `[[d1_databases]]` 这一段：

```toml
[[d1_databases]]
binding = "DB"
database_name = "image-host"
migrations_dir = "migrations"
```

在 `database_name` 下面加一行，填上刚才的 UUID：

```toml
database_id = "<你的 D1 UUID>"
```

然后 `git push` 推上 GitHub（fork 到你自己名下后，这一步只需要把修改 commit 再 push，也可以在 GitHub 网页上直接编辑文件）。为什么必须加这一行：Git 集成模式下，云端部署直接读这份配置文件，没有 ID 就不知道绑定哪个数据库。而原项目的一键部署脚本是运行时临时注入 ID 的，那个脚本在纯后台流程里用不上。

### 第三步：连接仓库创建 Worker

回到 Cloudflare 后台，进入 **Workers & Pages**，点 **Create application**，选择 **Import a repository**，按提示授权并选中你刚才推送的那个仓库。

在项目配置页里注意两件事：

1. **Worker 名称必须填 `beat`**。这是 `wrangler.toml` 里的 `name`，Cloudflare 要求后台的 Worker 名和配置文件完全一致，否则构建直接失败。你改其它名字都不行，除非你同时把 `wrangler.toml` 里的 `name` 也改了。
2. **构建命令（Build command）填 `npm run build`**。这个项目的前端是 Vite 构建的 React 应用，代码推上去时 `dist/` 是不存在的（在 `.gitignore` 里），必须在云端先构建出静态资源。Cloudflare 会先自动 `npm ci` 装依赖，再跑这个命令，最后执行 `npx wrangler deploy` 部署。

保存后 Cloudflare 会触发第一次构建。第一次通常会失败，原因见下一步——表还没建。先别管，把第四步做完再回来重新触发一次就行。

### 第四步：建表

Cloudflare 的 Git 集成只负责构建和部署代码，**不会自动执行数据库迁移**。需要手动把 `migrations/` 目录里的 SQL 应用到数据库上，纯后台的做法是：

1. 进入 **Storage & Databases → D1 → image-host**
2. 点 **Console**（控制台）
3. 打开仓库里的 `migrations/0001_init.sql`，把里面的内容全部复制
4. 粘贴到控制台，执行

执行成功会看到四张表被创建：`categories`、`files`、`file_path_cache`、`auth_attempts`。可以在左侧的数据库结构里确认。

> 如果你不介意用一次命令行，这一步也可以用 `npx wrangler d1 migrations apply image-host --remote` 完成。二者效果相同，任选其一。

### 第五步：在后台填入密钥

回到你的 Worker（Workers & Pages → `beat`），进入 **Settings → Variables and Secrets**，点 **Add**，类型选 **Secret**，依次添加四个：

| 变量名              | 值                           |
| ------------------- | ---------------------------- |
| `TG_BOT_TOKEN`    | 你的 Telegram Bot Token      |
| `STORAGE_CHAT_ID` | 存储群的 chat_id             |
| `ADMIN_PASSWORD`  | 网页管理密码，自己设         |
| `SESSION_SECRET`  | 会话签名密钥，用高强度随机值 |

其中 `SESSION_SECRET` 务必别自己编，用系统生成一段足够长的随机字符串（比如 `openssl rand -hex 32` 的输出，或者密码管理器生成的 64 位随机串）。Secret 类型会加密存储，保存后再看只会显示 `••••••`。

这四个加完后，回到 **Deployments** 页面重新触发一次刚才失败的构建，这次应该能成功。部署完成后，访问 `https://beat.<你的子域>.workers.dev`，输入 `ADMIN_PASSWORD` 就能进图库了。

### 之后的更新方式

以后想改任何东西：本地改代码 → `git push`，Cloudflare 检测到 push 自动构建部署。全流程只需要 Git，不再碰 Cloudflare 的命令行。

## 四、验证一遍

部署完别急着用，把五件事跑一遍：登录、上传一张图、复制直链、在无痕窗口打开直链、删除这张图后再刷新直链——最后一步应该返回 404。如果直链在删除后仍然能打开，说明 D1 记录没删干净，这是最容易被忽略的坑。

网页端单张图片默认限制 20 MB，支持 JPG、PNG、GIF 和 WebP。这里的格式判断不看浏览器给的 `Content-Type`，而是直接读文件头的 magic bytes，改扩展名骗不过去，文件名也会被重写成校验出的真实类型。

## 五、可选：用 Bot 直接发图

如果你想在手机上直接把图片发给 Bot 就完成上传：

```bash
curl "https://api.telegram.org/bot<token>/setWebhook?url=https://<你的域名>/webhook"
```

设置后，发给 Bot 的图片会自动入库，Bot 会回复保存好的直链。为了防止别人也往你的图床里塞东西，建议再设一个白名单：

在 Worker 的 **Settings → Variables and Secrets** 里新增一个 Secret，变量名 `ALLOWED_USERS`，值是允许使用的 Telegram 用户 ID 或聊天 ID，多个用逗号分隔。不在名单里的人会被直接拒绝。

## 六、绑定自己的域名

Worker 默认用当前请求的 host 生成直链，所以绑定自定义域名后，新上传的图片会自动使用新域名。在 Cloudflare 后台给 `beat` 这个 Worker 添加自定义域名即可，如果你希望所有直链固定在某个域名上（比如同时通过多个地址访问），可以在 `wrangler.toml` 的 `[vars]` 里加一行：

```toml
DOMAIN = "img.example.com"
```

需要注意，这只影响**新上传**的图片。已经入库的记录里存的是旧地址，改配置不会追溯修改。

## 七、本地开发

不想每次改动都部署到线上，可以跑本地版：

```bash
npm run dev:api   # 本地 API，端口 8787
npm run dev       # 前端，端口 5173
```

访问 `http://127.0.0.1:5173`，默认密码 `mono-local`。本地版用 Node 内置的 SQLite 和本地文件系统顶替 D1 和 Telegram，图片写在 `local-data/` 目录，不会提交到仓库。

要说明的是，本地后端只是一个形状相似的替身，表结构和线上并不完全一致。它适合调 UI 和交互，但存储相关的行为——比如 Telegram 的删除限制、file_path 缓存——只能在真实环境验证。

改完代码跑一遍检查：

```bash
npm run check   # eslint + vitest
```

## 几个已知的坑

**WebP 会变成贴纸。** Telegram 收到 WebP 时，返回的不是 `document` 而是 `sticker`，字段名不一样。代码里已经两个都取，但如果你要改上传逻辑，别忘了这件事。

**登录有限速。** 同一 IP 在 15 分钟内密码错误 8 次就会被临时锁住。自己被锁了就等一会儿，或者直接在 D1 控制台里清掉 `auth_attempts` 表。

**删除是真删。** 网页上的删除会同时移除 Telegram 消息和数据库记录，直链立即失效，没有回收站。不过如果 Telegram 那边删除失败（比如 Bot 被踢出群），数据库记录会被保留，方便你之后重试，不会出现"图还在但索引没了"的孤儿状态。

## 总结

这套方案的核心思路是**把三种免费资源各自用在它最擅长的地方**：Telegram 负责扛容量，D1 负责快速索引和分页，Workers 负责把两者缝合起来并挡在前面——它同时是网关、鉴权层和 Token 的保护壳。

值得留意的几个设计取舍：

- **直链走代理而非重定向**，多花一点 Worker 请求数，换来 Bot Token 不外泄，以及删除后立即失效的能力
- **file_path 缓存 50 分钟**，用一行 D1 记录规避了 Telegram 地址过期的问题，也顺带削掉了绝大部分 `getFile` 调用
- **配置里不写 database_id**，让仓库保持可被任何人一键部署的状态（代价是纯后台部署时需要你手动补上，见第三节第二步）

如果你只是想要一个能用的私人图床，照着第三节做完就行。如果你打算改造它，那么最该先理解的是 `/dl/` 这条路径——项目里几乎所有关于安全和一致性的考虑，都集中在那几十行代码里。

至于要不要换成 R2：如果你的图片总量不会超过 10 GB，而且更在意稳定性和延迟，那就换。Telegram 方案的价值在于容量无上限，而不是它更好。

---

## 附：命令行手动部署（备选）

如果你习惯用终端、或者以后想用脚本自动化，可以跳过上面的后台流程，用 wrangler 从本地直接部署：

```bash
npm ci
npx wrangler login
npm run deploy
```

`npm run deploy` 会做四件事：构建前端、按名字查找 D1 数据库（没有就自动创建）、把数据库 UUID 注入一份临时配置 `wrangler.auto.toml`、执行迁移并部署 Worker。**它不需要你手动建表、也不需要手动填 database_id**——临时配置用完即删，仓库始终保持干净。

密钥用命令行设置：

```bash
npx wrangler secret put TG_BOT_TOKEN
npx wrangler secret put STORAGE_CHAT_ID
npx wrangler secret put ADMIN_PASSWORD
npx wrangler secret put SESSION_SECRET
```

`SESSION_SECRET` 同样建议用 `openssl rand -hex 32` 生成。之后每次更新只需 `npm run deploy`。

两种方式都能达到同样的结果，区别在于：**后台 Git 集成**适合不想碰命令行的人，代码仓库即真相，push 即部署；**命令行方式**适合开发者，脚本可控、可进 CI/CD，且迁移由脚本自动完成。
