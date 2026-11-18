// What the user sees on a page Gerdr is driving: a glowing frame round the viewport (this window is the agent's)
// and a cursor that glides to each point the agent clicks, with a ripple on the press. Like Claude in Chrome's.
//
// overlay() is not called here: background.js sends its source into the page with Runtime.evaluate (now) and
// Page.addScriptToEvaluateOnNewDocument (every later page), so it must be self-contained. Written to stay out of
// the agent's way and the page's:
//   - pointer-events: none everywhere, so clicks and elementFromPoint (page.js's "covered by" check) pass through;
//   - a closed shadow root, so page.js's snapshot (which walks open roots) and page CSS never see inside;
//   - appended to <html>, not <body>, so body.innerText (the text action) leaves it out;
//   - no innerHTML and no <style>: pages with Trusted Types or a strict style-src CSP would refuse those,
//     while the CSSOM (el.style) and Web Animations are always allowed;
//   - hidden by background.js while a screenshot is taken.
export function overlay(color) {
  if (window !== window.top || window.__gerdrOverlay) return
  const css = (el, s) => (Object.assign(el.style, s), el)
  const SVG = 'http://www.w3.org/2000/svg'
  let host, root, glow, cursor, arrow, x = null, y = null, moving = null

  function build() {
    host = css(document.createElement('gerdr-overlay'), {
      position: 'fixed', inset: '0', zIndex: '2147483647', pointerEvents: 'none', display: 'block', margin: '0', padding: '0',
      border: 'none', background: 'none', transform: 'none', filter: 'none', opacity: '1', contain: 'strict',
    })
    root = host.attachShadow({ mode: 'closed' })
    glow = css(document.createElement('div'), {
      position: 'absolute', inset: '0', pointerEvents: 'none', borderRadius: '2px',
      boxShadow: `inset 0 0 0 2px ${color}, inset 0 0 30px 6px ${color}99`,
    })
    glow.animate([{ opacity: 0.55 }, { opacity: 1 }], { duration: 1600, iterations: Infinity, direction: 'alternate', easing: 'ease-in-out' })
    cursor = css(document.createElement('div'), {
      position: 'absolute', left: '0', top: '0', width: '24px', height: '24px', pointerEvents: 'none', display: 'none',
      filter: 'drop-shadow(0 1px 2px rgba(0,0,0,.35))',
    })
    const svg = (arrow = document.createElementNS(SVG, 'svg'))
    css(svg, { display: 'block', transformOrigin: '3px 2px' }) // the tip, so a press keeps it on the point
    svg.setAttribute('width', '24')
    svg.setAttribute('height', '24')
    svg.setAttribute('viewBox', '0 0 24 24')
    const path = document.createElementNS(SVG, 'path')
    // an arrow pointer whose tip is at (3, 2)
    path.setAttribute('d', 'M3 2 L3 19.5 L7.6 15.2 L10.6 21.6 L13.6 20.2 L10.7 13.9 L17 13.9 Z')
    path.setAttribute('fill', color)
    path.setAttribute('stroke', '#fff')
    path.setAttribute('stroke-width', '1.6')
    path.setAttribute('stroke-linejoin', 'round')
    svg.append(path)
    cursor.append(svg)
    root.append(glow, cursor)
  }

  // Put the overlay back if it isn't in the page: not yet (this runs before <html> exists on a new page) or no
  // longer (a framework rendering the whole document dropped it).
  function ensure() {
    const html = document.documentElement
    if (!html) return false
    if (!host) build()
    if (host.parentNode !== html) html.append(host)
    return true
  }

  const at = (px, py) => `translate(${px - 3}px, ${py - 2}px)`

  function move(nx, ny) {
    if (!ensure()) return
    if (cursor.style.display === 'none') {
      // first appearance: come in from the middle of the window so the user's eye can follow it
      x = innerWidth / 2
      y = innerHeight / 2
      cursor.style.display = 'block'
    }
    if (moving) {
      // a new point before the last glide ended: start from where it is now
      const m = new DOMMatrix(getComputedStyle(cursor).transform)
      moving.cancel()
      x = m.m41 + 3
      y = m.m42 + 2
    }
    const dist = Math.hypot(nx - x, ny - y)
    const from = at(x, y)
    x = nx
    y = ny
    cursor.style.transform = at(nx, ny)
    if (dist < 1) return
    const anim = (moving = cursor.animate([{ transform: from }, { transform: at(nx, ny) }], {
      duration: Math.min(550, 160 + dist * 0.45),
      easing: 'cubic-bezier(.3,.7,.2,1)',
    }))
    return anim.finished.then(
      () => moving === anim && (moving = null),
      () => {},
    )
  }

  function press() {
    if (!ensure() || x === null) return
    const ring = css(document.createElement('div'), {
      position: 'absolute', left: `${x - 14}px`, top: `${y - 14}px`, width: '28px', height: '28px', borderRadius: '50%',
      border: `2px solid ${color}`, background: `${color}33`, pointerEvents: 'none', boxSizing: 'border-box',
    })
    root.append(ring)
    ring.animate([{ transform: 'scale(.3)', opacity: 0.9 }, { transform: 'scale(1.5)', opacity: 0 }], { duration: 450, easing: 'ease-out' })
      .finished.then(() => ring.remove(), () => ring.remove())
    // squeeze the arrow, not `cursor`: the `scale` property applies outside `transform`, so on `cursor` it scaled the
    // translate too and the arrow jumped toward the window's top-left on every click (seen in a screen recording)
    arrow.animate([{ transform: 'scale(1)' }, { transform: 'scale(.82)' }, { transform: 'scale(1)' }], { duration: 220, easing: 'ease-out' })
  }

  function hide(on) {
    if (host) host.style.visibility = on ? 'hidden' : 'visible'
  }

  function remove() {
    observer.disconnect()
    host?.remove()
    delete window.__gerdrOverlay
  }

  // watch <html> appearing (or being replaced) and its children, to put the overlay back
  const observer = new MutationObserver(() => {
    const html = document.documentElement
    if (!html) return
    observer.observe(html, { childList: true })
    if (host?.parentNode !== html) ensure()
  })
  observer.observe(document, { childList: true })
  if (document.documentElement) observer.observe(document.documentElement, { childList: true })
  ensure()

  Object.defineProperty(window, '__gerdrOverlay', { value: { move, press, hide, remove }, configurable: true, enumerable: false })
}
