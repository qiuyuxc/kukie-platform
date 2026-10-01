// excalidraw-embed@0.18.4 — npm 当前最新版
const EXCALIDRAW_VERSION = '0.18.4'
const CSS_URL = `https://cdn.jsdelivr.net/npm/excalidraw-embed@${EXCALIDRAW_VERSION}/dist/excalidraw-embed.css`
const JS_URL = `https://cdn.jsdelivr.net/npm/excalidraw-embed@${EXCALIDRAW_VERSION}`

let assetsPromise = null
let excalidrawMountToken = 0

function loadExcalidrawAssets() {
  if (window.ExcalidrawEmbed) return Promise.resolve()
  if (!assetsPromise) {
    assetsPromise = new Promise((resolve, reject) => {
      if (!document.querySelector(`link[href="${CSS_URL}"]`)) {
        const link = document.createElement('link')
        link.rel = 'stylesheet'
        link.href = CSS_URL
        document.head.appendChild(link)
      }

      const existing = document.querySelector(`script[src="${JS_URL}"]`)
      if (existing) {
        if (window.ExcalidrawEmbed) {
          resolve()
          return
        }
        existing.addEventListener('load', () => resolve(), { once: true })
        existing.addEventListener('error', () => reject(new Error('excalidraw script failed')), { once: true })
        return
      }

      const script = document.createElement('script')
      script.src = JS_URL
      script.defer = true
      script.onload = () => resolve()
      script.onerror = () => reject(new Error('excalidraw script failed'))
      document.body.appendChild(script)
    })
  }
  return assetsPromise
}

function normalizeExcalidrawData(raw) {
  if (!raw || typeof raw !== 'object') {
    return { elements: [], appState: {}, files: {} }
  }
  return {
    elements: Array.isArray(raw.elements) ? raw.elements : [],
    appState: raw.appState && typeof raw.appState === 'object' ? raw.appState : {},
    files: raw.files && typeof raw.files === 'object' ? raw.files : {},
  }
}

function setStageState(stage, state) {
  if (!stage) return
  stage.classList.toggle('is-loading', state === 'loading')
  stage.classList.toggle('is-error', state === 'error')
  stage.classList.toggle('is-ready', state === 'ready')
}

/** 仅控制外层 .sakura-excalidraw-stage 容器，不干预 Excalidraw 内部 canvas */
const STAGE_WRAPPER_THEMES = {
  light: {
    stageBackground: 'var(--sakura-color-background)',
  },
  dark: {
    stageBackground: '#1f1f23',
  },
}

function getSiteTheme() {
  return document.documentElement.classList.contains('dark') ? 'dark' : 'light'
}

function getDrawingTheme(appState = {}) {
  return appState.theme === 'dark' ? 'dark' : 'light'
}

function syncStageWrapperTheme(stage) {
  if (!stage) return
  const siteTheme = getSiteTheme()
  const { stageBackground } = STAGE_WRAPPER_THEMES[siteTheme]
  stage.dataset.theme = siteTheme
  stage.style.setProperty('--sakura-excalidraw-stage-bg', stageBackground)
}

function scheduleExcalidrawResize() {
  window.requestAnimationFrame(() => {
    window.requestAnimationFrame(() => {
      window.dispatchEvent(new Event('resize'))
    })
  })
}

function waitUntilPageRevealed() {
  return new Promise((resolve) => {
    if (!document.body.classList.contains('sakura-loading')) {
      resolve()
      return
    }

    const observer = new MutationObserver(() => {
      if (!document.body.classList.contains('sakura-loading')) {
        observer.disconnect()
        scheduleExcalidrawResize()
        resolve()
      }
    })

    observer.observe(document.body, { attributes: true, attributeFilter: ['class'] })
    window.setTimeout(() => {
      observer.disconnect()
      resolve()
    }, 8000)
  })
}

async function waitForStageLayout(stage) {
  await waitUntilPageRevealed()

  const styleLink = document.querySelector('link[data-sakura-page-style="excalidraw"]')
  if (styleLink && !styleLink.sheet) {
    await new Promise((resolve) => {
      styleLink.addEventListener('load', resolve, { once: true })
      styleLink.addEventListener('error', resolve, { once: true })
    })
  }

  await new Promise((resolve) => {
    window.requestAnimationFrame(() => window.requestAnimationFrame(resolve))
  })

  if (stage && stage.offsetHeight < 48) {
    await new Promise((resolve) => window.setTimeout(resolve, 50))
  }
}

function bindStageWrapperThemeSync(stage, registerPageCleanup) {
  let lastTheme = getSiteTheme()

  const syncIfChanged = () => {
    const nextTheme = getSiteTheme()
    if (nextTheme === lastTheme) return
    lastTheme = nextTheme
    syncStageWrapperTheme(stage)
  }

  const observer = new MutationObserver(() => {
    syncIfChanged()
  })

  observer.observe(document.documentElement, { attributes: true, attributeFilter: ['class'] })
  syncStageWrapperTheme(stage)

  registerPageCleanup(() => {
    observer.disconnect()
  })
}

