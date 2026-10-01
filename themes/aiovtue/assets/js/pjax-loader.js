const LOADER_ID = 'sakura-pjax-loader'
const HIDE_MS = 240
const ECG_SELECTOR = '.sakura-pjax-loader__ecg-glow, .sakura-pjax-loader__ecg-line'

let loaderVisible = false
let hideTimer = 0

function getLoaderEl() {
  return document.getElementById(LOADER_ID)
}

export function isPjaxLoaderEnabled() {
  const meta = document.querySelector('meta[name="sakura-pjax"]')
  if (!meta || meta.content !== '1') return false
  return meta.getAttribute('data-loader') !== '0'
}

function resetEcgToStart(loaderEl) {
  loaderEl.querySelectorAll(ECG_SELECTOR).forEach((node) => {
    node.style.animation = 'none'
    node.style.strokeDashoffset = '80'
  })
}

function restartEcgAnimation(loaderEl) {
  loaderEl.querySelectorAll(ECG_SELECTOR).forEach((node) => {
    node.style.animation = 'none'
    node.style.strokeDashoffset = '80'
    void node.getBoundingClientRect()
    node.style.animation = ''
    node.style.strokeDashoffset = ''
  })
}

export function showPjaxLoader() {
  if (!isPjaxLoaderEnabled()) return
  const el = getLoaderEl()
  if (!el) return

  if (hideTimer) {
    window.clearTimeout(hideTimer)
    hideTimer = 0
  }

  loaderVisible = true
  el.hidden = false
  el.classList.remove('is-hidden')
  el.setAttribute('aria-hidden', 'false')
  document.body.classList.add('is-pjax-loading')
  restartEcgAnimation(el)
}

export function hidePjaxLoader() {
  const el = getLoaderEl()
  document.body.classList.remove('is-pjax-loading')
  if (!el || !loaderVisible) return

  loaderVisible = false
  el.classList.add('is-hidden')
  el.setAttribute('aria-hidden', 'true')
  resetEcgToStart(el)

  hideTimer = window.setTimeout(() => {
    hideTimer = 0
    if (!loaderVisible) el.hidden = true
  }, HIDE_MS)
}
