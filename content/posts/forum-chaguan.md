---
title: 用一个二进制文件跑一个论坛：chaguan 的技术选型笔记
date: 2026-09-06T00:00:00+08:00
description: 不用 Node.js、PostgreSQL、Redis，一个二进制文件也能跑完整论坛。
cover: /img/covers/forum-chaguan.webp
draft: false
categories:
    - GitHub
tags:
    - GitHub
    - forum
---

> **AI 声明**:本文由 AI(Claude)通读 chaguan 项目源码后撰写,内容基于仓库实际代码与提交记录,未经逐字人工校订。文中所有技术判断与数据均可在源码中核对,若有偏差以代码为准。PS:太长了,不爱看

# 用一个二进制文件跑一个论坛:chaguan 的技术选型笔记

大多数现代论坛的部署清单长这样:一个前端构建产物、一个后端服务、一个 Postgres、一个 Redis、一份反代配置,外加 npm 依赖树。chaguan 的部署清单是一个可执行文件加一个数据目录,启动命令只有一行:

```bash
docker run -d -p 8080:8080 -v "$PWD/data:/data" ghcr.io/qiuyuxc/chaguan:latest
```

没有前端构建,没有 npm,没有外部数据库,没有缓存层。模板与静态资源全部 `go:embed` 进二进制,时区数据也编了进去,所以 distroless 镜像里连 `/usr/share/zoneinfo` 都不存在,`TZ=Asia/Shanghai` 依然直接生效。

这不是「玩具项目所以简单」。功能表里有 LV0–LV6 等级体系、三色认证、积分商城、付费帖、抽奖帖、私信红包、全文搜索、TOTP 两步验证、SSE 实时推送、完整后台。代码量约 11000 行 Go 加 3400 行模板,24 个数据库迁移。简单是选出来的,不是功能少省出来的。

真正撑起这份简单的,是六个明确的技术决定:把数据库连接池设为 1、SQL 全部集中在一个包、迁移只加不删、积分存整数、实时推送只推信号、用后台巡检代替定时任务。下面逐条展开,之后再看几处值得单独拿出来说的实现细节。

## 决定一:数据库连接池大小设为 1

这是整个项目最反直觉的一行,连注释一起只有三行:

```go
// Store 包装 *sql.DB。<100 人论坛用单连接串行化全部读写,
// 彻底消灭 SQLITE_BUSY;单条查询微秒级,串行不构成瓶颈。
d.SetMaxOpenConns(1)
d.SetConnMaxIdleTime(0)
```

SQLite 的写操作本来就是全库串行的,多连接只能提升读并发,代价是要处理 `SQLITE_BUSY`、要区分读写连接、要小心事务里的锁升级。对一个百人量级的论坛,把连接池压到 1,这些问题一次性消失:读写全部排队,单条查询几十微秒,队列根本排不起来。配套的 pragma 也很朴素,直接拼在 DSN 里,新连接自动应用(单连接池下也就只有一条):

```go
dsn += "?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)"
```

驱动用的是 `modernc.org/sqlite`,纯 Go 实现,不需要 CGO。这一条直接决定了后面所有的交付方式:`CGO_ENABLED=0` 静态编译,交叉编译 amd64/arm64 无痛,scratch 镜像能跑。

值得说明的是这个选择的边界。它对「小型社区」成立,对「高读并发站点」不成立。项目把这个前提写在注释里,而不是藏在代码行为中,这比选择本身更重要。

## 决定二:不用 ORM,SQL 全部集中在一个包

`internal/db/db.go` 一个文件 3865 行,承载了项目全部 SQL。包注释只有一句话解释理由:

```go
// Package db 打开 SQLite 连接、执行内嵌迁移,并承载全部 SQL 查询。
// 不使用 ORM:换库时只需重写本包。
```

这是一个明确的架构边界。handlers 层调用的是 `store.ListFeedThreads(...)` 这样的领域方法,拿到的是 `db.Thread` 结构体,完全看不见 SQL;想从 SQLite 换成别的数据库,重写这一个包即可,上层不动。

代价是这个文件很长,项目用注释分节(`// ---------- 私信 ----------`、`// ---------- 积分 ----------`)代替拆文件。有个细节能看出这是有意为之:结构体扫描抽了个匿名接口出来复用。

```go
func scanUser(row interface{ Scan(...any) error }) (*User, error)
```

