
import { createLightboxZoom } from './lightbox-zoom.js'

const lightboxState = {
  box: null,
  main: null,
  stage: null,
  thumbs: null,
  track: null,
  counter: null,
  items: [],
  index: 0,
  activeMedia: null,
  zoom: null,
  thumbWidth: 72,
  thumbStep: 82,
  show: null,
  closing: false,
  transitioning: false,
}

let lightboxScrollLockY = 0
let momentPhotoDelegationBound = false

function getLightboxRoot() {
  return document.documentElement
}

function bindMomentPhotoLightboxDelegation() {
  if (momentPhotoDelegationBound) return
  momentPhotoDelegationBound = true

  document.addEventListener('click', (event) => {
    const btn = event.target.closest('.travel-moment__photo[data-media-src], .moments-card__photo[data-media-src]')
    if (!btn) return

    const moment = btn.closest('.travel-moment, .moments-card')
    if (!moment) return

    const photos = [...moment.querySelectorAll('.travel-moment__photo[data-media-src], .moments-card__photo[data-media-src]')]
    const urls = photos.map((photo) => photo.dataset.mediaSrc).filter(Boolean)
    const index = photos.indexOf(btn)
    if (index < 0 || !urls.length) return

    event.preventDefault()
    event.stopPropagation()
    openSakuraLightbox(urls, index)
  }, true)
}

function lockLightboxScroll() {
  if (document.body.classList.contains('lightbox-open')) return
  lightboxScrollLockY = window.scrollY
  document.body.classList.add('lightbox-open')
}

function unlockLightboxScroll() {
  if (!document.body.classList.contains('lightbox-open')) return
  const restoreY = lightboxScrollLockY
  document.body.classList.remove('lightbox-open')
  lightboxScrollLockY = 0
  window.scrollTo(0, restoreY)
}

export function primeAlbumVideoThumb(video) {
  if (!video || video.dataset.thumbReady === '1') return
  video.dataset.thumbReady = '1'
  const seek = () => {
    try {
      if (Number.isFinite(video.duration) && video.duration > 0) {
        video.currentTime = Math.min(0.15, video.duration * 0.05)
      }
    } catch (_) {}
  }
  if (video.readyState >= 1) seek()
  else video.addEventListener('loadedmetadata', seek, { once: true })
}

export function initAlbumVideoThumbs(root = document) {
  root.querySelectorAll('.album-viewer__thumb-video').forEach(primeAlbumVideoThumb)
}

function collectGalleryPostItems(root = document) {
  return window.__sakuraCollectGalleryPostItems?.(root) ?? []
}

function appendGalleryPostLightboxItems(root = document) {
  appendLightboxItems(collectGalleryPostItems(root))
}

window.__sakuraAppendGalleryPostLightbox = appendGalleryPostLightboxItems

function collectAlbumViewerItems(root = document) {
  const items = []
  root.querySelectorAll('.album-viewer__photo[data-media-src]').forEach((el) => {
    if (el.dataset.lightboxBound === '1') return
    const src = el.dataset.mediaSrc
    if (!src) return
    const thumbImg = el.querySelector('img.album-viewer__thumb')
    items.push({
      el,
      src,
      type: el.dataset.mediaType === 'video' ? 'video' : 'image',
      poster: thumbImg?.src || '',
    })
  })
  return items
}

function collectMarkdownVideoItems(root = document) {
  const items = []
  root.querySelectorAll('.sakura-markdown-media-wrap--video .sakura-markdown-lightbox-btn').forEach((btn) => {
    if (btn.dataset.lightboxBound === '1') return
    const wrap = btn.closest('.sakura-markdown-media-wrap--video')
    const video = wrap?.querySelector('video.sakura-markdown-video')
    const src = video?.currentSrc || video?.src || video?.getAttribute('src')
    if (!src) return
    items.push({
      el: btn,
      src,
      type: 'video',
      poster: video?.poster || '',
    })
  })
  return items
}

