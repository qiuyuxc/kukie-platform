
import { registerPageCleanup } from './page-cleanup.js'

let momentsScriptPromise = null
let excalidrawScriptPromise = null
let galleryPostScriptPromise = null

function getMomentsScriptUrl() {
  return document.querySelector('meta[name="sakura-moments-script"]')?.content || ''
}

async function loadMomentsScript() {
  if (window.__sakuraMountMoments) return
  const url = getMomentsScriptUrl()
  if (!url) return

  if (!momentsScriptPromise) {
    momentsScriptPromise = new Promise((resolve, reject) => {
      const existing = document.querySelector(`script[src="${url}"]`)
      if (existing) {
        if (window.__sakuraMountMoments) {
          resolve()
          return
        }
        const finishExisting = () => {
          if (window.__sakuraMountMoments) resolve()
          else reject(new Error('moments script failed'))
        }
        existing.addEventListener('load', finishExisting, { once: true })
        existing.addEventListener('error', () => reject(new Error('moments script failed')), { once: true })
        if (existing.dataset.sakuraLoaded === '1') queueMicrotask(finishExisting)
        return
      }

      const script = document.createElement('script')
      script.src = url
      script.defer = true
      script.onload = () => resolve()
      script.onerror = () => reject(new Error('moments script failed'))
      document.body.appendChild(script)
    })
  }

  await momentsScriptPromise
}

export async function initMomentsModule() {
  if (!document.querySelector('.sakura-moments-page')) return

  if (!window.__sakuraMountMoments) {
    await loadMomentsScript()
  }
  window.__sakuraMountMoments?.(registerPageCleanup)
}

function getExcalidrawScriptUrl() {
  return document.querySelector('meta[name="sakura-excalidraw-script"]')?.content || ''
}

async function loadExcalidrawScript() {
  if (window.__sakuraMountExcalidraw) return
  const url = getExcalidrawScriptUrl()
  if (!url) return

  if (!excalidrawScriptPromise) {
    excalidrawScriptPromise = new Promise((resolve, reject) => {
      const existing = document.querySelector(`script[src="${url}"]`)
      if (existing) {
        if (window.__sakuraMountExcalidraw) {
          resolve()
          return
        }
        existing.addEventListener('load', () => resolve(), { once: true })
        existing.addEventListener('error', () => reject(new Error('excalidraw script failed')), { once: true })
        return
      }

      const script = document.createElement('script')
      script.src = url
      script.defer = true
      script.onload = () => resolve()
      script.onerror = () => reject(new Error('excalidraw script failed'))
      document.body.appendChild(script)
    })
  }

  await excalidrawScriptPromise
}

export async function initExcalidrawModule() {
  if (!document.querySelector('.sakura-excalidraw-page')) return

  if (!window.__sakuraMountExcalidraw) {
    await loadExcalidrawScript()
  }
  window.__sakuraMountExcalidraw?.(registerPageCleanup)
}

function getGalleryPostScriptUrl() {
  return document.querySelector('meta[name="sakura-gallery-post-script"]')?.content || ''
}

async function loadGalleryPostScript() {
  if (window.__sakuraMountGalleryPost) return
  const url = getGalleryPostScriptUrl()
  if (!url) return

  if (!galleryPostScriptPromise) {
    galleryPostScriptPromise = new Promise((resolve, reject) => {
      const existing = document.querySelector(`script[src="${url}"]`)
      if (existing) {
        if (window.__sakuraMountGalleryPost) {
          resolve()
          return
        }
        existing.addEventListener('load', () => resolve(), { once: true })
        existing.addEventListener('error', () => reject(new Error('gallery-post script failed')), { once: true })
        return
      }

      const script = document.createElement('script')
      script.src = url
      script.defer = true
      script.onload = () => resolve()
      script.onerror = () => reject(new Error('gallery-post script failed'))
      document.body.appendChild(script)
    })
  }

  await galleryPostScriptPromise
}

export async function initGalleryPostModule() {
  if (!document.querySelector('.sakura-gallery-post-page')) return

  if (!window.__sakuraMountGalleryPost) {
    await loadGalleryPostScript()
  }
  window.__sakuraMountGalleryPost?.(registerPageCleanup)
}

/** PJAX：显示新页面前等待重页懒模块就绪，避免布局二次跳动 */
export async function awaitPageLazyModules() {
  const tasks = []
  if (document.querySelector('.sakura-moments-page')) tasks.push(initMomentsModule())
  if (document.querySelector('.sakura-excalidraw-page')) tasks.push(initExcalidrawModule())
  if (document.querySelector('.sakura-gallery-post-page')) tasks.push(initGalleryPostModule())
  if (!tasks.length) return
  await Promise.allSettled(tasks)
}