`*sql.Row` 和 `*sql.Rows` 都满足这个接口,于是单条查询和列表查询共用一份字段顺序,不会出现「加了一列,忘了改列表版本」的经典 bug。同样的模式在 `scanThread`、`scanPost`、`scanShopItem`、`scanDMConv` 上重复出现。

## 决定三:迁移只加列、只加表

24 个迁移文件,从 `0001_init.sql` 到 `0024_points_cents.sql`,没有一个删列、改类型、重命名。项目把这条写成了硬约定:迁移只做加列、加表,不改不删,旧二进制挂新库也能启动。

迁移执行逻辑本身很短:读 `embed.FS` 里的文件名排序,取已应用的最大版本号,逐个跳过或执行,每个迁移一个事务,版本号来自文件名前缀。

```go
ver, err := strconv.Atoi(strings.SplitN(name, "_", 2)[0])
if err != nil {
    return fmt.Errorf("migration 文件名必须以数字版本开头: %s", name)
}
```

没有 down migration,没有迁移框架。「只加不删」这个约束换来的是回滚安全:任何时候把镜像 tag 换回旧版本,库结构对旧二进制仍然是兼容的超集。对一个自托管、更新靠 `docker compose pull` 的项目,这比双向迁移实用得多。

## 决定四:钱不用浮点

积分系统在内部一律以「分」存整数,1 积分 = 100 分。这条规则单独占一个文件 `internal/db/points_unit.go`,73 行,核心就是一个常量:

```go
const PointScale = 100

// Pts 把整数积分换成内部单位。
func Pts(n int64) int64 { return n * PointScale }
```

三个入口各管一段:`db.Pts(n)` 写常量(代码里写 `Pts(5)` 表示 5 积分),`db.ParsePoints(s)` 读用户输入,模板助手 `pts` 负责展示。其中 `ParsePoints` 是手写解析而不是 `ParseFloat`,注释把接受范围划得很死:

```go
// ParsePoints 解析用户输入:"3.24" → 324,"5" → 500,".5" → 50。
// 只认最多两位小数;负号、指数、千分位一律拒掉(范围由调用方判)。
```

有个容易写错的地方它处理对了:`".5"` 要补零成 `"50"` 而不是 `"5"`,因为 5 角是 50 分不是 5 分。

```go
if len(frac) == 1 {
    frac += "0" // ".5" 是 5 角不是 5 分
}
```

`FormatPoints` 反向渲染:整数不带小数点,末尾的 0 去掉,所以 `500` 显示成 `5`,`324` 显示成 `3.24`,`50` 显示成 `0.5`。

积分从整数改成两位小数这件事是通过迁移 `0024_points_cents.sql` 做的,而 `points_unit.go` 的注释直接指向它:「涉及哪些列见迁移 0024;新增积分字段时记住存的是「分」」。这是把易错点记在了正确的位置。

## 决定五:实时推送只推信号,不推数据

`internal/handlers/events.go` 一共 104 行,实现了整个 SSE 推送。核心结构就是一个 map,值得注意的是它是 `userID → 该用户的所有连接`,因为同一个账号可能开了多个标签页:

```go
type hub struct {
    mu    sync.Mutex
    conns map[int64]map[chan string]struct{}
}
```

关键设计在 `publish` 上。事件里不带任何业务数据,只有一个事件名(`notif` 或 `dm`),前端收到信号后自己发请求去拉具体数字;channel 满了就直接丢弃,不阻塞调用方。

```go
// publish 给某用户的所有连接投递一个事件名;连接积压时丢弃该次信号,
// 前端靠下次心跳/轮询补上。
func (h *hub) publish(userID int64, event string) {
    h.mu.Lock()
    defer h.mu.Unlock()
    for ch := range h.conns[userID] {
        select {
        case ch <- event:
        default:
        }
    }
}
```

这么做有三个好处叠在一起。首先,推送方不占数据库连接——在只有一条数据库连接的架构里这一点是硬性的,如果 SSE 推送需要查库组装数据,写请求就会被推送阻塞。其次,丢消息变得安全,因为信号是无状态的,丢了下次任意一个信号或轮询都能补上,不需要重传机制。最后,前端逻辑简单,断线回退轮询和收到信号走的是同一条拉取路径。