function updateLightboxThumbMetrics() {
  const container = lightboxState.thumbs
  const thumb = container?.querySelector('.lightbox__thumb')
  const track = lightboxState.track
  if (!container || !thumb || !track) return

  lightboxState.thumbWidth = thumb.offsetWidth
  const gapValue = Number.parseFloat(getComputedStyle(track).columnGap || getComputedStyle(track).gap || '0')
  lightboxState.thumbStep = lightboxState.thumbWidth + (Number.isFinite(gapValue) ? gapValue : 0)
}

function updateLightboxThumbTrack() {
  updateLightboxThumbMetrics()
  const container = lightboxState.thumbs
  const track = lightboxState.track
  if (!container || !track) return

  const offset = container.clientWidth / 2 - lightboxState.thumbWidth / 2 - lightboxState.index * lightboxState.thumbStep
  track.style.transform = `translateX(${offset}px)`
}

function updateLightboxThumbUI() {
  lightboxState.track?.querySelectorAll('.lightbox__thumb').forEach((btn, i) => {
    if (i === lightboxState.index) btn.setAttribute('aria-current', 'true')
    else btn.removeAttribute('aria-current')
  })

  if (lightboxState.counter) {
    if (lightboxState.items.length > 1) {
      lightboxState.counter.hidden = false
      lightboxState.counter.textContent = `${lightboxState.index + 1} / ${lightboxState.items.length}`
    } else {
      lightboxState.counter.hidden = true
    }
  }

  const prev = lightboxState.box?.querySelector('.lightbox__prev')
  const next = lightboxState.box?.querySelector('.lightbox__next')
  if (prev) prev.hidden = lightboxState.items.length <= 1
  if (next) next.hidden = lightboxState.items.length <= 1

  updateLightboxThumbTrack()
}

function rebuildLightboxThumbs() {
  ensureLightbox()
  const track = lightboxState.track
  const thumbs = lightboxState.thumbs
  if (!track || !thumbs) return

  track.innerHTML = ''
  lightboxState.items.forEach((item, index) => {
    const btn = document.createElement('button')
    btn.type = 'button'
    btn.className = 'lightbox__thumb'
    btn.setAttribute('aria-label', `查看第 ${index + 1} 项`)

    const img = document.createElement('img')
    img.className = 'lightbox__thumb-media'
    if (item.type === 'video' && item.poster) {
      img.src = item.poster
    } else if (item.type !== 'video') {
      img.src = item.src
    } else {
      const video = document.createElement('video')
      video.className = 'lightbox__thumb-media'
      video.src = item.src
      video.muted = true
      video.playsInline = true
      video.preload = 'metadata'
      primeAlbumVideoThumb(video)
      btn.appendChild(video)
    }

    if (item.type !== 'video' || item.poster) {
      img.alt = ''
      img.loading = 'lazy'
      img.decoding = 'async'
      btn.appendChild(img)
    }

    if (item.type === 'video') {
      const play = document.createElement('span')
      play.className = 'lightbox__thumb-play'
      play.setAttribute('aria-hidden', 'true')
      play.innerHTML = '<svg viewBox="0 0 24 24"><path d="M8 5v14l11-7z" /></svg>'
      btn.appendChild(play)
    }

    btn.addEventListener('click', (e) => {
      e.stopPropagation()
      lightboxState.show(index)
    })
    track.appendChild(btn)
  })

  thumbs.hidden = lightboxState.items.length <= 1
  updateLightboxThumbUI()
}

