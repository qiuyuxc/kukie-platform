
function normalizeSidebarPath(path) {
  if (path.length > 1 && path.endsWith('/')) return path.slice(0, -1)
  return path
}

function isSidebarLinkActive(linkPath, currentPath) {
  if (linkPath === '/') return currentPath === '/'
  return currentPath === linkPath || currentPath.startsWith(`${linkPath}/`)
}

function setSidebarGroupOpen(toggle, subItems, open) {
  toggle.setAttribute('aria-expanded', open ? 'true' : 'false')
  subItems.hidden = !open
  toggle.querySelector('.sidebar-nav-group__chevron')?.classList.toggle('is-open', open)
}

export function updateSidebarNavActive() {
  const sidebar = document.getElementById('sidebar')
  if (!sidebar) return

  const currentPath = normalizeSidebarPath(window.location.pathname)

  sidebar.querySelectorAll('.sakura-sidebar-link-items .sakura-nav-link[href]').forEach((link) => {
    const href = link.getAttribute('href')
    if (!href) return
    let linkPath
    try {
      linkPath = normalizeSidebarPath(new URL(href, window.location.origin).pathname)
    } catch (_) {
      return
    }
    link.classList.toggle('is-active', isSidebarLinkActive(linkPath, currentPath))
  })

  sidebar.querySelectorAll('.sidebar-nav-group__toggle').forEach((toggle) => {
    const subItems = toggle.parentElement?.querySelector('.sakura-sidebar-link-sub-items')
    if (!subItems) return
    const hasActiveChild = [...subItems.querySelectorAll('.sakura-nav-link')].some((link) => link.classList.contains('is-active'))
    setSidebarGroupOpen(toggle, subItems, hasActiveChild)
  })
}

export function initSidebar(closeSidebar) {
  const sidebar = document.getElementById('sidebar')
  if (!sidebar) return

  if (sidebar.dataset.shellReady !== '1') {
    sidebar.dataset.shellReady = '1'

    sidebar.querySelectorAll('.sidebar-nav-group__toggle').forEach((toggle) => {
      const subItems = toggle.parentElement?.querySelector('.sakura-sidebar-link-sub-items')
      if (!subItems) return

      toggle.addEventListener('click', () => {
        const isOpen = toggle.getAttribute('aria-expanded') === 'true'
        setSidebarGroupOpen(toggle, subItems, !isOpen)
      })
    })

    sidebar.querySelectorAll('.sakura-sidebar-link .sakura-nav-link').forEach((link) => {
      link.addEventListener('click', () => closeSidebar(false))
    })
  }

  updateSidebarNavActive()
}