其余是穿越反代的实务处理:25 秒心跳防静默回收、`retry: 5000` 告诉浏览器重连间隔、`X-Accel-Buffering: no` 关掉 nginx 缓冲。未登录用户直接返回 204,浏览器不会去重连一条永远没数据的流。

## 决定六:后台巡检代替定时任务

红包 24 小时未领取自动退回,抽奖帖到点自动开奖。这两件事都需要「时间到了自动发生」,项目没有引入 cron 或任务队列,而是一个 goroutine:

```go
// startSweeper 后台巡检(红包超时退回 + 定点开奖),启动时先扫一次补上停机欠账。
func (s *Server) startSweeper(opts Options) {
    ttl, every := opts.redpackTTL(), opts.sweepEvery()
    go func() {
        s.sweepOnce(ttl)
        for range time.Tick(every) {
            s.sweepOnce(ttl)
        }
    }()
}
```

两个细节是这段代码的全部价值。一是启动时先扫一次:进程停了一小时再起来,这一小时内到期的红包和该开的奖不会丢,启动扫描直接补上;循环用 `time.Tick` 而不是 `time.Sleep`,周期不会随处理耗时漂移。二是巡检间隔就是定点开奖的精度,并且这一点被同时写进了文档和代码注释:

```go
// sweepEvery 也决定「定点开奖」的精度:设定时刻最多晚一个周期才生效。
```

默认 1 分钟,所以设定 20:00 开奖,实际开奖时间落在 20:00 到 20:01 之间。对论坛抽奖这个精度足够,换来的是不需要任何外部调度组件。

这个设计还顺带解决了一个测试难题:「等 24 小时」是不可测试的。项目的解法是把两个时间参数暴露成环境变量,`CHAGUAN_RP_TTL` 默认 `24h`、`CHAGUAN_SWEEP` 默认 `1m`,而测试脚本把前者压到 2 秒。README 里直说了这一点——这两个变量存在的唯一目的就是给测试用,生产环境用默认值。`Options` 结构体用零值回落默认值,所以生产环境完全不需要设置:

```go
func (o Options) redpackTTL() time.Duration {
    if o.RedpackTTL > 0 {
        return o.RedpackTTL
    }
    return 24 * time.Hour
}
```

这是一个小而典型的可测试性设计:不为测试改变生产行为,只把时间参数变成注入项。

## 一段值得单独看的算法:奖池拆分

六个决定说完,再看几处具体实现。先是抽奖开奖:如果奖品是积分,开奖时要把奖池随机拆成 n 份分给 n 个中奖者,约束有两条——每份至少 1,总和必须严格等于奖池。

朴素做法(每份随机、最后一份补差)会在边界上出问题,最后一份可能是 0 或负数。项目用的是随机切点法:

```go
// splitPool 把奖池随机拆成 n 份,每份至少 1、总和严格等于 pool。
// 随机切点法:在 1..pool-1 取 n-1 个互不相同切点,相邻差值即份额。
// pool 为 0(实物奖)时返回全 0。
```

把奖池想象成一根长 `pool` 的绳子,在 `1..pool-1` 之间取 `n-1` 个互不相同的整数切点,切开后相邻差值就是每份的份额。因为切点互不相同且落在开区间内,每份必然大于等于 1;因为切点在同一根绳子上,总和必然等于 `pool`。不需要任何补差修正。边界情况也单独处理了,人数不小于奖池时没有可切空间,前 `pool` 个人各拿 1,总和仍然正好是 `pool`:

```go
if int64(n) >= pool {
    for i := int64(0); i < pool; i++ {
        out[i] = 1
    }
    return out
}
```

实物奖(`pool == 0`)返回全 0,由上层按「抽人不发积分」处理。开奖入口 `runDraw` 是手动开奖和定点开奖共用的,`actorID` 传 0 表示系统开的,无人参与时关掉抽奖并退回奖池。

## 安全:朴素但完整

项目没有引入任何安全框架,几处关键位置各自处理。

CSRF 用双提交:cookie 里一个 token,表单里一个 `_csrf` 字段(或 `X-CSRF-Token` 头),常量时间比较。

```go
if got == "" || info.CSRF == "" ||
    subtle.ConstantTimeCompare([]byte(got), []byte(info.CSRF)) != 1 {
    http.Error(w, "CSRF 校验失败,请刷新页面重试", http.StatusForbidden)
    return
}
```