function ensureLightbox() {
  if (lightboxState.box) return

  const box = document.createElement('div')
  box.className = 'lightbox'
  box.hidden = true
  box.innerHTML = `
    <div class="lightbox__main">
      <button class="lightbox__close" type="button" aria-label="关闭">×</button>
      <button class="lightbox__prev" type="button" aria-label="上一张">‹</button>
      <button class="lightbox__next" type="button" aria-label="下一张">›</button>
      <span class="lightbox__counter" hidden></span>
      <div class="lightbox__stage-wrap">
        <div class="lightbox__stage"></div>
      </div>
    </div>
    <div class="lightbox__thumbs" hidden>
      <div class="lightbox__thumbs-indicator" aria-hidden="true"></div>
      <div class="lightbox__thumbs-track"></div>
    </div>
  `
  getLightboxRoot().appendChild(box)

  lightboxState.box = box
  lightboxState.main = box.querySelector('.lightbox__main')
  lightboxState.stage = box.querySelector('.lightbox__stage')
  lightboxState.thumbs = box.querySelector('.lightbox__thumbs')
  lightboxState.track = box.querySelector('.lightbox__thumbs-track')
  lightboxState.counter = box.querySelector('.lightbox__counter')

  const clearStage = () => {
    if (lightboxState.activeMedia?.tagName === 'VIDEO') {
      lightboxState.activeMedia.pause()
    }
    lightboxState.zoom?.destroy()
    lightboxState.zoom = null
    if (lightboxState.stage) {
      lightboxState.stage.innerHTML = ''
      lightboxState.stage.classList.remove('is-transitioning')
      lightboxState.stage.style.minHeight = ''
    }
    lightboxState.activeMedia = null
  }

  const removeMediaLayer = (el) => {
    if (!el) return
    if (el.tagName === 'VIDEO') el.pause()
    el.remove()
  }

  const preloadLightboxImage = (src) => new Promise((resolve) => {
    const img = new Image()
    const finish = () => {
      if (typeof img.decode === 'function') {
        img.decode().then(resolve).catch(resolve)
      } else {
        resolve()
      }
    }
    img.onload = finish
    img.onerror = finish
    img.src = src
  })

  const createMediaElements = (item) => {
    if (item.type === 'video') {
      const video = document.createElement('video')
      video.className = 'lightbox__media'
      video.src = item.src
      video.controls = true
      video.autoplay = true
      video.playsInline = true
      if (item.poster) video.poster = item.poster
      return { animatedEl: video, activeMedia: video, zoom: null }
    }

    const viewport = document.createElement('div')
    viewport.className = 'lightbox__viewport'
    const img = document.createElement('img')
    img.className = 'lightbox__media lightbox__img'
    img.alt = ''
    img.src = item.src
    img.draggable = false
    viewport.appendChild(img)
    return { animatedEl: viewport, activeMedia: img, zoom: null }
  }

  const lockStageSize = () => {
    const stage = lightboxState.stage
    if (!stage) return
    const rect = stage.getBoundingClientRect()
    stage.classList.add('is-transitioning')
    if (rect.height > 0) stage.style.minHeight = `${rect.height}px`
  }

  const unlockStageSize = () => {
    const stage = lightboxState.stage
    if (!stage) return
    stage.classList.remove('is-transitioning')
    stage.style.minHeight = ''
  }

  const isLightboxZoomed = () => lightboxState.zoom?.isZoomed?.() ?? false

  const prefersReducedMotion = () => window.matchMedia('(prefers-reduced-motion: reduce)').matches

  const getSlideDirection = (from, to, len) => {
    if (from === to) return 'none'
    const forward = (to - from + len) % len
    return forward <= len / 2 ? 'next' : 'prev'
  }

  const runMediaExitAnimation = (el, direction) => {
    if (!el || prefersReducedMotion()) return Promise.resolve()
    const exitClass = direction === 'next' ? 'is-exiting-left' : 'is-exiting-right'
    el.classList.remove(
      'is-entering',
      'is-entering-from-right',
      'is-entering-from-left',
      'is-exiting-left',
      'is-exiting-right',
    )
    el.classList.add(exitClass)
    return new Promise((resolve) => {
      let done = false
      const finish = () => {
        if (done) return
        done = true
        el.removeEventListener('animationend', onEnd)
        resolve()
      }
      const onEnd = (event) => {
        if (event.target !== el) return
        finish()
      }
      el.addEventListener('animationend', onEnd)
      window.setTimeout(finish, 240)
    })
  }

  const animateLightboxMedia = (el, direction = 'none') => {
    if (!el || prefersReducedMotion()) return
    el.classList.remove(
      'is-entering',
      'is-entering-from-right',
      'is-entering-from-left',
      'is-exiting-left',
      'is-exiting-right',
    )
    void el.offsetWidth
    if (direction === 'next') el.classList.add('is-entering-from-right')
    else if (direction === 'prev') el.classList.add('is-entering-from-left')
    else el.classList.add('is-entering')
  }

  const finishLightboxClose = () => {
    lightboxState.closing = false
    clearStage()
    box.hidden = true
    box.classList.remove('is-closing', 'is-open')
    document.documentElement.classList.remove('image-gallery-open')
    unlockLightboxScroll()
  }

  const openLightboxShell = () => {
    if (!box.parentNode) getLightboxRoot().appendChild(box)
    if (!box.hidden && box.classList.contains('is-open')) return
    box.hidden = false
    box.classList.remove('is-closing')
    lockLightboxScroll()
    document.documentElement.classList.add('image-gallery-open')
    if (prefersReducedMotion()) {
      box.classList.add('is-open')
      return
    }
    box.classList.remove('is-open')
    requestAnimationFrame(() => {
      box.classList.add('is-open')
    })
  }

  const show = async (i) => {
    if (!lightboxState.items.length || lightboxState.closing || lightboxState.transitioning) return

    const wasHidden = box.hidden
    const prevIndex = lightboxState.index
    const newIndex = (i + lightboxState.items.length) % lightboxState.items.length
    const direction = wasHidden || prevIndex === newIndex
      ? 'none'
      : getSlideDirection(prevIndex, newIndex, lightboxState.items.length)

    lightboxState.transitioning = true
    try {
      lightboxState.index = newIndex
      const item = lightboxState.items[lightboxState.index]
      const oldMedia = lightboxState.stage?.querySelector('.lightbox__viewport, video.lightbox__media')

      if (item.type === 'image') {
        await preloadLightboxImage(item.src)
      }

      const { animatedEl, activeMedia } = createMediaElements(item)
      openLightboxShell()

      if (oldMedia && direction !== 'none') {
        lightboxState.zoom?.destroy()
        lightboxState.zoom = null

        lockStageSize()
        oldMedia.classList.add('lightbox__slide-layer')
        animatedEl.classList.add('lightbox__slide-layer')
        lightboxState.stage.appendChild(animatedEl)

        lightboxState.activeMedia = activeMedia
        if (activeMedia.tagName === 'IMG') {
          lightboxState.zoom = createLightboxZoom(activeMedia, animatedEl)
        }

        animateLightboxMedia(animatedEl, direction)
        await runMediaExitAnimation(oldMedia, direction)

        removeMediaLayer(oldMedia)
        animatedEl.classList.remove('lightbox__slide-layer')
        unlockStageSize()
      } else {
        clearStage()
        lightboxState.stage.appendChild(animatedEl)
        lightboxState.activeMedia = activeMedia
        if (activeMedia.tagName === 'IMG') {
          lightboxState.zoom = createLightboxZoom(activeMedia, animatedEl)
        }
        animateLightboxMedia(animatedEl, wasHidden ? 'none' : direction)
      }

      updateLightboxThumbUI()
      requestAnimationFrame(() => updateLightboxThumbTrack())
    } finally {
      lightboxState.transitioning = false
    }
  }

  const hide = () => {
    if (box.hidden || lightboxState.closing) return
    if (prefersReducedMotion()) {
      finishLightboxClose()
      return
    }
    lightboxState.closing = true
    box.classList.remove('is-open')
    box.classList.add('is-closing')
    const onAnimEnd = (event) => {
      if (event.target !== box) return
      box.removeEventListener('animationend', onAnimEnd)
      finishLightboxClose()
    }
    box.addEventListener('animationend', onAnimEnd)
    window.setTimeout(finishLightboxClose, 320)
  }

  box.querySelector('.lightbox__close')?.addEventListener('click', hide)
  box.querySelector('.lightbox__prev')?.addEventListener('click', (e) => {
    e.stopPropagation()
    show(lightboxState.index - 1)
  })
  box.querySelector('.lightbox__next')?.addEventListener('click', (e) => {
    e.stopPropagation()
    show(lightboxState.index + 1)
  })
  lightboxState.main?.addEventListener('click', (e) => {
    if (e.target === lightboxState.main) hide()
  })
  box.addEventListener('click', (e) => {
    if (e.target === box) hide()
  })

  window.addEventListener('keydown', (e) => {
    if (box.hidden || lightboxState.closing) return
    if (e.key === 'Escape') hide()
    if (isLightboxZoomed()) return
    if (e.key === 'ArrowLeft') {
      e.preventDefault()
      show(lightboxState.index - 1)
    }
    if (e.key === 'ArrowRight') {
      e.preventDefault()
      show(lightboxState.index + 1)
    }
  })

  let swipeStartX = 0
  let swipeStartY = 0
  let swipeTracking = false

  lightboxState.stage?.addEventListener('touchstart', (e) => {
    if (isLightboxZoomed() || e.touches.length !== 1) return
    swipeStartX = e.touches[0].clientX
    swipeStartY = e.touches[0].clientY
    swipeTracking = true
  }, { passive: true })

  lightboxState.stage?.addEventListener('touchend', (e) => {
    if (!swipeTracking) return
    swipeTracking = false
    if (isLightboxZoomed()) return
    const touch = e.changedTouches[0]
    if (!touch) return
    const deltaX = touch.clientX - swipeStartX
    const deltaY = touch.clientY - swipeStartY
    if (Math.abs(deltaX) < 48 || Math.abs(deltaY) > Math.abs(deltaX)) return
    if (deltaX > 0) show(lightboxState.index - 1)
    else show(lightboxState.index + 1)
  }, { passive: true })

  lightboxState.stage?.addEventListener('touchcancel', () => {
    swipeTracking = false
  }, { passive: true })

  window.addEventListener('resize', () => {
    if (!box.hidden) updateLightboxThumbTrack()
  })

  lightboxState.show = show
}

