
export function preservePjaxHistoryState(url) {
  history.replaceState(
    { ...(history.state || {}), pjax: true, scrollY: window.scrollY },
    '',
    url,
  )
}

export function escapeHtml(text) {
  return String(text)
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;')
}

export function stripHtml(html) {
  const tmp = document.createElement('div')
  tmp.innerHTML = html
  return tmp.textContent || tmp.innerText || ''
}

export function parseJsonData(el) {
  const raw = JSON.parse(el.textContent || '[]')
  return typeof raw === 'string' ? JSON.parse(raw) : raw
}

const CHINA_TZ_OFFSET = '+08:00'
const DATE_ONLY_PATTERN = /^\d{4}-\d{2}-\d{2}$/

export function parsePostDateTime(isoDate, isDateOnly = false) {
  if (!isoDate) return null

  const trimmed = String(isoDate).trim()
  if (!trimmed) return null

  const treatAsDateOnly = isDateOnly || DATE_ONLY_PATTERN.test(trimmed)
  const date = treatAsDateOnly
    ? new Date(`${trimmed}T12:00:00${CHINA_TZ_OFFSET}`)
    : new Date(trimmed)

  return Number.isNaN(date.getTime()) ? null : date
}

export function formatRelativeTime(isoDate, absoluteLabel, isDateOnly = false, maxDays = 7) {
  if (!isoDate) return absoluteLabel

  const date = parsePostDateTime(isoDate, isDateOnly)
  if (!date) return absoluteLabel

  const diffMs = Date.now() - date.getTime()
  const maxMs = maxDays * 24 * 60 * 60 * 1000
  if (diffMs < 0 || diffMs > maxMs) return absoluteLabel

  const diffMinutes = Math.floor(diffMs / 60000)
  if (diffMinutes < 1) return '刚刚'
  if (diffMinutes < 60) return `${diffMinutes} 分钟前`

  const diffHours = Math.floor(diffMinutes / 60)
  if (diffHours < 24) return `${diffHours} 小时前`

  const diffDays = Math.floor(diffHours / 24)
  return `${diffDays} 天前`
}

export function shuffleArray(list) {
  const arr = [...list]
  for (let i = arr.length - 1; i > 0; i -= 1) {
    const j = Math.floor(Math.random() * (i + 1))
    ;[arr[i], arr[j]] = [arr[j], arr[i]]
  }
  return arr
}
