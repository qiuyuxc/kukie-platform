import { mountMomentCommentPanel } from './comments.js'
import { formatRelativeTime } from './utils.js'

function primeMomentVideoThumb(video) {
  if (!video || video.dataset.thumbReady === '1') return
  video.dataset.thumbReady = '1'
  const seek = () => {
    try {
      if (Number.isFinite(video.duration) && video.duration > 0) {
        video.currentTime = Math.min(0.15, video.duration * 0.05)
      }
    } catch (_) { /* ignore seek errors */ }
  }
  if (video.readyState >= 1) seek()
  else video.addEventListener('loadedmetadata', seek, { once: true })
}

function bindMoments(root) {
  if (!root) return
  root.querySelectorAll('.travel-moment__photo video, .moments-card__photo video').forEach(primeMomentVideoThumb)
}

function initMomentsTimes(root) {
  root.querySelectorAll('.moments-card__meta time[datetime], .travel-moment__footer time[datetime]').forEach((timeEl) => {
    const absolute = timeEl.dataset.absolute || timeEl.textContent.trim()
    const isDateOnly = timeEl.hasAttribute('data-date-only')
    const formatted = formatRelativeTime(timeEl.getAttribute('datetime'), absolute, isDateOnly)
    timeEl.textContent = formatted
    if (formatted !== absolute) {
      timeEl.title = absolute
    } else {
      timeEl.removeAttribute('title')
    }
  })
}

function initMomentsComments(page) {
  const feed = page.querySelector('.moments-feed')
  if (!feed) return () => {}

  const controller = new AbortController()
  const { signal } = controller

  feed.addEventListener('click', async (event) => {
    const toggle = event.target.closest('.moment-comment-toggle')
    if (!toggle || !feed.contains(toggle)) return

    const entry = toggle.closest('.moments-card, .travel-moment')
    const panel = entry?.querySelector('.moment-comment-panel')
    if (!panel) return

    const wasOpen = !panel.hidden
    feed.querySelectorAll('.moment-comment-panel').forEach((item) => {
      item.hidden = true
    })
    feed.querySelectorAll('.moment-comment-toggle').forEach((item) => {
      item.classList.remove('is-active')
      item.setAttribute('aria-expanded', 'false')
    })
    if (wasOpen) return

    panel.hidden = false
    toggle.classList.add('is-active')
    toggle.setAttribute('aria-expanded', 'true')

    if (panel.dataset.loaded !== '1') {
      await mountMomentCommentPanel(panel)
      panel.dataset.loaded = '1'
    }
  }, { signal })

  return () => controller.abort()
}

function initMomentsLoadMore(page) {
  const feed = page.querySelector('.moments-feed')
  const dataEl = document.getElementById('moments-more-data')
  const sentinel = document.getElementById('moments-feed-sentinel')
  const statusEl = document.getElementById('moments-feed-status')
  if (!feed || !dataEl || !sentinel) return () => {}

  let templates = [...dataEl.querySelectorAll('template.moments-more-item')]
  const batchSize = Number(sentinel.dataset.batchSize) || 5
  let loading = false

  const setStatus = (mode) => {
    if (!statusEl) return
    if (mode === 'idle') {
      statusEl.hidden = true
      statusEl.textContent = ''
      statusEl.classList.remove('is-loading', 'is-done')
      return
    }
    statusEl.hidden = false
    statusEl.classList.toggle('is-loading', mode === 'loading')
    statusEl.classList.toggle('is-done', mode === 'done')
    statusEl.textContent = mode === 'loading' ? '加载中…' : '没有更多了'
  }

  const finish = () => {
    scrollObserver.disconnect()
    sentinel.remove()
    dataEl.remove()
    setStatus('done')
  }

  const loadNextBatch = () => {
    if (loading || !templates.length) return
    loading = true
    setStatus('loading')

    const batch = templates.slice(0, batchSize)
    templates = templates.slice(batchSize)

    const fragment = document.createDocumentFragment()
    batch.forEach((tpl) => {
      const node = tpl.content.firstElementChild?.cloneNode(true)
      if (node) fragment.appendChild(node)
      tpl.remove()
    })
    feed.insertBefore(fragment, sentinel)

    bindMoments(feed)
    initMomentsTimes(feed)

    loading = false
    setStatus('idle')

    if (!templates.length) {
      finish()
      return
    }

    if (sentinel.getBoundingClientRect().top <= window.innerHeight) {
      loadNextBatch()
    }
  }

  const scrollObserver = new IntersectionObserver((entries) => {
    if (entries.some((entry) => entry.isIntersecting)) loadNextBatch()
  }, {
    root: null,
    rootMargin: '240px 0px 0px',
    threshold: 0,
  })

  scrollObserver.observe(sentinel)

  return () => scrollObserver.disconnect()
}

export function initMomentsPage() {
  const page = document.querySelector('.sakura-moments-page')
  if (!page) return () => {}

  bindMoments(page)
  initMomentsTimes(page)
  const cleanupComments = initMomentsComments(page)
  const cleanupLoadMore = initMomentsLoadMore(page)

  return () => {
    cleanupComments()
    cleanupLoadMore()
  }
}
