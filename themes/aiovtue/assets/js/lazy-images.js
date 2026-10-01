export function initLazyImages(root = document) {
  root.querySelectorAll('.markdown-body img:not([loading])').forEach((img) => {
    img.loading = 'lazy'
    img.decoding = 'async'
    img.classList.add('sakura-lazy-img')
  })

  root.querySelectorAll('img.sakura-lazy-img, img[loading="lazy"]').forEach((img) => {
    if (img.dataset.lazyReady) return
    img.dataset.lazyReady = '1'
    const reveal = () => img.classList.add('is-loaded')
    if (img.complete && img.naturalWidth > 0) reveal()
    else {
      img.addEventListener('load', reveal, { once: true })
      img.addEventListener('error', reveal, { once: true })
    }
  })
}
