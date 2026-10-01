
import { navigateToUrl } from './page-nav.js'

export function initSearchModal() {
  const modal = document.getElementById('search-modal')
  const searchBtn = document.getElementById('search-open')
  if (!modal || !searchBtn || modal.dataset.shellReady === '1') return
  modal.dataset.shellReady = '1'

  const lockScrollForSearch = () => {
    const sw = Math.max(0, window.innerWidth - document.documentElement.clientWidth)
    document.documentElement.style.setProperty('--search-scroll-lock-padding', `${sw}px`)
    document.body.classList.add('is-scroll-locked')
    document.getElementById('navbar')?.classList.add('is-scroll-locked')
  }
  const unlockScrollForSearch = () => {
    document.body.classList.remove('is-scroll-locked')
    document.getElementById('navbar')?.classList.remove('is-scroll-locked')
    document.documentElement.style.removeProperty('--search-scroll-lock-padding')
  }
  const syncHomeNavbarForSearch = (open) => {
    if (!open || !document.documentElement.classList.contains('is-home')) return
    const links = document.getElementById('navbar-links')
    if (!links) return
    links.classList.remove(
      'sakura-fade-in-left', 'sakura-fade-out-left',
      'sakura-fade-in-right', 'sakura-fade-out-right',
    )
    links.style.removeProperty('transform')
  }
  const isSearchOpen = () => modal && !modal.hidden && !modal.classList.contains('is-closing')
  let searchClosing = false
  const finishSearchClose = () => {
    if (modal.hidden) return
    searchClosing = false
    modal.hidden = true
    modal.classList.remove('is-closing', 'is-open')
    unlockScrollForSearch()
    document.documentElement.classList.remove('is-search-open')
    window.dispatchEvent(new Event('scroll'))
    document.getElementById('search-modal-input')?.blur()
  }
  const setSearchOpen = (open) => {
    if (open) {
      if (searchClosing) return
      modal.hidden = false
      modal.classList.remove('is-closing')
      requestAnimationFrame(() => {
        modal.classList.add('is-open')
      })
      lockScrollForSearch()
      document.documentElement.classList.add('is-search-open')
      syncHomeNavbarForSearch(true)
      searchBtn?.classList.add('is-search-open')
      setTimeout(() => document.getElementById('search-modal-input')?.focus(), 0)
    } else {
      if (modal.hidden || searchClosing) return
      if (window.matchMedia('(prefers-reduced-motion: reduce)').matches) {
        searchBtn?.classList.remove('is-search-open')
        finishSearchClose()
      } else {
        searchClosing = true
        modal.classList.add('is-closing')
        modal.classList.remove('is-open')
        searchBtn?.classList.remove('is-search-open')
        const onAnimEnd = (event) => {
          if (event.target !== modal) return
          modal.removeEventListener('animationend', onAnimEnd)
          finishSearchClose()
        }
        modal.addEventListener('animationend', onAnimEnd)
        setTimeout(finishSearchClose, 280)
      }
    }
    searchBtn?.setAttribute('aria-expanded', open ? 'true' : 'false')
    searchBtn?.setAttribute('aria-label', open ? '关闭搜索' : '搜索')
  }
  const closeSearch = () => setSearchOpen(false)
  searchBtn?.addEventListener('click', () => {
    if (searchClosing) return
    if (isSearchOpen()) closeSearch()
    else setSearchOpen(true)
  })
  document.getElementById('search-modal-input')?.addEventListener('keydown', (e) => {
    if (e.key === 'Enter') {
      const q = e.target.value.trim()
      if (!q) return
      e.preventDefault()
      e.target.blur()
      searchClosing = false
      modal.hidden = true
      modal.classList.remove('is-closing', 'is-open')
      unlockScrollForSearch()
      document.documentElement.classList.remove('is-search-open')
      searchBtn?.classList.remove('is-search-open')
      searchBtn?.setAttribute('aria-expanded', 'false')
      searchBtn?.setAttribute('aria-label', '搜索')
      navigateToUrl(`/search/?q=${encodeURIComponent(q)}`)
    }
    if (e.key === 'Escape') closeSearch()
  })
  modal?.addEventListener('click', (e) => { if (e.target === modal) closeSearch() })

  document.addEventListener('sakura:close-search', () => {
    if (!modal || modal.hidden) return
    searchClosing = false
    finishSearchClose()
  })
}