function isMediaVideoUrl(url) {
  return /\.(mp4|webm|ogg)(\?.*)?$/i.test(String(url || ''))
}

function urlToLightboxItem(url) {
  const src = String(url || '').trim()
  if (!src) return null
  const isVideo = isMediaVideoUrl(src)
  return {
    type: isVideo ? 'video' : 'image',
    src,
    poster: isVideo ? '' : src,
  }
}

export function openSakuraLightbox(urls, startIndex = 0) {
  const list = (urls || []).map(urlToLightboxItem).filter(Boolean)
  if (!list.length) return
  ensureLightbox()
  lightboxState.items = list
  rebuildLightboxThumbs()
  lightboxState.show(Math.max(0, Math.min(startIndex, list.length - 1)))
}

function appendLightboxItems(newItems) {
  if (!newItems.length) return
  ensureLightbox()
  const start = lightboxState.items.length
  lightboxState.items.push(...newItems)
  newItems.forEach((item, offset) => {
    item.el.dataset.lightboxBound = '1'
    item.el.addEventListener('click', (e) => {
      e.preventDefault()
      e.stopPropagation()
      lightboxState.show(start + offset)
    })
  })
  rebuildLightboxThumbs()
}

export function appendAlbumViewerLightboxItems(viewer) {
  appendLightboxItems(collectAlbumViewerItems(viewer))
}

