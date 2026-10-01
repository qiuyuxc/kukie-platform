
const pageCleanups = []

export function registerPageCleanup(fn) {
  pageCleanups.push(fn)
}

export function runPageCleanups() {
  pageCleanups.forEach((fn) => {
    try { fn() } catch (_) { /* ignore */ }
  })
  pageCleanups.length = 0
}
