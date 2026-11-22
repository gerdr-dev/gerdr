/**
 * ```mermaid blocks drawn as diagrams (components/chat/MermaidBlock.tsx): agents draw flowcharts and sequences in
 * their replies, and the App and the phone showed the source (user report).
 *
 * Why not Streamdown's own Mermaid (@streamdown/mermaid): Streamdown draws it in a chunk it imports lazily, and
 * gerdr's vendor bundle leaves a package's lazy chunks out (internal/uisite/vendor.go), so that chunk came with a
 * second copy of Streamdown whose context had no plugin in it ("Mermaid plugin not available"). Drawing the block
 * ourselves through ChatHost.codeBlocks needs none of that.
 *
 * Mermaid is megabytes: imported the first time a diagram is drawn, never with the page.
 */

type Mermaid = (typeof import("mermaid"))["default"];
let mod: Promise<Mermaid> | null = null;
const load = () =>
  (mod ??= import("mermaid").then(
    (m) => m.default,
    (e) => {
      mod = null; // offline / the CDN failing: the next diagram tries again
      throw e;
    },
  ));

// Mermaid is one global and initialize() sets the theme for the next render: one render at a time
let queue: Promise<unknown> = Promise.resolve();
let seq = 0;

/** Drawn SVGs by theme + source: a row the virtual list unmounted shows its diagram at once when it comes back. */
const done = new Map<string, string>();
const pending = new Map<string, Promise<string>>();
const keyOf = (code: string, dark: boolean) => `${dark ? "d" : "l"}\n${code}`;

/** A diagram already drawn, or undefined. */
export const drawnMermaid = (code: string, dark: boolean) => done.get(keyOf(code, dark));

/** The diagram as an SVG string; rejects with Mermaid's parse error for a source it can't read. */
export function renderMermaid(code: string, dark: boolean): Promise<string> {
  const key = keyOf(code, dark);
  const had = done.get(key);
  if (had !== undefined) return Promise.resolve(had);
  let p = pending.get(key);
  if (p) return p;
  p = queue.then(async () => {
    const m = await load();
    // strict: no scripts or click handlers in the SVG (it goes into the page as markup), labels sanitized.
    // suppressErrorRendering: a bad source rejects instead of leaving Mermaid's bomb picture in the page.
    m.initialize({ startOnLoad: false, securityLevel: "strict", suppressErrorRendering: true, theme: dark ? "dark" : "default" });
    return (await m.render(`gerdr-mermaid-${++seq}`, code)).svg;
  });
  queue = p.catch(() => {});
  pending.set(key, p);
  p.then(
    (svg) => {
      done.set(key, svg);
      if (done.size > 200) done.delete(done.keys().next().value!); // the oldest
    },
    () => {},
  ).finally(() => pending.delete(key));
  return p;
}

/**
 * The SVG as an image for the lightbox (zoom, pan, download). Mermaid sizes its SVG to its container (width 100%),
 * which an <img> doesn't have: give it the size of its viewBox. And a background, as the page behind it is gone.
 *
 * Mermaid serializes flowcharts through innerHTML, which leaves unclosed HTML void tags (like <br> inside
 * <foreignObject> labels) that fail XML parsing when loaded into an <img> as image/svg+xml. Re-serializing through
 * XMLSerializer turns the parsed DOM into strict XML and avoids duplicate width/height attributes.
 */
export function mermaidImageURL(svg: string, dark: boolean): string {
  let out = svg;
  const box = document.createElement("div");
  box.innerHTML = svg;
  const el = box.querySelector("svg");
  if (el) {
    const vb = el
      .getAttribute("viewBox")
      ?.trim()
      .split(/[\s,]+/);
    if (vb && vb.length === 4) {
      el.setAttribute("width", vb[2]);
      el.setAttribute("height", vb[3]);
    } else {
      el.removeAttribute("width");
      el.removeAttribute("height");
    }
    el.setAttribute("xmlns", "http://www.w3.org/2000/svg");
    el.setAttribute("style", `background-color:${dark ? "#1e1e1e" : "#ffffff"}`);
    out = new XMLSerializer().serializeToString(el);
  }
  // One lightbox at a time: the previous diagram's URL can go
  if (lastURL) URL.revokeObjectURL(lastURL);
  return (lastURL = URL.createObjectURL(new Blob([out], { type: "image/svg+xml" })));
}
let lastURL = "";
