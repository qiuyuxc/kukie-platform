
import { escapeHtml } from './utils.js'

export function isCoverVideo(cover) {
  if (!cover) return false
  const lower = cover.toLowerCase().split('?')[0].split('#')[0]
  return lower.endsWith('.mp4') || lower.endsWith('.webm') || lower.endsWith('.ogg')
}

export function renderNoticeCoverMedia(cover, title) {
  if (!cover) return ''
  const src = escapeHtml(cover)
  const safeTitle = escapeHtml(title)
  if (isCoverVideo(cover)) {
    return `<video class="sakura-cover-thumb sakura-lazy-img" src="${src}" muted playsinline preload="metadata" disablepictureinpicture aria-hidden="true"></video>`
  }
  return `<img class="sakura-lazy-img" src="${src}" alt="${safeTitle}" loading="eager" fetchpriority="high" decoding="async">`
}

export function formatNoticeMetaLine(categories, tags) {
  const cats = Array.isArray(categories) ? categories.join(' / ') : (categories || '')
  const tagList = Array.isArray(tags) ? tags.join(' · ') : (tags || '')
  if (!cats && !tagList) return ''
  if (cats && tagList) {
    return `<p class="notice-board-wrap__meta-line"><span class="notice-board-wrap__meta-item">${escapeHtml(cats)}</span><span class="notice-board-wrap__meta-sep">·</span><span class="notice-board-wrap__meta-item">${escapeHtml(tagList)}</span></p>`
  }
  const text = cats || tagList
  return `<p class="notice-board-wrap__meta-line"><span class="notice-board-wrap__meta-item">${escapeHtml(text)}</span></p>`
}
