export function pickRssArticleBody(article) {
  let body = String(article.content || '').trim()
  const title = String(article.title || '').trim()
  const desc = String(article.description || '').trim()

  if (!body) return ''

  if (desc && (body === desc || body.startsWith(desc))) {
    body = body.slice(desc.length).trim()
  }
  if (title && (body === title || body.startsWith(title))) {
    body = body.slice(title.length).trim()
  }

  return body
}

export function getRssArticleDisplayParts(article, minLen = 80) {
  const desc = String(article.description || '').trim()
  const body = pickRssArticleBody(article)

  if (body.length >= minLen) {
    return { summary: desc, body }
  }

  const raw = String(article.content || '').trim()
  if (raw.length >= minLen) {
    return { summary: '', body: raw }
  }

  return { summary: desc, body: '' }
}

export function hasEnoughRssBody(article, minLen = 80) {
  const { summary, body } = getRssArticleDisplayParts(article, minLen)
  return (body || summary).length >= minLen
}
