const EFFECT_TYPES = ['sakura', 'rain', 'snow', 'firefly', 'butterfly']
const DRIFT_TYPES = new Set(['sakura', 'snow'])
const SPREAD_TYPES = new Set(['butterfly'])
const DEFAULT_SCRIPT = '/vendor/yzhanweather/yzhanweather.min.js'
const DEFAULT_RANDOM_TYPES = [...EFFECT_TYPES]
const FIREFLY_DURATION = [1.6, 2.8]
const RAIN_DURATION = [0.38, 0.65]
const DRIFT_DURATION = [0.55, 1.2]
const SPREAD_DURATION = [0.75, 1.25]
const DRIFT_ANIMATIONS = ['sakura-fx-drift-a', 'sakura-fx-drift-b', 'sakura-fx-drift-c']
const CONFIG_ID = 'sakura-site-effects-config'
const REDUCED_MOTION = '(prefers-reduced-motion: reduce)'
const MOBILE_WIDTH = '(max-width: 768px)'
const MOBILE_TOUCH = '(hover: none) and (pointer: coarse)'

let initPromise = null
let effectInstance = null
let cachedConfig = null
let visibilityListenerAdded = false

function readJsonScript(id, fallback) {
  const el = document.getElementById(id)
  if (!el?.textContent?.trim()) return fallback
  try {
    let parsed = JSON.parse(el.textContent)
    if (typeof parsed === 'string') parsed = JSON.parse(parsed)
    return parsed
  } catch {
    return fallback
  }
}

function isMobileView() {
  return window.matchMedia(MOBILE_TOUCH).matches
    || window.matchMedia(MOBILE_WIDTH).matches
}

function waitForYZhanWeather(timeoutMs = 8000) {
  if (resolveYZhanWeatherClass()) return Promise.resolve()

  return new Promise((resolve, reject) => {
    const started = performance.now()
    const tick = () => {
      if (resolveYZhanWeatherClass()) {
        resolve()
        return
      }
      if (performance.now() - started >= timeoutMs) {
        reject(new Error('YZhanWeather is unavailable'))
        return
      }
      window.setTimeout(tick, 32)
    }
    tick()
  })
}

function loadExternalScript(url) {
  const src = String(url || DEFAULT_SCRIPT).trim() || DEFAULT_SCRIPT
  return new Promise((resolve, reject) => {
    const existing = document.querySelector(`script[data-site-effects-src="${src}"]`)
    if (existing) {
      waitForYZhanWeather().then(() => resolve(src)).catch(reject)
      return
    }

    const tag = document.createElement('script')
    tag.src = src
    tag.defer = true
    tag.dataset.siteEffectsSrc = src
    tag.onload = () => {
      waitForYZhanWeather().then(() => resolve(src)).catch(reject)
    }
    tag.onerror = () => reject(new Error(`failed to load: ${src}`))
    document.head.appendChild(tag)
  })
}

function normalizeRandomTypes(types) {
  if (!Array.isArray(types)) return [...DEFAULT_RANDOM_TYPES]
  const filtered = types
    .map((type) => String(type).trim().toLowerCase())
    .filter((type) => EFFECT_TYPES.includes(type))
  return filtered.length ? filtered : [...DEFAULT_RANDOM_TYPES]
}

function pickEffectType(config) {
  const type = String(config.type || 'sakura').trim().toLowerCase()
  if (type !== 'random') {
    return EFFECT_TYPES.includes(type) ? type : 'sakura'
  }
  const pool = normalizeRandomTypes(config.randomTypes)
  return pool[Math.floor(Math.random() * pool.length)]
}

function readConfig() {
  if (cachedConfig) return cachedConfig

  const raw = readJsonScript(CONFIG_ID, null)
  if (!raw) return null

  const maxDuration = Number.parseFloat(raw.maxDuration ?? 10)
  const zIndex = Number.parseInt(raw.zIndex ?? 5, 10)

  cachedConfig = {
    type: String(raw.type || 'sakura').trim().toLowerCase(),
    maxDuration: Number.isFinite(maxDuration) && maxDuration > 0 ? maxDuration : 10,
    zIndex: Number.isFinite(zIndex) ? zIndex : 5,
    script: String(raw.script || DEFAULT_SCRIPT).trim() || DEFAULT_SCRIPT,
    randomTypes: normalizeRandomTypes(raw.randomTypes),
    randomize: raw.randomize !== false,
    mobile: raw.mobile === true,
  }
  return cachedConfig
}

