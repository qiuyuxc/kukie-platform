/**
 * 手写签名动画 — 可插入任意 HTML
 *
 * 用法：
 *   <link rel="stylesheet" href="signature.css">
 *   <div class="signature-widget" data-signature-src="绘图.svg"></div>
 *   <script src="signature.js"></script>
 *
 * data-speed：全局速度 px/ms（总路径长 / speed = 总时长 ms）
 */
(function () {
  'use strict';

  var NS = 'http://www.w3.org/2000/svg';
  var DEFAULT_DURATION = 1500;
  var DEFAULT_SRC = '绘图.svg';
  var MIN_LEN = 0.01;
  var VIEW_PAD = 2;

  /** 时间权重：越小越快，越大越慢 */
  var WEIGHT = {
    path12: 1.0,
    rightDetail: 1.95,
    rightMid: 1.35,
    x154: 1.75,
    x148: 1.15,
    defaultLeft: 0.95
  };

  var RIGHT_DETAIL = { path15: 1, path16: 1, path17: 1, path18: 1, path19: 1 };

  function isCmd(t) {
    return /^[a-zA-Z]$/.test(t);
  }

  function fmt(n) {
    return String(+n.toFixed(4));
  }

  function withHiddenSvg(fn) {
    var svg = document.createElementNS(NS, 'svg');
    svg.setAttribute('xmlns', NS);
    svg.style.cssText = 'position:absolute;visibility:hidden;pointer-events:none';
    document.body.appendChild(svg);
    try {
      return fn(svg);
    } finally {
      document.body.removeChild(svg);
    }
  }

  function absolutizeSubpaths(d) {
    var tokens = d.match(/[a-zA-Z]|-?\d*\.?\d+(?:e[-+]?\d+)?/g);
    if (!tokens || !tokens.length) return [d];

    var i = 0;
    var cx = 0;
    var cy = 0;
    var sx = 0;
    var sy = 0;
    var subpaths = [];
    var parts = [];

    function num() {
      return parseFloat(tokens[i++]);
    }

    function flush() {
      if (parts.length) subpaths.push(parts.join(' '));
      parts = [];
    }

    while (i < tokens.length) {
      var cmd = tokens[i++];
      var upper = cmd.toUpperCase();
      var rel = cmd !== upper;

      if (upper === 'M') {
        flush();
        var x = num();
        var y = num();
        if (rel) { x += cx; y += cy; }
        cx = sx = x;
        cy = sy = y;
        parts.push('M', fmt(x), fmt(y));
        while (i < tokens.length && !isCmd(tokens[i])) {
          x = num();
          y = num();
          if (rel) { x += cx; y += cy; }
          cx = x;
          cy = y;
          parts.push('L', fmt(x), fmt(y));
        }
      } else if (upper === 'L') {
        do {
          var lx = num();
          var ly = num();
          if (rel) { lx += cx; ly += cy; }
          cx = lx;
          cy = ly;
          parts.push('L', fmt(lx), fmt(ly));
        } while (i < tokens.length && !isCmd(tokens[i]));
      } else if (upper === 'C') {
        do {
          var x1 = num();
          var y1 = num();
          var x2 = num();
          var y2 = num();
          x = num();
          y = num();
          if (rel) {
            x1 += cx; y1 += cy;
            x2 += cx; y2 += cy;
            x += cx; y += cy;
          }
          cx = x;
          cy = y;
          parts.push('C', fmt(x1), fmt(y1), fmt(x2), fmt(y2), fmt(x), fmt(y));
        } while (i < tokens.length && !isCmd(tokens[i]));
      } else if (upper === 'Z') {
        cx = sx;
        cy = sy;
        parts.push('Z');
      }
    }

    flush();
    return subpaths.length ? subpaths : [d];
  }

  function measureStrokes(pathNodes) {
    var strokes = [];
    pathNodes.forEach(function (node) {
      var d = node.getAttribute('d');
      var id = node.getAttribute('id') || '';
      if (!d) return;
      absolutizeSubpaths(d.trim()).forEach(function (sub, idx, subs) {
        strokes.push({ d: sub, id: subs.length > 1 ? id + ':' + idx : id });
      });
    });
    return strokes;
  }

  function measurePath(d, svg) {
    var p = document.createElementNS(NS, 'path');
    p.setAttribute('d', d);
    svg.appendChild(p);
    var box = p.getBBox();
    var info = {
      len: p.getTotalLength(),
      startX: box.x,
      startY: box.y,
      minX: box.x,
      minY: box.y,
      maxX: box.x + box.width,
      maxY: box.y + box.height
    };
    svg.removeChild(p);
    return info;
  }

  function prepareStrokeData(pathNodes, viewPad) {
    var pad = typeof viewPad === 'number' && isFinite(viewPad) ? viewPad : VIEW_PAD;
    var originals = pathNodes.map(function (node) {
      return { d: node.getAttribute('d'), id: node.getAttribute('id') || '' };
    }).filter(function (s) { return s.d; });

    var strokes = measureStrokes(pathNodes);

    return withHiddenSvg(function (svg) {
      var minX = Infinity;
      var minY = Infinity;
      var maxX = -Infinity;
      var maxY = -Infinity;

      originals.forEach(function (stroke) {
        var m = measurePath(stroke.d, svg);
        minX = Math.min(minX, m.minX);
        minY = Math.min(minY, m.minY);
        maxX = Math.max(maxX, m.maxX);
        maxY = Math.max(maxY, m.maxY);
      });

      strokes.forEach(function (stroke) {
        var m = measurePath(stroke.d, svg);
        stroke.len = m.len;
        stroke.startX = m.startX;
        stroke.startY = m.startY;
      });

      strokes = strokes.filter(function (s) { return s.len > MIN_LEN; }).sort(function (a, b) {
        if (a.startX !== b.startX) return a.startX - b.startX;
        return a.startY - b.startY;
      });

      var viewBox = '0 0 100 100';
      if (isFinite(minX)) {
        viewBox = [
          (minX - pad).toFixed(2),
          (minY - pad).toFixed(2),
          (maxX - minX + pad * 2).toFixed(2),
          (maxY - minY + pad * 2).toFixed(2)
        ].join(' ');
      }

      return { strokes: strokes, viewBox: viewBox };
    });
  }

  function strokeTimeWeight(stroke) {
    var baseId = (stroke.id || '').split(':')[0];

    if (baseId === 'path12') return WEIGHT.path12;
    if (RIGHT_DETAIL[baseId]) return WEIGHT.rightDetail;
    if (baseId === 'path21' || baseId === 'path7') return WEIGHT.rightMid;
    if (stroke.startX >= 154) return WEIGHT.x154;
    if (stroke.startX >= 148) return WEIGHT.x148;
    return WEIGHT.defaultLeft;
  }

  function buildTimeline(strokes, pathEls, totalDuration) {
    var meta = pathEls.map(function (p, i) {
      return { len: p._sigLen, weight: strokeTimeWeight(strokes[i]) };
    });

    var weighted = meta.reduce(function (s, m) { return s + m.len * m.weight; }, 0);
    if (!weighted) weighted = 1;

    var t = 0;
    meta.forEach(function (m) {
      m.startTime = t;
      m.duration = totalDuration * (m.len * m.weight / weighted);
      m.endTime = t + m.duration;
      t = m.endTime;
    });

    return meta;
  }

  function trimViewBox(viewBox, crops) {
    var left = 0;
    var top = 0;
    var right = 0;
    var bottom = 0;

    if (typeof crops === 'number') {
      left = crops;
    } else if (crops) {
      left = crops.left || 0;
      top = crops.top || 0;
      right = crops.right || 0;
      bottom = crops.bottom || 0;
    }

    if (!left && !top && !right && !bottom) return viewBox;

    var parts = viewBox.split(/\s+/).map(parseFloat);
    if (parts.length !== 4) return viewBox;

    left = Math.min(Math.max(0, left), parts[2] * 0.45);
    top = Math.min(Math.max(0, top), parts[3] * 0.35);
    right = Math.min(Math.max(0, right), parts[2] * 0.2);
    bottom = Math.min(Math.max(0, bottom), parts[3] * 0.35);

    parts[0] += left;
    parts[1] += top;
    parts[2] -= left + right;
    parts[3] -= top + bottom;

    if (parts[2] <= 0 || parts[3] <= 0) return viewBox;

    return parts.map(function (n) { return n.toFixed(2); }).join(' ');
  }

  function buildSvg(viewBox, strokes) {
    var svg = document.createElementNS(NS, 'svg');
    svg.setAttribute('class', 'signature-svg');
    svg.setAttribute('viewBox', viewBox);
    svg.setAttribute('xmlns', NS);
    svg.setAttribute('aria-hidden', 'true');

    var g = document.createElementNS(NS, 'g');
    g.setAttribute('class', 'signature-ink');

    strokes.forEach(function (stroke, i) {
      var path = document.createElementNS(NS, 'path');
      path.setAttribute('class', 'signature-stroke');
      path.setAttribute('d', stroke.d);
      path.dataset.order = String(i + 1);
      if (stroke.id) path.dataset.id = stroke.id;
      g.appendChild(path);
    });

    svg.appendChild(g);
    return svg;
  }

  function resolveStrokeColor(widget) {
    var color = getComputedStyle(widget).getPropertyValue('--signature-color').trim();
    if (!color || color === 'inherit') color = '#222222';
    return color;
  }

  function applyStrokeColor(el, color) {
    el.style.stroke = color;
  }

  function preparePath(el, color) {
    var len = el.getTotalLength();
    el._sigLen = len;
    el._sigDone = false;
    el._sigDrawing = false;
    el.classList.remove('is-complete', 'is-drawing');
    el.style.opacity = '0';
    el.style.strokeLinecap = 'butt';
    el.style.strokeDasharray = String(len);
    el.style.strokeDashoffset = String(len);
    applyStrokeColor(el, color);
  }

  function setStrokeHidden(el, color) {
    el._sigDone = false;
    el._sigDrawing = false;
    el.classList.remove('is-complete', 'is-drawing');
    el.style.opacity = '0';
    el.style.strokeLinecap = 'butt';
    el.style.strokeDasharray = String(el._sigLen);
    el.style.strokeDashoffset = String(el._sigLen);
    applyStrokeColor(el, color);
  }

  function setStrokeDrawing(el, progress, color) {
    var len = el._sigLen;
    var p = Math.min(1, Math.max(0, progress));

    if (!el._sigDrawing) {
      el._sigDrawing = true;
      el.classList.add('is-drawing');
      el.classList.remove('is-complete');
      el.style.opacity = '1';
      el.style.strokeLinecap = 'round';
      el.style.strokeDasharray = String(len);
      applyStrokeColor(el, color);
    }

    el.style.strokeDashoffset = String(len * (1 - p));
  }

  function setStrokeComplete(el, color) {
    el._sigDone = true;
    el._sigDrawing = false;
    el.classList.remove('is-drawing');
    el.classList.add('is-complete');
    el.style.opacity = '1';
    el.style.strokeLinecap = 'round';
    el.style.strokeDasharray = 'none';
    el.style.strokeDashoffset = '0';
    applyStrokeColor(el, color);
  }

  function loadSvg(src) {
    return fetch(src).then(function (res) {
      if (!res.ok) throw new Error('无法加载 SVG: ' + src);
      return res.text();
    }).then(function (text) {
      var doc = new DOMParser().parseFromString(text, 'image/svg+xml');
      var svg = doc.querySelector('svg');
      if (!svg) throw new Error('SVG 无效: ' + src);
      return svg;
    });
  }

  function initWidget(widget) {
    if (widget.dataset.signatureReady === '1' || widget.dataset.signatureLoading === '1') return;
    widget.dataset.signatureLoading = '1';

    var src = widget.dataset.signatureSrc || DEFAULT_SRC;
    var autoplay = widget.dataset.autoplay !== 'false';
    var customSpeed = widget.dataset.speed ? parseFloat(widget.dataset.speed) : null;
    var viewPad = widget.dataset.viewPad !== undefined ? parseFloat(widget.dataset.viewPad) : VIEW_PAD;
    var cropLeft = widget.dataset.cropLeft ? parseFloat(widget.dataset.cropLeft) : 0;
    var cropTop = widget.dataset.cropTop ? parseFloat(widget.dataset.cropTop) : 0;
    var cropBottom = widget.dataset.cropBottom ? parseFloat(widget.dataset.cropBottom) : 0;
    var inlineSvg = widget.querySelector('svg');

    function finishLoading() {
      widget.removeAttribute('data-signature-loading');
    }

    function showFallback() {
      var label = widget.getAttribute('aria-label');
      if (label) {
        widget.textContent = label;
        widget.style.fontSize = 'inherit';
        widget.style.lineHeight = 'inherit';
        widget.style.color = 'inherit';
      }
      widget.dataset.signatureReady = '1';
    }

    function setup(svgSource) {
      try {
      var pathNodes = Array.from(svgSource.querySelectorAll('path'));
      if (!pathNodes.length) {
        showFallback();
        return;
      }

      var data = prepareStrokeData(pathNodes, viewPad);
      var strokes = data.strokes;
      if (!strokes.length) {
        showFallback();
        return;
      }

      widget.querySelectorAll('svg').forEach(function (el) { el.remove(); });

      var svg = buildSvg(trimViewBox(data.viewBox, {
        left: cropLeft,
        top: cropTop,
        bottom: cropBottom
      }), strokes);
      widget.appendChild(svg);

      var strokeColor = resolveStrokeColor(widget);
      var paths = Array.from(svg.querySelectorAll('.signature-stroke'));
      paths.forEach(function (p) { preparePath(p, strokeColor); });

      var totalLen = paths.reduce(function (s, p) { return s + p._sigLen; }, 0);
      var totalDuration = customSpeed ? totalLen / customSpeed : DEFAULT_DURATION;
      var meta = buildTimeline(strokes, paths, totalDuration);

      var played = false;
      var runId = 0;
      var observer = null;

      function reset() {
        widget.removeAttribute('data-signature-playing');
        paths.forEach(function (p) {
          p._sigProgress = -1;
          setStrokeHidden(p, strokeColor);
        });
      }

      function showComplete() {
        widget.removeAttribute('data-signature-playing');
        paths.forEach(function (p) {
          p._sigProgress = 1;
          setStrokeComplete(p, strokeColor);
        });
      }

      function applyProgress(elapsed) {
        for (var i = 0; i < paths.length; i++) {
          var p = paths[i];
          var m = meta[i];

          if (p._sigDone) continue;

          if (elapsed <= m.startTime) {
            if (p._sigProgress !== 0) {
              setStrokeHidden(p, strokeColor);
              p._sigProgress = 0;
            }
          } else if (elapsed >= m.endTime) {
            if (p._sigProgress !== 1) {
              setStrokeComplete(p, strokeColor);
              p._sigProgress = 1;
            }
          } else {
            var progress = (elapsed - m.startTime) / m.duration;
            if (!p._sigDrawing) {
              setStrokeDrawing(p, progress, strokeColor);
            } else {
              p.style.strokeDashoffset = String(p._sigLen * (1 - progress));
            }
            p._sigProgress = progress;
          }
        }
      }

      function play() {
        var token = ++runId;
        reset();

        return new Promise(function (resolve) {
          var t0 = 0;

          function frame(now) {
            if (token !== runId) { resolve(); return; }
            if (!t0) {
              t0 = now;
              widget.setAttribute('data-signature-playing', '1');
            }

            var elapsed = Math.min(totalDuration, now - t0);
            applyProgress(elapsed);

            if (elapsed < totalDuration) {
              requestAnimationFrame(frame);
            } else {
              paths.forEach(function (p) {
                if (!p._sigDone) setStrokeComplete(p, strokeColor);
              });
              resolve();
            }
          }

          requestAnimationFrame(function () {
            requestAnimationFrame(frame);
          });
        });
      }

      function replay() {
        played = true;
        play();
      }

      if (autoplay) {
        reset();
      } else {
        showComplete();
      }
      widget.dataset.signatureReady = '1';

      if (autoplay) {
        observer = new IntersectionObserver(function (entries) {
          if (entries[0].isIntersecting && !played) {
            played = true;
            if (observer) {
              observer.disconnect();
              observer = null;
            }
            play();
          }
        }, { threshold: 0.35 });
        observer.observe(widget);
      }

      widget.addEventListener('click', replay);
      widget.addEventListener('keydown', function (e) {
        if (e.key === 'Enter' || e.key === ' ') {
          e.preventDefault();
          replay();
        }
      });
      } catch (err) {
        console.error('[signature]', err);
        showFallback();
      }
    }

    if (inlineSvg && !widget.dataset.signatureSrc) {
      try {
        setup(inlineSvg);
      } finally {
        finishLoading();
      }
      return Promise.resolve();
    }

    return loadSvg(src).then(setup).catch(function (err) {
      console.error('[signature]', err.message);
      if (inlineSvg) {
        setup(inlineSvg);
        return;
      }
      showFallback();
    }).finally(finishLoading);
  }

  function boot() {
    document.querySelectorAll('.signature-widget').forEach(initWidget);
  }

  if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', boot);
  } else {
    boot();
  }

  window.SignatureWidget = { init: initWidget, boot: boot };
})();
