/* 站点顶栏公告 —— 运行时从后端拉取，改内容不需要重新构建。
 * 配置来自 <meta name="sakura-site-banner">（见 layouts/partials/site-banner.html）。
 * 任何失败都静默处理，绝不影响页面本身。
 */
(function () {
  'use strict'

  var meta = document.querySelector('meta[name="sakura-site-banner"]')
  if (!meta || !meta.content) return

  var ENDPOINT = meta.content
  var TIMEOUT = parseInt(meta.getAttribute('data-timeout'), 10) || 5000
  var DISMISS_KEY = 'kukie-site-banner-dismissed'
  var BAR_ID = 'site-banner'

  var payload = null
  var version = ''

  function barEl() { return document.getElementById(BAR_ID) }

  function syncHeight() {
    var bar = barEl()
    var h = bar ? Math.ceil(bar.getBoundingClientRect().height) : 0
    document.documentElement.style.setProperty('--site-banner-h', h + 'px')
  }

  function isDismissed(v) {
    try { return localStorage.getItem(DISMISS_KEY) === v } catch (e) { return false }
  }
  function rememberDismiss(v) {
    try { localStorage.setItem(DISMISS_KEY, v) } catch (e) { /* 忽略隐私模式等异常 */ }
  }

  function el(tag, cls, text) {
    var node = document.createElement(tag)
    if (cls) node.className = cls
    if (text != null) node.textContent = text
    return node
  }

  function mount(d) {
    if (barEl()) return

    var bar = el('div', 'site-banner')
    bar.id = BAR_ID
    bar.setAttribute('role', 'region')
    bar.setAttribute('aria-label', '站点公告')
    if (d.level) bar.setAttribute('data-level', d.level)

    bar.appendChild(el('span', 'site-banner__badge', d.badge || '公告'))

    var body = el('div', 'site-banner__body')
    var text
    if (d.link) {
      text = el('a', 'site-banner__link', d.text)
      text.href = d.link
      if (d.external) { text.target = '_blank'; text.rel = 'noopener' }
    } else {
      text = el('span', 'site-banner__link', d.text)
    }
    body.appendChild(text)
    bar.appendChild(body)

    var toggle = el('button', 'site-banner__toggle', '展开')
    toggle.type = 'button'
    toggle.hidden = true
    bar.appendChild(toggle)

    var close = el('button', 'site-banner__close', '\u2715')
    close.type = 'button'
    close.setAttribute('aria-label', '关闭公告')
    close.title = '关闭'
    close.addEventListener('click', function () {
      rememberDismiss(version)
      bar.remove()
      syncHeight()
    })
    bar.appendChild(close)

    function refresh() {
      var expanded = bar.classList.contains('is-expanded')
      /* 折叠时只有真的被截断才给「展开」；展开后按钮常驻 */
      toggle.hidden = expanded ? false : (text.scrollHeight <= text.clientHeight + 1)
      syncHeight()
    }

    toggle.addEventListener('click', function () {
      var expanded = bar.classList.toggle('is-expanded')
      toggle.textContent = expanded ? '收起' : '展开'
      refresh()
      window.requestAnimationFrame(refresh)
    })

    document.body.insertBefore(bar, document.body.firstChild)
    refresh()
    window.requestAnimationFrame(refresh)
  }

  function onLayoutChange() {
    if (barEl()) syncHeight()
    else if (payload && !isDismissed(version)) mount(payload)
  }

  window.addEventListener('resize', syncHeight, { passive: true })
  window.addEventListener('orientationchange', function () { window.setTimeout(syncHeight, 150) })
  /* PJAX 换页后：若内容区被整体替换导致顶栏丢失，则补挂回去 */
  document.addEventListener('sakura:page-content-visible', function () {
    window.setTimeout(onLayoutChange, 60)
  })
  if (document.fonts && document.fonts.ready) {
    document.fonts.ready.then(function () { window.setTimeout(syncHeight, 0) })
  }

  var controller = new AbortController()
  var timer = window.setTimeout(function () { controller.abort() }, TIMEOUT)

  fetch(ENDPOINT, { cache: 'no-store', signal: controller.signal })
    .then(function (res) {
      var lastModified = ''
      try { lastModified = res.headers.get('Last-Modified') || '' } catch (e) { /* 跨域不可读时忽略 */ }
      if (!res.ok) throw new Error('http ' + res.status)
      return res.json().then(function (data) { return { data: data, lastModified: lastModified } })
    })
    .then(function (r) {
      window.clearTimeout(timer)
      var d = r.data
      if (!d || d.enabled === false || !d.text) return
      /* 版本号：优先 updated，没写就用响应的 Last-Modified 兜底 */
      version = String(d.updated || r.lastModified || d.text)
      if (isDismissed(version)) return
      payload = d
      mount(d)
    })
    .catch(function () {
      window.clearTimeout(timer)
      /* 静默：公告不是关键路径 */
    })
})()