function resolveYZhanWeatherClass() {
  const exported = window.YZhanWeather
  if (typeof exported === 'function') return exported
  if (typeof exported?.default === 'function') return exported.default
  return null
}

export function shouldUseSiteEffects() {
  if (window.matchMedia(REDUCED_MOTION).matches) return false
  const config = readConfig()
  if (!config) return false
  if (!config.mobile && isMobileView()) return false
  return true
}

function randomBetween(min, max) {
  return min + Math.random() * (max - min)
}

function applyTimedAnimation(el, duration, easing = 'linear') {
  el.style.animationDuration = `${duration}s`
  el.style.animationDelay = `${-randomBetween(0, duration * 0.98)}s`
  el.style.animationTimingFunction = easing
  el.style.animationIterationCount = 'infinite'
}

function setupFallContainer(container) {
  container.style.display = 'block'
  container.style.position = 'absolute'
  container.style.inset = '0'
  container.style.width = '100%'
  container.style.height = '100%'
  container.style.overflow = 'hidden'
  container.style.pointerEvents = 'none'
}

function applyRainParticleStyle(el, base) {
  const duration = base * randomBetween(RAIN_DURATION[0], RAIN_DURATION[1])
  el.style.cssText = [
    'position:absolute',
    'top:0',
    'margin:0',
    `left:${randomBetween(-5, 105)}vw`,
  ].join(';')
  applyTimedAnimation(el, duration, 'linear')
  el.style.setProperty('--sakura-fx-rain-opacity', randomBetween(0.5, 0.8).toFixed(3))
}

function randomizeRainParticles(container, base) {
  const particles = container.querySelectorAll(':scope > div')
  particles.forEach((el) => applyRainParticleStyle(el, base))

  const boost = Math.floor(particles.length * 0.25)
  if (!boost) return

  const fragment = document.createDocumentFragment()
  for (let i = 0; i < boost; i += 1) {
    const clone = particles[i % particles.length].cloneNode(false)
    applyRainParticleStyle(clone, base)
    fragment.appendChild(clone)
  }
  container.appendChild(fragment)
}

function pickDriftAnimation() {
  return DRIFT_ANIMATIONS[Math.floor(Math.random() * DRIFT_ANIMATIONS.length)]
}

function randomizeFallParticles(container, base) {
  const particles = container.querySelectorAll(':scope > div')

  particles.forEach((el) => {
    const duration = base * randomBetween(DRIFT_DURATION[0], DRIFT_DURATION[1])

    el.style.cssText = [
      'position:absolute',
      'top:0',
      'margin:0',
      `left:${randomBetween(0, 100)}vw`,
      `animation-name:${pickDriftAnimation()}`,
    ].join(';')
    applyTimedAnimation(el, duration, 'linear')
    el.style.setProperty('--sakura-fx-scale', randomBetween(0.55, 1.25).toFixed(3))
    el.style.setProperty('--sakura-fx-opacity', randomBetween(0.35, 0.95).toFixed(3))
    el.style.setProperty('--sakura-fx-shift', `${randomBetween(-18, 18)}vw`)
    el.style.setProperty('--sakura-fx-rotate', `${randomBetween(-45, 45)}deg`)
  })
}

function randomizeFireflyParticles(container, base) {
  const particles = container.querySelectorAll(':scope > div')

  particles.forEach((el) => {
    const duration = base * randomBetween(FIREFLY_DURATION[0], FIREFLY_DURATION[1])

    el.style.cssText = [
      'position:absolute',
      'top:0',
      'margin:0',
      `left:${randomBetween(0, 100)}vw`,
    ].join(';')
    applyTimedAnimation(el, duration, 'ease-in-out')
    el.style.setProperty('--sakura-fx-firefly-opacity', randomBetween(0.35, 0.9).toFixed(3))
    el.style.setProperty('--sakura-fx-firefly-shift', `${randomBetween(-6, 6)}vw`)
    el.style.setProperty('--sakura-fx-firefly-drift', `${randomBetween(-10, 10)}vw`)
  })
}

