const loadedRules = new Set()
let styleEl = null
let basePath = '/mouse/tuantuanma/'
let initPromise = null

function normalizeBasePath(path) {
  const trimmed = String(path || '').trim()
  if (!trimmed) return '/mouse/tuantuanma/'
  return trimmed.endsWith('/') ? trimmed : `${trimmed}/`
}

function ensureStyleElement() {
  if (styleEl?.isConnected) return styleEl
  styleEl = document.getElementById('sakura-mouse-style-sheet')
  if (!styleEl) {
    styleEl = document.createElement('style')
    styleEl.id = 'sakura-mouse-style-sheet'
    styleEl.dataset.mouseTheme = 'tuantuanma'
    document.head.appendChild(styleEl)
  }
  return styleEl
}

function ruleToCss(rule, assetBasePath) {
  const grouped = new Map()

  for (const frame of rule.keyframes) {
    if (!frame.percents?.length) continue
    const key = `${frame.file}|${frame.x}|${frame.y}`
    const current = grouped.get(key) ?? {
      file: frame.file,
      x: frame.x,
      y: frame.y,
      percents: [],
    }
    current.percents.push(...frame.percents)
    grouped.set(key, current)
  }

  const keyframes = [...grouped.values()]
    .map((frame) => {
      const percent = [...new Set(frame.percents)]
        .sort((a, b) => a - b)
        .map((value) => `${value}%`)
        .join(', ')
      const url = `${assetBasePath}${frame.file}`
      return `${percent} { cursor: url(${url}) ${frame.x} ${frame.y}, auto; }`
    })
    .join('\n  ')

  if (!keyframes) {
    throw new Error(`rule has no keyframes: ${rule.name}`)
  }

  const pseudoSelector = rule.hover ? ':hover' : ''
  return `
@keyframes ${rule.name} {
  ${keyframes}
}
${rule.selector}${pseudoSelector} {
  animation: ${rule.name} ${rule.duration}ms step-end infinite;
}
`.trim()
}

async function fetchManifest() {
  const url = `${basePath}manifest.json`
  const response = await fetch(url)
  if (!response.ok) {
    throw new Error(`failed to fetch mouse manifest: ${url}`)
  }
  return response.json()
}

async function loadRule(rule) {
  if (loadedRules.has(rule.name)) return
  const css = ruleToCss(rule, basePath)
  loadedRules.add(rule.name)
  const el = ensureStyleElement()
  el.textContent = `${el.textContent}\n${css}\n`
}

async function loadDefaultPointer(rules) {
  const pointer = rules.find((rule) => rule.name === 'tuantuanma-pointer')
  if (!pointer) return
  await loadRule(pointer)
}

async function loadRemainingStyles(rules) {
  const rest = rules
    .filter((rule) => rule.name !== 'tuantuanma-pointer')
    .sort((a, b) => a.priority - b.priority)

  for (const rule of rest) {
    await loadRule(rule)
  }
}

function shouldUseMouseStyle() {
  if (window.matchMedia('(prefers-reduced-motion: reduce)').matches) return false
  const meta = document.querySelector('meta[name="sakura-mouse-style"]')
  return meta?.getAttribute('content') === '1'
}

function readBasePath() {
  const meta = document.querySelector('meta[name="sakura-mouse-style"]')
  return normalizeBasePath(meta?.dataset.path || '/mouse/tuantuanma/')
}

async function initMouseStyle() {
  if (!shouldUseMouseStyle()) return
  basePath = readBasePath()
  ensureStyleElement()

  const manifest = await fetchManifest()
  const rules = manifest.rules ?? []
  if (!rules.length) {
    throw new Error('mouse manifest has no rules')
  }

  await loadDefaultPointer(rules)

  const preload = () => {
    void loadRemainingStyles(rules).catch((err) => console.warn('[mouse-style]', err))
  }

  if ('requestIdleCallback' in window) {
    requestIdleCallback(preload, { timeout: 8000 })
  } else {
    window.setTimeout(preload, 2000)
  }
}

export function scheduleMouseStyleInit() {
  if (!shouldUseMouseStyle()) return
  if (initPromise) return

  initPromise = initMouseStyle().catch((err) => {
    initPromise = null
    console.warn('[mouse-style]', err)
  })
}
