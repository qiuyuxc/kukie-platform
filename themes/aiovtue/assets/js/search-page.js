
import { registerPageCleanup } from './page-cleanup.js'
import { escapeHtml, parseJsonData } from './utils.js'
import { renderNoticeCoverMedia } from './cover-media.js'
import { initLazyImages } from './lazy-images.js'
import { primeAlbumVideoThumb } from './lightbox.js'

const FUSE_SCRIPT_URL = 'https://cdn.jsdelivr.net/npm/fuse.js@7.0.0/dist/fuse.min.js'
let fuseScriptPromise = null

function loadFuseScript() {
  if (window.Fuse) return Promise.resolve(window.Fuse)
  if (fuseScriptPromise) return fuseScriptPromise

  fuseScriptPromise = new Promise((resolve, reject) => {
    const existing = document.querySelector(`script[src="${FUSE_SCRIPT_URL}"]`)
    if (existing) {
      if (window.Fuse) {
        resolve(window.Fuse)
        return
      }
      existing.addEventListener('load', () => resolve(window.Fuse), { once: true })
      existing.addEventListener('error', () => reject(new Error('fuse load failed')), { once: true })
      return
    }

    const script = document.createElement('script')
    script.src = FUSE_SCRIPT_URL
    script.defer = true
    script.onload = () => resolve(window.Fuse)
    script.onerror = () => reject(new Error('fuse load failed'))
    document.body.appendChild(script)
  })

  return fuseScriptPromise
}

function renderSearchResultItem(item) {
  const title = escapeHtml(item.title || '')
  const url = escapeHtml(item.url || '#')
  const excerpt = escapeHtml(item.excerpt || '')
  const date = escapeHtml(item.date || '')
  const coverMedia = item.cover ? renderNoticeCoverMedia(item.cover, item.title) : ''
  return `<article class="search-result-entry">
    <a class="search-result-entry__cover" href="${url}">
      <span class="search-result-entry__overlay" aria-hidden="true">
        <iconify-icon icon="fa6-regular:file-lines"></iconify-icon>
      </span>
      ${coverMedia}
    </a>
    <div class="search-result-entry__body">
      <div class="search-result-entry__head">
        <h3 class="search-result-entry__title"><a href="${url}">${title}</a></h3>
        <div class="search-result-entry__date">
          <iconify-icon icon="mdi:clock-outline" aria-hidden="true"></iconify-icon>
          发布于 ${date}
        </div>
      </div>
      <p class="search-result-entry__excerpt">${excerpt}</p>
      <hr class="search-result-entry__divider">
    </div>
  </article>`
}

export async function initSearchPage() {
  const dataEl = document.getElementById('search-index-data')
  const input = document.getElementById('search-page-input')
  const results = document.getElementById('search-results')
  if (!dataEl || !input || !results) return

  let Fuse
  try {
    Fuse = await loadFuseScript()
  } catch (err) {
    console.warn('[search]', err)
    results.innerHTML = '<p class="search-results-empty">搜索组件加载失败，请检查网络或刷新重试。</p>'
    return
  }
  if (!Fuse) return

  try {
    const list = parseJsonData(dataEl)
    const fuse = new Fuse(list, {
      keys: ['title', 'tags', 'categories', 'excerpt'],
      threshold: Number.parseFloat(document.documentElement.dataset.searchThreshold || '') || 0.35,
      ignoreLocation: true,
    })
    const render = (items) => {
      results.innerHTML = items.length
        ? items.map(({ item }) => renderSearchResultItem(item)).join('')
        : '<p class="search-results-empty">没有找到相关内容</p>'
      initLazyImages(results)
      results.querySelectorAll('video.sakura-cover-thumb').forEach(primeAlbumVideoThumb)
    }
    const run = (q) => render(q ? fuse.search(q) : [])
    const onInput = () => run(input.value.trim())
    input.addEventListener('input', onInput)
    const q = new URLSearchParams(location.search).get('q')
    if (q) { input.value = q; run(q) }

    registerPageCleanup(() => {
      input.removeEventListener('input', onInput)
    })
  } catch (err) { console.warn('[search]', err) }
}