function randomizeSpreadParticles(container, base) {
  const particles = container.querySelectorAll(':scope > div')

  particles.forEach((el) => {
    const duration = base * randomBetween(SPREAD_DURATION[0], SPREAD_DURATION[1])
    el.style.marginLeft = ''
    el.style.marginTop = ''
    applyTimedAnimation(el, duration, 'ease-in-out')
  })
}

function applyFireflyLayout(container, base) {
  container.classList.add('sakura-fx-randomized', 'sakura-fx-firefly')
  setupFallContainer(container)
  randomizeFireflyParticles(container, base)
}

function randomizeEffectLayout(instance, config, effectType) {
  const container = instance?.container
  if (!container) return

  const base = config.maxDuration

  // 萤火虫始终使用自定义向上动画，不受 randomize 影响
  if (effectType === 'firefly') {
    applyFireflyLayout(container, base)
    return
  }

  if (!config.randomize) return

  container.classList.add('sakura-fx-randomized')

  if (effectType === 'rain') {
    container.classList.add('sakura-fx-rain')
    setupFallContainer(container)
    randomizeRainParticles(container, base)
    return
  }

  if (DRIFT_TYPES.has(effectType)) {
    container.classList.add('sakura-fx-fall')
    setupFallContainer(container)
    randomizeFallParticles(container, base)
    return
  }

  if (SPREAD_TYPES.has(effectType)) {
    container.classList.add('sakura-fx-spread')
    setupFallContainer(container)
    container.style.display = 'flex'
    container.style.justifyContent = 'space-around'
    randomizeSpreadParticles(container, base)
  }
}

function bindVisibilityPause() {
  if (visibilityListenerAdded) return
  visibilityListenerAdded = true

  document.addEventListener('visibilitychange', () => {
    const paused = document.hidden
    document.querySelectorAll('.sakura-fx-layer').forEach((layer) => {
      layer.classList.toggle('sakura-fx-paused', paused)
    })
  }, { passive: true })
}

function destroyEffectInstance() {
  if (effectInstance?.destory) {
    effectInstance.destory()
  } else if (effectInstance?.destroy) {
    effectInstance.destroy()
  }
  effectInstance = null
}

function runEffect(instance, effectType, runDuration) {
  try {
    instance.run(effectType, { maxDuration: runDuration })
    return effectType
  } catch (err) {
    console.warn('[site-effects] fallback to sakura:', effectType, err)
    try {
      instance.run('sakura', { maxDuration: runDuration })
      return 'sakura'
    } catch (fallbackErr) {
      console.warn('[site-effects] sakura fallback failed:', fallbackErr)
      return null
    }
  }
}

async function initSiteEffects() {
  if (!shouldUseSiteEffects()) return

  const config = readConfig()
  if (!config) return

  await loadExternalScript(config.script)

  const YZhanWeather = resolveYZhanWeatherClass()
  if (!YZhanWeather) throw new Error('YZhanWeather is unavailable')

  destroyEffectInstance()

  const effectType = pickEffectType(config)
  const runDuration = config.maxDuration * randomBetween(0.85, 1.15)

  effectInstance = new YZhanWeather()
  const wrapper = effectInstance.wrapper
  if (wrapper) {
    wrapper.classList.add('sakura-fx-layer')
    wrapper.style.zIndex = String(config.zIndex)
    bindVisibilityPause()
  }

  const activeType = runEffect(effectInstance, effectType, runDuration)
  if (!activeType) return

  randomizeEffectLayout(effectInstance, { ...config, maxDuration: runDuration }, activeType)
}

export function scheduleSiteEffectsInit() {
  if (!shouldUseSiteEffects()) return
  if (initPromise) return

  const run = () => {
    initPromise = initSiteEffects().catch((err) => {
      initPromise = null
      console.warn('[site-effects]', err)
    })
  }

  if ('requestIdleCallback' in window) {
    requestIdleCallback(run, { timeout: 4000 })
  } else {
    window.setTimeout(run, 800)
  }
}