这里有个容易漏的坑处理到了:multipart 表单需要单独调 `ParseMultipartForm`,否则 `FormValue("_csrf")` 拿不到值,图片上传会全部 403。

```go
if strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data") {
    r.ParseMultipartForm(4 << 20) // multipart 表单里也带 _csrf
} else {
    r.ParseForm()
}
```

htmx 侧则是在 `<body>` 上挂 `hx-headers`,所有 htmx 请求自动带头,不需要每个表单单独处理:

```html
<body hx-headers='{"X-CSRF-Token": "{{.CSRF}}"}'>
```

Markdown 的 XSS 防护是两层。goldmark 不开 `WithUnsafe`,原始 HTML 按纯文本输出;渲染完再过一道正则,把危险协议替换成 `#`。

```go
unsafeURL = regexp.MustCompile(`(?i)((?:href|src)=")(?:javascript|vbscript|data):`)
```

两层叠加的意义在于,即使 goldmark 某天放行了某种 HTML,协议过滤仍然拦着。私信正文用的是同一套过滤口径,只额外开了 `WithHardWraps`,因为聊天语境下单换行应该直接断行。

cookie 的 Secure 位按环境判定而不是硬编码,反代场景下也正确:

```go
func Secure(r *http.Request) bool {
    return r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https"
}
```

中间件顺序是 `recover → loadUser → csrf → mux`。CSRF 校验必须在 `loadUser` 之后,因为它需要会话里的 token;`recover` 在最外层,任何 panic 都变成 500 而不是断连。

最后一处是业务安全而非技术安全,但同样重要:付费帖要先验等级再收钱。

```go
// 等级门槛没过时先别收钱
if t.MinLevel > 0 {
    ...
    if level < t.MinLevel {
        http.Error(w, "这篇需要 LV"+strconv.Itoa(t.MinLevel)+" 及以上才能阅读", http.StatusForbidden)
        return
    }
}
```

如果顺序反了,用户会付了积分却依然看不到内容。

## 阅读门槛:口径写在文件头

付费帖和等级帖的可见性判断集中在 `internal/handlers/gate.go`,文件头两行注释就把产品口径定死了:

```go
// 阅读门槛(等级 / 付费解锁)与抽奖帖。
// 门槛只挡正文与回复区,标题、作者、统计始终可见,列表页也照常出现。
```

这个口径很关键:门槛帖仍然出现在首页流里,标题和作者可见,只有正文被挡。否则付费帖没有曝光,也就没人会去解锁。

对应的 `gateFor` 返回一个结构体而不是布尔值,因为「看不了」需要区分原因,前端要显示不同的提示:

```go
type threadGate struct {
    OK        bool  // 可以看正文
    MyLevel   int   // 我的等级(提示用)
    NeedLevel int   // >0:等级不够,需要这个等级
    NeedPay   int64 // >0:需支付这么多积分解锁
    Paid      bool  // 已解锁过
}
```

作者本人、管理员、该版块版主一律直通。未登录用户返回完整的门槛信息,好在页面上提示「登录后可解锁」。

## 前端:零构建的 SSR 加 htmx

模板是 `html/template`,一页一个文件,`partials/` 放复用片段。渲染器按页面名预解析模板集,每个页面都带上全部 partial:

```go
for _, name := range pageNames {
    t, err := template.New("layout").Funcs(funcs).ParseFS(tmplFS,
        "templates/layout.html",
        "templates/admin_layout.html",
        "templates/partials/composer.html",
        ...
        "templates/"+name+".html",
    )
}
```

三个渲染入口分工明确:`Render` 出前台整页,`RenderAdmin` 出后台整页(独立布局,不套前台版式),`Partial` 出单个 `define` 片段给 htmx 局部刷新用。

模板助手集中在 `web/render.go`,大部分是渲染细节的封装,有几个值得一提。`quotePreview` 生成引用回复的预览,它会跳过原文里以 `>` 开头的行,只取对方自己写的内容:

```go
// quotePreview 取帖子正文压成一行并截前 120 字。引用某楼时只带对方自己写的内容,
// 引用行(含 `>` 嵌套引用)不算,否则连环嵌套。
```

不这么做的话,A 引用 B、C 引用 A,预览里会套三层引用,越往后越长。兜底也想到了,整楼都是引用时回退用原文,避免预览是空的。

`roleBadge` 则用 `sql.NullString` 表达三态,而不是拆成两个字段:

```go
// roleBadge 渲染用户称号徽章:
// badge NULL=跟随身份,""=隐藏,非空=自定义文案(统一实心配色)。
```

认证 V 的配色分三色(官方蓝、厂商红、作者黄),并且兼容了旧数据里的写法——这是「迁移只加不删」的必然后果,老数据不改,靠读取层归一化:

```go
// verifyKindName 归一化分类:官方=蓝 V、厂商=红 V、作者=黄 V;
// 兼容旧数据里的「官号」「认证作者」写法。
```

前端 JS 是手写的 `app.js`(54KB)加 htmx(50KB),CSS 是手写的 `style.css`(97KB)。夜间模式用一段内联脚本在 `<head>` 里先设 `data-theme`,避免首屏闪白:

```html
<script>(function(){try{if(localStorage.getItem("chaguan-theme")==="dark")document.documentElement.setAttribute("data-theme","dark");}catch(e){}})();</script>
```

## 等级体系:一个公式加一个数组

LV0 到 LV6,经验值由三个行为加权得出,阈值数组的下标直接就是等级:

```go
func socialExp(threads, replies, liked, extra int64) int64 {
    return threads*12 + replies*3 + liked + extra
}

// levelThresholds 仿 B 站成长曲线(简化):下标即等级 LV0..LV6。
var levelThresholds = [...]int64{0, 60, 250, 800, 2200, 6000, 16000}
```

发帖 12 分、回复 3 分、被赞 1 分,`extra` 是管理员手动加的。`levelOf` 顺序扫一遍数组就出结果,`levelInfo` 额外处理了「管理员手动指定等级」的情况:此时展示经验按该级起点兜底,LV6 固定显示满值,而不是暴露真实累计经验。整个等级系统就这么两个函数加一个数组,没有配置表,没有后台可调参数。

## 测试:1677 行 shell,570 多条断言

项目没有 Go 单元测试,全部是 curl 打真实 HTTP 的端到端脚本,四套分工如下:

| 脚本               | 行数 | 覆盖                             |
| ------------------ | ---- | -------------------------------- |
| `smoke.sh`       | 1378 | 516 条断言,主流程全覆盖          |
| `mailflow.sh`    | 82   | 邮件链路 16 条,python3 起假 SMTP |
| `accountflow.sh` | 95   | 两步验证 18 条,python3 算 TOTP   |
| `sweeper.sh`     | 122  | 巡检 24 条,红包退回 + 定点开奖   |

这个选择和整个项目的气质一致:测的是「用户能不能走通」,而不是「函数返回值对不对」。对一个 SSR 项目,HTTP 层断言的信息密度确实更高,一条 curl 同时验证了路由、权限、SQL、模板渲染四层。

代价是没法测纯函数的边界。`splitPool` 和 `ParsePoints` 这两个算法性较强的函数,用表驱动单测覆盖边界会更扎实,这是目前最明显的一处可补空间。

顺带一提,`sweeper.sh` 会自己起实例并把 `CHAGUAN_RP_TTL` 压到 2 秒,而定点开奖那段只精确到分钟,所以这套要跑一分钟左右。CI 跑全部四套。

## 交付:三个工作流,一个镜像

CI 分三个工作流,`ci.yml` 在 push 和 PR 上跑 `go vet`、编译和四套端到端测试;`build.yml` 在默认分支和 `v*` 标签上用 buildx 出多架构镜像推 ghcr;`release.yml` 在 `v*` 标签上交叉编译二进制,连 `checksums.txt` 一起挂到 Release。

Dockerfile 是多阶段,底座 distroless static,有几个决定值得说。首先是以 root 运行,理由写在注释里:

```dockerfile
# root 运行:docker 自动建的 bind mount 目录归 root,非 root uid 写不进去。
```

这是自托管场景的现实妥协。要求用户先 `chown` 目录再启动,会让「一条 docker run 跑起来」的承诺失效,项目选了后者,并把理由留在了代码里。

其次是根证书和 `/tmp` 显式带入。distroless static 里什么都没有,而发信和 Turnstile 验证需要证书,multipart 溢出需要 `/tmp`:

```dockerfile
# 发信/Turnstile 要验证书;multipart 溢出写 /tmp
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=build /out/data /data
COPY --from=build /out/tmp /tmp
```

再者是探活用二进制自己。distroless 没有 curl 也没有 shell,所以主程序带了个 `-healthcheck` 标志,请求本机 `/healthz` 后按状态码退出:

```go
// healthcheck 请求本机 /healthz,正常返回 0(distroless 里没有 curl,探活用二进制自己)。
```

```dockerfile
HEALTHCHECK CMD ["/chaguan", "-healthcheck"]
```

最后是留了两条退路。`ARG BASE` 允许把底座换成 `scratch`,因为该带的东西都已经显式 COPY 了,换底座不会缺文件;`ARG GOPROXY` 给国内网络留了 `goproxy.cn` 的口子。这两处都是从实际部署踩坑反推出来的参数。

## 运行时的两个细节:数据目录与时区

SQLite 文件和上传的图片都在 `/data`,挂出去就是完整备份,换镜像不影响数据,迁移服务器就是拷一个目录:

```dockerfile
ENV CHAGUAN_DB=/data/chaguan.db CHAGUAN_UPLOADS=/data/uploads
VOLUME /data
```

三个环境变量能覆盖全部路径配置(`PORT`、`CHAGUAN_DB`、`CHAGUAN_UPLOADS`),而 SMTP、人机验证、站点品牌都在后台页面配、存数据库。这个划分是对的:进程启动需要的进环境变量,运营需要改的进后台。

时区则涉及一个全局副作用。签到的「一天」、后台今日统计、定点开奖都依赖本地时区,项目的处理是启动时按 `TZ` 覆盖 `time.Local`:

```go
// applyTZ 按 TZ 装载时区并覆盖 time.Local(全局副作用)。
func applyTZ() {
    ...
    time.Local = loc
}
```

改 `time.Local` 一般不推荐,但对一个单进程、单时区的自托管应用,它是最省事的做法:所有 `time.Now()` 自动正确,不需要到处传 `*time.Location`。注释里点明了「全局副作用」,加载失败也只是记日志继续跑,不 fatal。配套的是 `_ "time/tzdata"` 匿名导入,把时区数据编进二进制,这样 scratch 和 distroless 里也能解析 `Asia/Shanghai`。

## 这套选型适合什么,不适合什么

适合的是成员数百人以内的自托管社区:运维人力接近零,更新靠 `docker compose pull`,数据备份靠拷目录。

不适合的是高读并发(单连接会成为瓶颈)、多实例水平扩展(SQLite 文件锁不支持)、需要复杂全文检索排序(FTS5 trigram 够用但不是 Elasticsearch)。

项目自己很清楚这条线在哪。`SetMaxOpenConns(1)` 的注释直接写了「<100 人论坛」,这是把适用范围声明在代码里,而不是等用户踩坑。

## 收尾:约束比功能更值得抄 (PS: 纯吹)

chaguan 的功能列表其实不特别,大部分论坛都有。真正有借鉴价值的是它给自己划的那几条硬约束:连接池设为 1,用性能换掉一整类并发问题并写明适用规模;SQL 集中一包,换库只重写一个文件、上层零感知;迁移只加不删,换来任意版本回滚安全;钱存整数,单位换算集中在一个 73 行的文件里、注释指向具体迁移;推送只推信号,避免推送方占用唯一的数据库连接、顺带让丢消息变得安全;时间参数可注入,不为测试改生产行为、把「等 24 小时」压成「等 2 秒」。

这些约束都很具体,都有明确的换取物,并且都写在代码注释里而不是只存在于作者脑子里。这一点比任何单项技术选择都更值得抄。

代码里出现频率最高的注释模式不是「这段做什么」,而是「为什么这么做,以及不这么做会怎样」:

```go
frac += "0" // ".5" 是 5 角不是 5 分
r.ParseMultipartForm(4 << 20) // multipart 表单里也带 _csrf
w.Header().Set("X-Accel-Buffering", "no") // nginx 默认会缓冲,显式关掉
```

这种注释在半年后回头改代码时,价值比文档高得多。

---

**项目地址**:`github.com/qiuyuxc/chaguan`,许可 MIT

**技术栈**:Go 标准库 net/http(无框架)· html/template SSR + htmx(零构建)· SQLite(modernc.org/sqlite 纯 Go 驱动,WAL + 单写连接)· 单二进制交付
