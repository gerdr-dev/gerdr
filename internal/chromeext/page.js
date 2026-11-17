// Runs inside the page (Runtime.evaluate), installed once per document and kept on a non-enumerable window property:
// `(<this file>).snapshot({...})`. Refs (e1, e2, …) map to elements through WeakRefs, so a ref stays valid while
// its element is in the page and survives later snapshots; a navigation starts over.
(() => {
  const KEY = '__gerdr_page_v2'
  if (window[KEY]) return window[KEY]

  const refs = new Map()
  const ids = new WeakMap()
  let n = 0
  const ref = (el) => {
    let id = ids.get(el)
    if (!id) {
      id = 'e' + ++n
      ids.set(el, id)
      refs.set(id, new WeakRef(el))
    }
    return id
  }
  const get = (id) => {
    const el = refs.get(id)?.deref()
    if (!el || !el.isConnected) throw new Error(`ref ${id} is gone (the page changed): take a new snapshot`)
    return el
  }

  const clean = (s, max = 80) => {
    s = (s || '').replace(/\s+/g, ' ').trim()
    return s.length > max ? s.slice(0, max) + '…' : s
  }

  const INTERACTIVE = new Set(['button', 'link', 'textbox', 'searchbox', 'checkbox', 'radio', 'combobox', 'listbox', 'option', 'menuitem',
    'menuitemcheckbox', 'menuitemradio', 'tab', 'switch', 'slider', 'spinbutton', 'treeitem', 'file'])
  const STRUCTURE = new Set(['heading', 'navigation', 'main', 'banner', 'contentinfo', 'complementary', 'form', 'dialog', 'alertdialog',
    'alert', 'iframe', 'img', 'table', 'search', 'menu', 'menubar', 'tablist', 'tabpanel', 'list'])
  const LANDMARK = { NAV: 'navigation', MAIN: 'main', HEADER: 'banner', FOOTER: 'contentinfo', ASIDE: 'complementary', FORM: 'form', DIALOG: 'dialog' }
  const INPUT_ROLE = { checkbox: 'checkbox', radio: 'radio', button: 'button', submit: 'button', reset: 'button', image: 'button',
    range: 'slider', number: 'spinbutton', search: 'searchbox', file: 'file', hidden: '' }
  const SKIP = new Set(['SCRIPT', 'STYLE', 'NOSCRIPT', 'TEMPLATE', 'HEAD', 'META', 'LINK', 'SVG', 'svg', 'PATH', 'path'])

  const editableRoot = (el) => el.isContentEditable && !(el.parentElement && el.parentElement.isContentEditable)

  function role(el) {
    const r = el.getAttribute('role')
    if (r) return r.split(/\s+/)[0]
    const t = el.tagName
    switch (t) {
      case 'A': return el.hasAttribute('href') ? 'link' : ''
      case 'BUTTON': case 'SUMMARY': return 'button'
      case 'SELECT': return el.multiple ? 'listbox' : 'combobox'
      case 'TEXTAREA': return 'textbox'
      case 'OPTION': return 'option'
      case 'IMG': return 'img'
      case 'IFRAME': case 'FRAME': return 'iframe'
      case 'TABLE': return 'table'
      case 'INPUT': {
        const ty = (el.type || 'text').toLowerCase()
        return ty in INPUT_ROLE ? INPUT_ROLE[ty] : 'textbox'
      }
    }
    if (/^H[1-6]$/.test(t)) return 'heading'
    if (LANDMARK[t]) return LANDMARK[t]
    if (editableRoot(el)) return 'textbox'
    return ''
  }

  function interactive(el, r) {
    if (INTERACTIVE.has(r) || editableRoot(el)) return true
    if (el.hasAttribute('onclick') || (el.hasAttribute('tabindex') && el.tabIndex >= 0 && r !== 'dialog')) return true
    // clickable elements built from divs: a pointer cursor where the parent's isn't
    const p = el.parentElement
    return getComputedStyle(el).cursor === 'pointer' && (!p || getComputedStyle(p).cursor !== 'pointer')
  }

  function name(el, r) {
    const a = el.getAttribute('aria-label')
    if (a) return clean(a)
    const lb = el.getAttribute('aria-labelledby')
    if (lb) {
      const t = lb.split(/\s+/).map((id) => el.ownerDocument.getElementById(id)?.innerText || '').join(' ')
      if (t.trim()) return clean(t)
    }
    const t = el.tagName
    if (t === 'IMG') return clean(el.alt || el.title)
    if (t === 'IFRAME' || t === 'FRAME') return clean(el.title || el.src, 120)
    if (t === 'INPUT' || t === 'TEXTAREA' || t === 'SELECT') {
      if (['button', 'submit', 'reset'].includes(el.type)) return clean(el.value || el.title)
      if (el.labels && el.labels[0]) return clean(el.labels[0].innerText)
      return clean(el.placeholder || el.title || el.name)
    }
    if (r === 'textbox') return clean(el.getAttribute('placeholder') || el.getAttribute('data-placeholder') || el.title)
    if (['list', 'table', 'navigation', 'main', 'banner', 'contentinfo', 'complementary', 'form', 'dialog', 'tabpanel', 'menu', 'menubar', 'tablist'].includes(r)) {
      return clean(el.title)
    }
    return clean(el.innerText || el.title || el.getAttribute('alt') || el.querySelector('img[alt]')?.alt || '')
  }

  function state(el, r) {
    const s = []
    const t = el.tagName
    if (t === 'INPUT' || t === 'TEXTAREA') {
      if (el.type === 'checkbox' || el.type === 'radio') s.push(el.checked ? 'checked' : 'unchecked')
      else if (el.type === 'password') { if (el.value) s.push('value=••••') }
      else if (el.type === 'file') { if (el.files && el.files.length) s.push('files=' + [...el.files].map((f) => f.name).join(',')) }
      else if (el.value) s.push('value=' + JSON.stringify(clean(el.value, 120)))
    } else if (t === 'SELECT') {
      s.push('value=' + JSON.stringify(clean([...el.selectedOptions].map((o) => o.text).join(', '))))
    } else if (r === 'textbox' && el.isContentEditable) {
      const v = clean(el.innerText, 120)
      if (v) s.push('value=' + JSON.stringify(v))
    }
    if (el.disabled || el.getAttribute('aria-disabled') === 'true') s.push('disabled')
    const ex = el.getAttribute('aria-expanded')
    if (ex) s.push(ex === 'true' ? 'expanded' : 'collapsed')
    if (el.getAttribute('aria-selected') === 'true') s.push('selected')
    const cur = el.getAttribute('aria-current')
    if (cur && cur !== 'false') s.push('current')
    const ch = el.getAttribute('aria-checked')
    if (ch) s.push(ch === 'true' ? 'checked' : ch === 'mixed' ? 'mixed' : 'unchecked')
    if (r === 'heading') s.push('h' + ((t.match(/^H(\d)$/) || [])[1] || el.getAttribute('aria-level') || ''))
    if (r === 'link') {
      const h = el.getAttribute('href')
      if (h && !h.startsWith('javascript:') && h !== '#') s.push('→ ' + clean(h, 100))
    }
    if (el.ownerDocument.activeElement === el && el !== el.ownerDocument.body) s.push('focused')
    return s.join(' ')
  }

  // the element's rect in the top page's viewport (same-origin frames add their offset)
  function rect(el) {
    let r = [...el.getClientRects()].find((x) => x.width > 0 && x.height > 0) || el.getBoundingClientRect()
    let x = r.left, y = r.top
    let w = el.ownerDocument.defaultView
    while (w && w !== window && w.frameElement) {
      const f = w.frameElement.getBoundingClientRect()
      x += f.left + w.frameElement.clientLeft
      y += f.top + w.frameElement.clientTop
      w = w.parent
    }
    return { x, y, w: r.width, h: r.height }
  }

  const sized = (el) => {
    const r = el.getBoundingClientRect()
    return r.width > 0 && r.height > 0
  }
  const shown = (el) => (el.checkVisibility ? el.checkVisibility({ checkOpacity: true, checkVisibilityCSS: true }) : true) && sized(el)
  const inViewport = (el) => {
    const r = rect(el)
    return r.y + r.h > 0 && r.x + r.w > 0 && r.y < innerHeight && r.x < innerWidth
  }

  function children(node) {
    const out = []
    if (node.shadowRoot) out.push(...node.shadowRoot.children)
    if (node.tagName === 'IFRAME' || node.tagName === 'FRAME') {
      try {
        if (node.contentDocument?.body) out.push(node.contentDocument.body)
      } catch {}
      return out
    }
    out.push(...node.children)
    return out
  }

  function describe(el) {
    const r = role(el) || el.tagName.toLowerCase()
    const nm = name(el, r)
    return nm ? `${r} "${nm}"` : r
  }

  function snapshot(o = {}) {
    const find = (o.find || '').toLowerCase()
    const max = o.max || 400
    const lines = []
    let total = 0
    const emit = (depth, text) => {
      if (find && !text.toLowerCase().includes(find)) return
      total++
      if (lines.length < max) lines.push('  '.repeat(depth) + text)
    }
    function walk(node, depth, inInteractive) {
      for (const el of children(node)) {
        if (SKIP.has(el.tagName)) continue
        const isFile = el.tagName === 'INPUT' && el.type === 'file'
        if (!isFile) {
          if (el.getAttribute('aria-hidden') === 'true') continue
          if (el.checkVisibility && !el.checkVisibility()) continue
        }
        const r = role(el)
        let d = depth
        let inter = inInteractive
        const vis = isFile || shown(el)
        const wanted = vis && (!o.viewport || inViewport(el))
        if (vis && !inInteractive && interactive(el, r)) {
          if (wanted) {
            const st = state(el, r)
            emit(depth, `[${ref(el)}] ${r || el.tagName.toLowerCase()}${name(el, r) ? ` "${name(el, r)}"` : ''}${st ? ' ' + st : ''}`)
            d = depth + 1
          }
          // its name already carries the text inside; nested controls are still listed
          inter = true
        } else if (vis && STRUCTURE.has(r) && !(r === 'img' && !name(el, r)) && !(r === 'list' && !o.all)) {
          if (wanted) {
            const st = state(el, r)
            const nm = name(el, r)
            emit(depth, `[${ref(el)}] ${r}${nm ? ` "${nm}"` : ''}${st ? ' ' + st : ''}`)
            d = depth + 1
          }
        } else if (o.all && vis && !inInteractive && wanted) {
          // text written directly in this element
          let own = ''
          for (const c of el.childNodes) if (c.nodeType === 3) own += c.textContent
          own = clean(own, 200)
          if (own) emit(depth, `text "${own}"`)
        }
        walk(el, d, inter)
      }
    }
    walk(document.documentElement, 0, false)
    return {
      url: location.href,
      title: document.title,
      viewport: { w: innerWidth, h: innerHeight },
      scroll: { x: Math.round(scrollX), y: Math.round(scrollY), w: document.documentElement.scrollWidth, h: document.documentElement.scrollHeight },
      lines,
      total,
    }
  }

  // where to click an element: scrolled into view, the centre of its first box, and what is on top there
  function point(id) {
    const el = get(id)
    if (!inViewport(el) || rect(el).y < 0 || rect(el).y + rect(el).h > innerHeight) el.scrollIntoView({ block: 'center', inline: 'center', behavior: 'instant' })
    const r = rect(el)
    const x = r.x + r.w / 2, y = r.y + r.h / 2
    let covered = ''
    const hit = document.elementFromPoint(x, y)
    if (hit && hit !== el && !el.contains(hit) && !hit.contains(el) && hit.tagName !== 'IFRAME') covered = describe(hit)
    return { x, y, desc: describe(el), covered, w: r.w, h: r.h }
  }

  function box(id) {
    const el = get(id)
    el.scrollIntoView({ block: 'center', inline: 'center', behavior: 'instant' })
    const r = rect(el)
    return { x: r.x + scrollX, y: r.y + scrollY, w: r.w, h: r.h }
  }

  // get a field ready to be typed into: focused, its current content selected
  function prepare(id) {
    const el = get(id)
    el.scrollIntoView({ block: 'center', behavior: 'instant' })
    if (el.tagName === 'SELECT') return { kind: 'select', desc: describe(el) }
    if (el.tagName === 'INPUT' && (el.type === 'checkbox' || el.type === 'radio')) return { kind: 'check', checked: el.checked, desc: describe(el) }
    const ac = el.getAttribute('aria-checked')
    if (ac && ['checkbox', 'switch', 'radio', 'menuitemcheckbox'].includes(role(el))) return { kind: 'check', checked: ac === 'true', desc: describe(el) }
    el.focus()
    if (el.tagName === 'INPUT' || el.tagName === 'TEXTAREA') {
      try { el.select() } catch {}
    } else if (el.isContentEditable) {
      const r = el.ownerDocument.createRange()
      r.selectNodeContents(el)
      const s = el.ownerDocument.defaultView.getSelection()
      s.removeAllRanges()
      s.addRange(r)
    } else {
      throw new Error(`${describe(el)} is not a field that takes text`)
    }
    return { kind: 'text', desc: describe(el) }
  }

  function selectOption(id, values) {
    const el = get(id)
    const opts = [...el.options]
    let hit = 0
    for (const o of opts) {
      const m = values.some((v) => o.value === v || o.text.trim() === v)
      if (el.multiple) o.selected = m
      else if (m && !hit) el.value = o.value
      if (m) hit++
    }
    if (!hit) throw new Error('no option ' + JSON.stringify(values) + '; options: ' + opts.map((o) => o.text.trim()).join(' | '))
    el.dispatchEvent(new Event('input', { bubbles: true }))
    el.dispatchEvent(new Event('change', { bubbles: true }))
    return [...el.selectedOptions].map((o) => o.text.trim()).join(', ')
  }

  function focus(id) {
    const el = get(id)
    el.scrollIntoView({ block: 'center', behavior: 'instant' })
    el.focus()
    return describe(el)
  }

  function scrollIntoView(id) {
    const el = get(id)
    el.scrollIntoView({ block: 'center', inline: 'center', behavior: 'instant' })
    return describe(el)
  }

  // scroll the scrollable box under a point (or the element's own), else the page. Wheel events through DevTools
  // hang in a window that isn't in front, so this scrolls from inside the page; scroll events fire as usual.
  function scrollBy(id, x, y, dx, dy) {
    let el = id ? get(id) : document.elementFromPoint(x, y)
    const can = (e) => {
      if (!(e instanceof Element)) return false
      const cs = getComputedStyle(e)
      const oy = /(auto|scroll|overlay)/.test(cs.overflowY) && e.scrollHeight > e.clientHeight + 1
      const ox = /(auto|scroll|overlay)/.test(cs.overflowX) && e.scrollWidth > e.clientWidth + 1
      return (dy !== 0 && oy) || (dx !== 0 && ox)
    }
    while (el && el !== document.body && el !== document.documentElement && !can(el)) el = el.parentElement || el.getRootNode().host
    const box = el && el !== document.body && el !== document.documentElement ? el : document.scrollingElement || document.documentElement
    box.scrollBy({ left: dx, top: dy, behavior: 'instant' })
    const page = box === document.scrollingElement || box === document.documentElement
    return { what: page ? 'the page' : describe(box), x: Math.round(box.scrollLeft), y: Math.round(box.scrollTop), w: box.scrollWidth, h: box.scrollHeight, vw: box.clientWidth, vh: box.clientHeight }
  }

  function check(c) {
    const body = document.body ? document.body.innerText : ''
    if (c.text != null && !body.includes(c.text)) return false
    if (c.gone != null && body.includes(c.gone)) return false
    if (c.selector) {
      const el = document.querySelector(c.selector)
      if (!el || !shown(el)) return false
    }
    if (c.url && !location.href.includes(c.url)) return false
    if (c.load && document.readyState !== 'complete') return false
    return true
  }

  function text(max) {
    let t = document.body ? document.body.innerText : ''
    t = t.replace(/[ \t]+\n/g, '\n').replace(/\n{3,}/g, '\n\n').trim()
    const total = t.length
    if (max && t.length > max) t = t.slice(0, max)
    return { url: location.href, title: document.title, text: t, total }
  }

  function metrics() {
    return {
      dpr: devicePixelRatio, w: innerWidth, h: innerHeight, x: scrollX, y: scrollY,
      sw: document.documentElement.scrollWidth, sh: document.documentElement.scrollHeight,
      url: location.href, title: document.title, ready: document.readyState,
    }
  }

  const api = { get, snapshot, point, box, prepare, selectOption, focus, scrollIntoView, scrollBy, check, text, metrics, describe }
  Object.defineProperty(window, KEY, { value: api, enumerable: false })
  return api
})()