function bindExcalidrawViewportFullscreen(stage, registerPageCleanup) {
  const btn = stage.querySelector('.sakura-excalidraw-fullscreen-btn')
  if (!btn) return

  const root = document.documentElement
  btn.hidden = false

  const isActive = () => stage.classList.contains('is-viewport-fullscreen')

  const notifyResize = () => {
    scheduleExcalidrawResize()
  }

  const updateBtn = () => {
    const active = isActive()
    btn.classList.toggle('is-active', active)
    const label = active ? '退出全屏' : '浏览器内全屏'
    btn.setAttribute('aria-label', label)
    btn.title = label
  }

  const exitViewportFullscreen = () => {
    if (!isActive()) return
    stage.classList.remove('is-viewport-fullscreen')
    root.classList.remove('is-excalidraw-viewport-fullscreen')
    updateBtn()
    notifyResize()
  }

  const enterViewportFullscreen = () => {
    if (isActive()) return
    stage.classList.add('is-viewport-fullscreen')
    root.classList.add('is-excalidraw-viewport-fullscreen')
    updateBtn()
    notifyResize()
  }

  const onClick = () => {
    if (isActive()) exitViewportFullscreen()
    else enterViewportFullscreen()
  }

  const onKeydown = (event) => {
    if (event.key !== 'Escape' || !isActive()) return
    event.preventDefault()
    exitViewportFullscreen()
  }

  btn.addEventListener('click', onClick)
  document.addEventListener('keydown', onKeydown)
  updateBtn()

  registerPageCleanup(() => {
    btn.removeEventListener('click', onClick)
    document.removeEventListener('keydown', onKeydown)
    exitViewportFullscreen()
  })
}

function resetExcalidrawAssetsState() {
  assetsPromise = null
}

async function mountExcalidrawPage(registerPageCleanup) {
  const page = document.querySelector('.sakura-excalidraw-page')
  if (!page || page.dataset.excalidrawMounted === '1') return

  const stage = page.querySelector('.sakura-excalidraw-stage')
  const mountEl = page.querySelector('.sakura-excalidraw-canvas')
  const loadingEl = page.querySelector('.sakura-excalidraw-loading')
  if (!mountEl || !stage) return

  const token = ++excalidrawMountToken
  const isStale = () => token !== excalidrawMountToken || !page.isConnected

  registerPageCleanup(() => {
    excalidrawMountToken += 1
  })

  const src = stage.dataset.excalidrawSrc
  if (!src) {
    setStageState(stage, 'error')
    if (loadingEl) loadingEl.textContent = '未配置画板文件'
    return
  }

  setStageState(stage, 'loading')
  try {
    await waitForStageLayout(stage)
    if (isStale()) return
    await loadExcalidrawAssets()
    if (isStale()) return
    const response = await fetch(src, { credentials: 'same-origin' })
    if (!response.ok) throw new Error(`fetch failed: ${response.status}`)
    const raw = await response.json()
    if (isStale()) return
    const initialData = normalizeExcalidrawData(raw)
    const drawingTheme = getDrawingTheme(initialData.appState)

    const api = await window.ExcalidrawEmbed.renderExcalidraw(mountEl, {
      initialData,
      theme: drawingTheme,
      viewModeEnabled: true,
      zenModeEnabled: true,
      gridModeEnabled: false,
      UIOptions: {
        canvasActions: {
          changeViewBackgroundColor: false,
          clearCanvas: false,
          export: false,
          loadScene: false,
          saveToActiveFile: false,
          saveAsImage: true,
          toggleTheme: false,
        },
        tools: {
          image: false,
        },
      },
    })

    if (isStale()) {
      mountEl.innerHTML = ''
      api?.updateScene?.({ elements: [], appState: {}, files: {} })
      return
    }

    page.dataset.excalidrawMounted = '1'
    setStageState(stage, 'ready')
    syncStageWrapperTheme(stage)
    scheduleExcalidrawResize()
    bindStageWrapperThemeSync(stage, registerPageCleanup)
    bindExcalidrawViewportFullscreen(stage, registerPageCleanup)
    registerPageCleanup(() => {
      mountEl.innerHTML = ''
      api?.updateScene?.({ elements: [], appState: {}, files: {} })
      delete page.dataset.excalidrawMounted
      resetExcalidrawAssetsState()
    })
  } catch (err) {
    if (isStale()) return
    console.warn('[excalidraw]', err)
    setStageState(stage, 'error')
    if (loadingEl) loadingEl.textContent = '画板加载失败，请稍后重试'
    delete page.dataset.excalidrawMounted
    resetExcalidrawAssetsState()
  }
}

function mountExcalidrawFeatures(registerPageCleanup) {
  void mountExcalidrawPage(registerPageCleanup)
}

window.__sakuraMountExcalidraw = mountExcalidrawFeatures
