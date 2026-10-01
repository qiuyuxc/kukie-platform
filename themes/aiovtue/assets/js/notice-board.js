
import { registerPageCleanup } from './page-cleanup.js'
import { escapeHtml, parseJsonData } from './utils.js'
import { renderNoticeCoverMedia, formatNoticeMetaLine } from './cover-media.js'
import { initLazyImages } from './lazy-images.js'

export function initNoticeBoard() {
  const postsDataEl = document.getElementById('notice-posts-data')
  const noticeArticle = document.getElementById('notice-article')
  const noticeWrap = document.querySelector('.notice-board-wrap')
  if (!postsDataEl || !noticeArticle || !noticeWrap) return

  let posts = []
  try {
    posts = parseJsonData(postsDataEl)
  } catch (err) {
    console.warn('[notice-board]', err)
    return
  }
  if (!posts.length) return

  let current = 0
  let timer
  let wheelLocked = false
  let isHovered = false
  const interval = Number(noticeWrap.dataset.interval || 5000)

  const normalizeIndex = (index) => ((index % posts.length) + posts.length) % posts.length

  const render = (index) => {
    current = normalizeIndex(index)
    const post = posts[current]
    if (!post) return

    noticeArticle.innerHTML = `
      <a class="notice-board-wrap__article-link" href="${escapeHtml(post.url)}" aria-label="查看文章：${escapeHtml(post.title)}"></a>
      <div class="notice-board-wrap__cover-col">
        <div class="notice-board-wrap__dots" role="tablist" aria-label="文章切换">
          <div class="notice-board-wrap__dot-center" aria-hidden="true"></div>
          <div class="notice-board-wrap__dots-track">
            ${[-2, -1, 0, 1, 2].map((offset) => `<button type="button" class="notice-board-wrap__dot${offset === 0 ? ' is-active' : ''}" data-offset="${offset}" role="tab" aria-label="切换文章"></button>`).join('')}
          </div>
        </div>
        <div class="notice-board-wrap__cover-frame">
          <div class="notice-board-wrap__cover">
            ${renderNoticeCoverMedia(post.cover, post.title)}
          </div>
        </div>
      </div>
      <div class="notice-board-wrap__meta-col">
        <div class="notice-board-wrap__meta">
          <time class="notice-board-wrap__date" datetime="${escapeHtml(post.date || '')}">${escapeHtml(post.date || '')}</time>
          <h2 class="notice-board-wrap__post-heading">${escapeHtml(post.title)}</h2>
          ${formatNoticeMetaLine(post.categories, post.tags)}
          <div class="notice-board-wrap__excerpt-slot">
            <p class="notice-board-wrap__excerpt">${escapeHtml(post.excerpt || '')}</p>
          </div>
        </div>
      </div>`

    noticeArticle.querySelectorAll('.notice-board-wrap__dot').forEach((dot) => {
      dot.addEventListener('click', (event) => {
        event.preventDefault()
        event.stopPropagation()
        const offset = Number(dot.dataset.offset || 0)
        if (offset) switchBy(offset)
      })
    })
    initLazyImages(noticeArticle)
  }

  const switchBy = (delta) => {
    if (!delta) return
    render(current + delta)
  }

  const startAutoRotate = () => {
    clearInterval(timer)
    if (posts.length <= 1 || isHovered) return
    timer = setInterval(() => {
      if (!isHovered) switchBy(1)
    }, interval)
  }

  const stopAutoRotate = () => {
    clearInterval(timer)
    timer = undefined
  }

  noticeArticle.addEventListener('mouseenter', () => {
    isHovered = true
    stopAutoRotate()
  })
  noticeArticle.addEventListener('mouseleave', () => {
    isHovered = false
    startAutoRotate()
  })
  noticeArticle.addEventListener('wheel', (event) => {
    if (posts.length <= 1 || wheelLocked) return
    event.preventDefault()
    if (Math.abs(event.deltaY) < 8) return
    wheelLocked = true
    switchBy(event.deltaY > 0 ? 1 : -1)
    window.setTimeout(() => { wheelLocked = false }, 320)
  }, { passive: false })

  render(0)
  if (posts.length > 1) {
    window.setTimeout(startAutoRotate, 3000)
  }

  registerPageCleanup(() => {
    stopAutoRotate()
  })
}
