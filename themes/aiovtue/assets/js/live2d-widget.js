const DEFAULT_MIN_WIDTH = 768
const DEFAULT_WIDGET = 'https://live2d.kafuchino.top/'

function normalizeApiBase(api) {
  const trimmed = String(api || '').trim().replace(/\?.*$/, '')
  if (!trimmed) return ''
  return trimmed.endsWith('/') ? trimmed : `${trimmed}/`
}

function readJsonScript(id, fallback) {
  const el = document.getElementById(id)
  if (!el?.textContent) return fallback
  try {
    return JSON.parse(el.textContent)
  } catch {
    return fallback
  }
}

function loadExternalResource(url, type) {
  return new Promise((resolve, reject) => {
    let tag
    if (type === 'css') {
      tag = document.createElement('link')
      tag.rel = 'stylesheet'
      tag.href = url
    } else if (type === 'js') {
      tag = document.createElement('script')
      tag.src = url
      tag.defer = true
    }
    if (!tag) {
      reject(new Error(`invalid resource type: ${type}`))
      return
    }
    tag.onload = () => resolve(url)
    tag.onerror = () => reject(new Error(`failed to load: ${url}`))
    document.head.appendChild(tag)
  })
}

function readConfig(root) {
  const widget = normalizeApiBase(root.dataset.widget || DEFAULT_WIDGET)
  const apiPath = normalizeApiBase(root.dataset.apiPath)
  const cdnPath = normalizeApiBase(root.dataset.cdnPath || widget)
  return { widget, apiPath, cdnPath }
}

let initPromise = null

async function bootLive2d(root) {
  const { widget, apiPath, cdnPath } = readConfig(root)
  const tipsPath = root.dataset.waifuTips || 'jsdelivr/sequential/waifu-tips.js'
  const cssPath = root.dataset.waifuCss || 'css/left.css'
  const waifuTipsJson = root.dataset.waifuPath || 'waifu-tips.json'

  await Promise.all([
    loadExternalResource(`${widget}${cssPath}`, 'css'),
    loadExternalResource(`${widget}live2d.min.js`, 'js'),
    loadExternalResource(`${widget}${tipsPath}`, 'js'),
  ])

  if (typeof window.initWidget !== 'function') {
    throw new Error('initWidget is not available')
  }

  const tools = readJsonScript('sakura-live2d-tools', [])
  const config = {
    waifuPath: `${widget}${waifuTipsJson}`,
  }

  if (apiPath) {
    config.apiPath = apiPath
  } else {
    config.cdnPath = cdnPath
  }

  if (Array.isArray(tools) && tools.length) config.tools = tools
  if (root.dataset.drag === 'true') config.drag = true

  window.initWidget(config)
  root.dataset.loaded = 'true'
  root.dataset.modelSource = apiPath || cdnPath
}

async function initLive2dWidget(root) {
  if (root.dataset.loaded === 'true') return
  if (initPromise) return initPromise

  const minWidth = Number(root.dataset.minWidth || DEFAULT_MIN_WIDTH)
  if (window.innerWidth < minWidth) return

  initPromise = bootLive2d(root).catch((err) => {
    initPromise = null
    throw err
  })

  return initPromise
}

export function scheduleLive2dInit() {
  const root = document.getElementById('sakura-live2d')
  if (!root || root.dataset.loaded === 'true') return

  const run = () => {
    void initLive2dWidget(root).catch((err) => console.warn('[live2d]', err))
  }

  if ('requestIdleCallback' in window) {
    requestIdleCallback(run, { timeout: 6000 })
  } else {
    window.setTimeout(run, 1500)
  }
}
