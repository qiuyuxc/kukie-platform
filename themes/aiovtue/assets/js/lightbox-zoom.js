const MIN_SCALE = 1
const MAX_SCALE = 4
const DOUBLE_TAP_SCALE = 2
const DBLCLICK_TOUCH_GUARD_MS = 700
const DRAG_START_THRESHOLD = 6
const WHEEL_ZOOM_INTENSITY = 0.002

function getTouchDistance(touches) {
  if (touches.length < 2) return 0
  const dx = touches[0].clientX - touches[1].clientX
  const dy = touches[0].clientY - touches[1].clientY
  return Math.hypot(dx, dy)
}

function getTouchMidpoint(touches) {
  return {
    x: (touches[0].clientX + touches[1].clientX) / 2,
    y: (touches[0].clientY + touches[1].clientY) / 2,
  }
}

export function createLightboxZoom(imageEl, viewportEl) {
  let currentScale = MIN_SCALE
  let isDragging = false
  let isPinching = false
  let isWheelZooming = false
  let panOffset = { x: 0, y: 0 }

  let dragStartX = 0
  let dragStartY = 0
  let startPanX = 0
  let startPanY = 0
  let pendingDrag = false
  let activePointerId = null
  let dragCaptureTarget = null

  let pinchStartDistance = 0
  let pinchStartScale = MIN_SCALE
  let pinchStartPan = { x: 0, y: 0 }
  let pinchStartFocal = { x: 0, y: 0 }
  let pinchLayoutCenter = { x: 0, y: 0 }
  let suppressDblClickUntil = 0
  let wheelZoomEndTimer = null

  const listeners = []

  function on(target, type, handler, options) {
    target.addEventListener(type, handler, options)
    listeners.push([target, type, handler, options])
  }

  function isZoomed() {
    return currentScale > MIN_SCALE + 0.01
  }

  function markTouchGesture() {
    suppressDblClickUntil = Date.now() + DBLCLICK_TOUCH_GUARD_MS
  }

  function getLayoutCenter() {
    const rect = imageEl.getBoundingClientRect()
    if (rect.width === 0 || rect.height === 0) return null
    return {
      x: rect.left + rect.width / 2 - panOffset.x,
      y: rect.top + rect.height / 2 - panOffset.y,
    }
  }

  function applyTransform() {
    if (currentScale <= MIN_SCALE && panOffset.x === 0 && panOffset.y === 0) {
      imageEl.style.transform = ''
      imageEl.style.transition = ''
      viewportEl.classList.remove('is-zoomed', 'is-dragging', 'is-pinching')
      imageEl.classList.remove('is-zoomed')
      return
    }

    const interacting = isDragging || isPinching || isWheelZooming
    imageEl.style.transform = `translate(${panOffset.x}px, ${panOffset.y}px) scale(${currentScale})`
    imageEl.style.transformOrigin = 'center center'
    imageEl.style.transition = interacting ? 'none' : 'transform 0.25s ease'
    viewportEl.classList.toggle('is-zoomed', isZoomed())
    imageEl.classList.toggle('is-zoomed', isZoomed())
    viewportEl.classList.toggle('is-dragging', isDragging)
    viewportEl.classList.toggle('is-pinching', isPinching)
  }

  function adjustPanForScaleChange(focalX, focalY, oldScale, newScale) {
    const center = getLayoutCenter()
    if (!center) return
    panOffset = {
      x: focalX - center.x - (focalX - center.x - panOffset.x) * (newScale / oldScale),
      y: focalY - center.y - (focalY - center.y - panOffset.y) * (newScale / oldScale),
    }
  }

  function setScaleAt(focalX, focalY, newScale) {
    const oldScale = currentScale
    const clamped = Math.min(MAX_SCALE, Math.max(MIN_SCALE, newScale))
    if (Math.abs(clamped - oldScale) < 0.001) return
    adjustPanForScaleChange(focalX, focalY, oldScale, clamped)
    currentScale = clamped
    applyTransform()
  }

  function resetZoomState() {
    currentScale = MIN_SCALE
    isDragging = false
    isPinching = false
    isWheelZooming = false
    pendingDrag = false
    activePointerId = null
    dragCaptureTarget = null
    panOffset = { x: 0, y: 0 }
    pinchStartDistance = 0
    applyTransform()
  }

  function releasePointerCapture() {
    if (dragCaptureTarget && activePointerId !== null) {
      try {
        dragCaptureTarget.releasePointerCapture(activePointerId)
      } catch (_) {}
    }
    activePointerId = null
    dragCaptureTarget = null
  }

  function zoomImageAt(clientX, clientY) {
    if (isZoomed()) {
      resetZoomState()
      return
    }

    const rect = imageEl.getBoundingClientRect()
    if (rect.width === 0 || rect.height === 0) return
    setScaleAt(clientX, clientY, DOUBLE_TAP_SCALE)
  }

  function onImageDblClick(event) {
    if (Date.now() < suppressDblClickUntil) {
      event.preventDefault()
      return
    }
    pendingDrag = false
    isDragging = false
    releasePointerCapture()
    zoomImageAt(event.clientX, event.clientY)
  }

  function onPointerDown(event) {
    if (event.pointerType === 'touch') markTouchGesture()
    if (!isZoomed() || isPinching || event.button !== 0) return

    pendingDrag = true
    activePointerId = event.pointerId
    dragCaptureTarget = event.currentTarget
    dragStartX = event.clientX
    dragStartY = event.clientY
    startPanX = panOffset.x
    startPanY = panOffset.y
  }

  function onPointerMove(event) {
    if (!pendingDrag && !isDragging) return
    if (activePointerId !== null && event.pointerId !== activePointerId) return

    if (pendingDrag && !isDragging) {
      const dx = event.clientX - dragStartX
      const dy = event.clientY - dragStartY
      if (Math.hypot(dx, dy) < DRAG_START_THRESHOLD) return
      isDragging = true
      pendingDrag = false
      if (dragCaptureTarget) dragCaptureTarget.setPointerCapture(event.pointerId)
    }

    panOffset = {
      x: startPanX + event.clientX - dragStartX,
      y: startPanY + event.clientY - dragStartY,
    }
    applyTransform()
  }

  function onPointerUp(event) {
    if (activePointerId !== null && event.pointerId !== activePointerId) return
    pendingDrag = false
    isDragging = false
    releasePointerCapture()
    applyTransform()
  }

  function onWheel(event) {
    event.preventDefault()
    isWheelZooming = true
    clearTimeout(wheelZoomEndTimer)
    wheelZoomEndTimer = setTimeout(() => {
      isWheelZooming = false
      applyTransform()
    }, 120)

    const newScale = Math.min(
      MAX_SCALE,
      Math.max(MIN_SCALE, currentScale * Math.exp(-event.deltaY * WHEEL_ZOOM_INTENSITY)),
    )

    if (newScale <= MIN_SCALE + 0.02) {
      resetZoomState()
      return
    }

    setScaleAt(event.clientX, event.clientY, newScale)
  }

  function onTouchStart(event) {
    markTouchGesture()
    if (event.touches.length !== 2) return

    isPinching = true
    isDragging = false
    pendingDrag = false
    releasePointerCapture()

    pinchStartDistance = getTouchDistance(event.touches)
    pinchStartScale = currentScale
    pinchStartPan = { ...panOffset }
    pinchStartFocal = getTouchMidpoint(event.touches)

    const center = getLayoutCenter()
    if (center) pinchLayoutCenter = { ...center }

    event.preventDefault()
  }

  function onTouchMove(event) {
    if (event.touches.length === 2 && isPinching) {
      const distance = getTouchDistance(event.touches)
      if (pinchStartDistance <= 0) return

      const focal = getTouchMidpoint(event.touches)
      const newScale = Math.min(
        MAX_SCALE,
        Math.max(MIN_SCALE, pinchStartScale * (distance / pinchStartDistance)),
      )

      const ratio = newScale / pinchStartScale
      panOffset = {
        x: focal.x - pinchLayoutCenter.x
          - (pinchStartFocal.x - pinchLayoutCenter.x - pinchStartPan.x) * ratio
          + (focal.x - pinchStartFocal.x),
        y: focal.y - pinchLayoutCenter.y
          - (pinchStartFocal.y - pinchLayoutCenter.y - pinchStartPan.y) * ratio
          + (focal.y - pinchStartFocal.y),
      }

      currentScale = newScale
      applyTransform()
      event.preventDefault()
    }
  }

  function onTouchEnd(event) {
    markTouchGesture()
    if (!isPinching || event.touches.length >= 2) return
    isPinching = false
    pinchStartDistance = 0
    if (currentScale <= MIN_SCALE + 0.02) resetZoomState()
    else applyTransform()
  }

  function onTouchCancel() {
    markTouchGesture()
    if (!isPinching) return
    isPinching = false
    pinchStartDistance = 0
    if (currentScale <= MIN_SCALE + 0.02) resetZoomState()
    else applyTransform()
  }

  on(imageEl, 'dblclick', onImageDblClick)
  on(viewportEl, 'pointerdown', onPointerDown)
  on(viewportEl, 'pointermove', onPointerMove)
  on(viewportEl, 'pointerup', onPointerUp)
  on(viewportEl, 'pointercancel', onPointerUp)
  on(viewportEl, 'wheel', onWheel, { passive: false })
  on(viewportEl, 'touchstart', onTouchStart, { passive: false })
  on(viewportEl, 'touchmove', onTouchMove, { passive: false })
  on(viewportEl, 'touchend', onTouchEnd)
  on(viewportEl, 'touchcancel', onTouchCancel)

  return {
    isZoomed,
    resetZoomState,
    destroy() {
      clearTimeout(wheelZoomEndTimer)
      listeners.forEach(([target, type, handler, options]) => {
        target.removeEventListener(type, handler, options)
      })
      resetZoomState()
    },
  }
}
