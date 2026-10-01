
let typeWriterTimer = null

export function cancelTypeWriter() {
  if (typeWriterTimer) {
    clearTimeout(typeWriterTimer)
    typeWriterTimer = null
  }
}

export function typeWriter(el, text, speed = 80) {
  return new Promise((resolve) => {
    cancelTypeWriter()
    el.textContent = ''
    el.classList.add('is-typing')
    let i = 0
    const tick = () => {
      if (i >= text.length) {
        typeWriterTimer = null
        el.classList.remove('is-typing')
        resolve()
        return
      }
      el.textContent += text.charAt(i)
      i += 1
      typeWriterTimer = setTimeout(tick, speed)
    }
    tick()
  })
}