export function initLightbox() {
  bindMomentPhotoLightboxDelegation()

  const items = []

  document.querySelectorAll('.markdown-body img').forEach((el) => {
    if (!el.src || el.dataset.lightboxBound === '1') return
    items.push({ el, src: el.src, type: 'image', poster: el.src })
  })

  document.querySelectorAll('[data-lightbox="album"]').forEach((el) => {
    if (el.dataset.lightboxBound === '1') return
    const src = el.dataset.src || el.src || el.getAttribute('href')
    if (!src) return
    items.push({ el, src, type: 'image', poster: src })
  })

  if (!document.querySelector('.sakura-gallery-album-page.is-locked')) {
    items.push(...collectAlbumViewerItems())
  }

  items.push(...collectGalleryPostItems())

  items.push(...collectMarkdownVideoItems())

  appendLightboxItems(items)
}

export function cleanupLightbox() {
  if (lightboxState.box && !lightboxState.box.hidden) {
    lightboxState.box.hidden = true
    lightboxState.box.classList.remove('is-open', 'is-closing')
  }
  document.documentElement.classList.remove('image-gallery-open')
  unlockLightboxScroll()
  lightboxState.items = []
  lightboxState.index = 0
  lightboxState.closing = false
  lightboxState.transitioning = false
}
