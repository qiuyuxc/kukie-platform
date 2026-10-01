import { initMomentsPage } from './moments.js'

function mountMomentsFeatures(registerPageCleanup) {
  const register = typeof registerPageCleanup === 'function'
    ? registerPageCleanup
    : () => {}

  if (!document.querySelector('.sakura-moments-page')) return
  register(initMomentsPage())
}

window.__sakuraMountMoments = mountMomentsFeatures

const momentsScriptEl = document.currentScript
if (momentsScriptEl instanceof HTMLScriptElement) {
  momentsScriptEl.dataset.sakuraLoaded = '1'
}
