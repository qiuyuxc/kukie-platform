import { registerPageCleanup } from './page-cleanup.js'

export function initKukieComments() {
  const root = document.querySelector('[data-comment-provider="kukie"][data-post-id]')
  if (!root || root.dataset.ready) return
  root.dataset.ready = '1'
  const controller = new AbortController()
  const { signal } = controller
  registerPageCleanup(() => controller.abort())
  const find = name => root.querySelector(`[data-comment-${name}]`)
  const endpoint = `/api/v1/posts/${encodeURIComponent(root.dataset.postId)}/comments`
  let csrf = ''
  let busy = false
  let codeUntil = 0

  function session(data) {
    csrf = data?.csrf || ''
    find('auth').hidden = Boolean(data?.user)
    find('account').hidden = !data?.user
    find('compose').hidden = !data?.user
    find('identity').textContent = data?.user ? `已登录：${data.user.name}` : ''
  }

  async function request(path, body) {
    const response = await fetch(path, {
      method: body === undefined ? 'GET' : 'POST',
      credentials: 'same-origin',
      cache: 'no-store',
      headers: { 'Content-Type': 'application/json', 'X-CSRF-Token': csrf },
      body: body === undefined ? undefined : JSON.stringify(body),
      signal,
    })
    const data = await response.json()
    if (!response.ok) {
      if (data.code === 'session_expired' || data.code === 'unauthorized') session(null)
      throw new Error(data.error || '请求失败，请稍后重试')
    }
    return data
  }

  async function action(work) {
    if (busy) return
    busy = true
    root.setAttribute('aria-busy', 'true')
    root.querySelectorAll('button').forEach(button => { button.disabled = true })
    find('status').textContent = '正在处理…'
    try {
      await work()
    } catch (error) {
      if (!signal.aborted) find('status').textContent = error.message
    } finally {
      busy = false
      root.removeAttribute('aria-busy')
      root.querySelectorAll('button').forEach(button => { button.disabled = false })
    }
  }

  function render(items) {
    const list = find('list')
    list.replaceChildren()
    find('count').textContent = String(items.length)
    for (const item of items) {
      const entry = document.createElement('li')
      const meta = document.createElement('div')
      meta.className = 'kukie-comment-meta'
      const name = document.createElement('strong')
      name.textContent = item.name
      meta.append(name)
      if (item.author_label) {
        const badge = document.createElement('span')
        badge.textContent = item.author_label
        meta.append(badge)
      }
      const date = document.createElement('time')
      date.dateTime = item.created_at
      date.textContent = new Date(item.created_at).toLocaleString('zh-CN')
      meta.append(date)
      const text = document.createElement('p')
      text.textContent = item.text
      entry.append(meta, text)
      list.append(entry)
    }
  }

  for (const mode of ['login', 'register']) {
    find(mode).addEventListener('submit', event => {
      event.preventDefault()
      const form = event.currentTarget
      void action(async () => {
        const data = await request(`/api/v1/auth/${mode}`, Object.fromEntries(new FormData(form)))
        session(data)
        form.reset()
        find('status').textContent = '登录成功，可以发表评论了。'
        find('compose').querySelector('textarea').focus()
      })
    }, { signal })
  }

  find('code').addEventListener('click', () => {
    const email = find('register').elements.email
    if (!email.reportValidity()) return
    if (Date.now() < codeUntil) {
      find('status').textContent = '请稍等一分钟再发送验证码。'
      return
    }
    void action(async () => {
      await request('/api/v1/auth/email-code', { email: email.value })
      codeUntil = Date.now() + 60000
      find('status').textContent = '验证码已发送，请查看邮箱。'
    })
  }, { signal })

  find('logout').addEventListener('click', () => void action(async () => {
    await request('/api/v1/auth/logout', {})
    session(null)
    find('status').textContent = '已退出登录。'
  }), { signal })

  find('compose').addEventListener('submit', event => {
    event.preventDefault()
    const form = event.currentTarget
    void action(async () => {
      await request(endpoint, { text: form.elements.text.value })
      form.reset()
      find('status').textContent = '评论已发表。'
      try {
        render((await request(endpoint)).comments)
      } catch (error) {
        if (!signal.aborted) find('status').textContent = '评论已发表，列表刷新失败。请刷新页面查看，无需重复提交。'
      }
    })
  }, { signal })

  void (async () => {
    try {
      const response = await fetch('/api/v1/me', { credentials: 'same-origin', cache: 'no-store', signal })
      if (response.ok) session(await response.json())
      else if (response.status === 401) session(null)
      else throw new Error('登录状态加载失败，请刷新页面重试。')
      find('controls').hidden = false
      const config = await request('/api/v1/config')
      find('registration').hidden = !config.registration_enabled
      find('registration-note').textContent = config.registration_enabled ? '验证码用于验证你的邮箱。' : '暂未开放新账号注册，已有账号可以登录。'
    } catch (error) {
      if (!signal.aborted) {
        find('controls').hidden = false
        find('status').textContent = error.message
      }
    }
  })()
}
