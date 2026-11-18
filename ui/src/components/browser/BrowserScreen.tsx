import { useCallback, useEffect, useRef, useState } from "react";
import { toast } from "sonner";
import { ArrowLeft, ArrowRight, Check, Globe, Keyboard, MoreHorizontal, Plus, RotateCw, XCircle } from "lucide-react";
import { Button } from "@/components/ui/button";
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuSeparator, DropdownMenuTrigger } from "@/components/ui/dropdown-menu";
import { api, type BrowserInput } from "@/lib/api";
import { claimNative, focusPane, loadBrowserTabs, noteBrowserURL, releaseNative, showBrowserTab, useBrowserTabs, useNativeOwners } from "@/lib/actions";
import { t } from "@/lib/i18n";
import { paintFrame, parseFrame, wsURL, type StreamPage } from "@/lib/screenStream";
import { canNativeBrowser, nativeBrowserView } from "@/lib/native";
import { cn } from "@/lib/utils";

// The Browser pane's content: a page of the project's own browser (internal/projbrowser, docs/dev/BROWSER-PANE.md),
// shown through the remote screen's stream (server/screenstream.go: only changed tiles, as JPEG) and driven from here:
// the mouse, the wheel and the keyboard go back as input events (chromeext.View). The page is laid out at this pane's
// size, so it reads like a browser beside the chat; on a phone or another computer it is the same picture.
//
// Keys: a hidden textarea holds the focus while the picture is clicked. Printable text, the input method's composition
// and pasted text arrive there as text; named keys (Enter, arrows…) and editing shortcuts (⌘A / C / X / Z) go as keys.
// Other ⌘ combinations are left to Gerdr's own shortcuts. ⌘C / ⌘X also bring the page's selection back to this
// device's clipboard: the page's own clipboard is the computer's, not the viewer's.

const NAMED = new Set([
  "Enter",
  "Tab",
  "Escape",
  "Backspace",
  "Delete",
  "ArrowUp",
  "ArrowDown",
  "ArrowLeft",
  "ArrowRight",
  "Home",
  "End",
  "PageUp",
  "PageDown",
  ...Array.from({ length: 12 }, (_, i) => `F${i + 1}`),
]);
const EDITING = new Set(["a", "c", "x", "z", "y"]);
const QUALITY = 70;

type Props = {
  project: string;
  tab?: number;
  /** where the pane's page last was (opened again from here after a restart) */
  lastURL?: string;
  paneId: string;
  focused: boolean;
  /** the title bar's own look (Panes.tsx: height, padding, dimmed when not focused) */
  headClassName: string;
  onHeadPointerDown?: (e: React.PointerEvent<HTMLElement>) => void;
  /** after the address bar in the title bar (the phone's pane count, zoom's restore) */
  extra?: React.ReactNode;
  /** the ⋯ menu's items every pane has (Panes.tsx PaneMoveItems), below this pane's own */
  paneItems: React.ReactNode;
  /** wraps the picture (Panes.tsx PaneBody: the zoom animation's pinned size) */
  Body: React.ComponentType<{ children: React.ReactNode }>;
};

function modsOf(e: { metaKey: boolean; ctrlKey: boolean; altKey: boolean; shiftKey: boolean }) {
  const m: string[] = [];
  if (e.metaKey) m.push("cmd");
  if (e.ctrlKey) m.push("ctrl");
  if (e.altKey) m.push("opt");
  if (e.shiftKey) m.push("shift");
  return m;
}

/** What was typed in the address bar → a URL: a scheme is kept, something like a host gets one, the rest is a search. */
function toURL(s: string): string {
  const v = s.trim();
  if (!v) return "";
  if (/^[a-z][a-z0-9+.-]*:\/\//i.test(v) || v === "about:blank") return v;
  if (/^(localhost|127\.0\.0\.1|\[::1\]|0\.0\.0\.0)(:\d+)?(\/|$)/i.test(v)) return "http://" + v;
  if (!/\s/.test(v) && /^[^/]+\.[a-z]{2,}(:\d+)?(\/.*)?$/i.test(v)) return "https://" + v;
  return "https://www.google.com/search?q=" + encodeURIComponent(v);
}

function hostOf(u: string) {
  try {
    return new URL(u).host || u;
  } catch {
    return u;
  }
}

export function BrowserScreen({ project, tab, lastURL, paneId, focused, headClassName, onHeadPointerDown, extra, paneItems, Body }: Props) {
  const all = useBrowserTabs((s) => s.tabs);
  const loaded = useBrowserTabs((s) => s.loaded);
  useEffect(() => {
    if (!loaded) void loadBrowserTabs();
  }, [loaded]);
  const pages = all.filter((x) => x.project === project && !x.gone).sort((a, b) => a.opened - b.opened);
  const cur = tab ? all.find((x) => x.tab === tab) : undefined;
  // Gone: the list is loaded and doesn't have it (Gerdr restarted, it was closed). The stream ending refreshes the list.
  // An error in a frame (nothing drawn yet) is passing: shown only while there is no picture.
  const [streamErr, setStreamErr] = useState("");
  const gone = !!tab && loaded && (!cur || !!cur.gone);
  const live = !!tab && !gone;
  // A page of the macOS App's own browser, shown here as the real window over the box (native.ts nativeBrowserView);
  // the stream then only carries where the page is (pic=0) and the address bar's input
  // One page is one window: the first pane on it owns it, another pane on the same page shows its pictures instead
  const owner = useNativeOwners((o) => (tab ? o[tab] : undefined));
  const canNative = live && !!cur?.native && canNativeBrowser();
  useEffect(() => {
    if (!canNative || !tab) return;
    claimNative(tab, paneId);
    return () => releaseNative(tab, paneId);
  }, [canNative, tab, paneId]);
  // the owner went (its pane closed, it moved to another page): take it over
  useEffect(() => {
    if (canNative && tab && !owner) claimNative(tab, paneId);
  }, [owner, canNative, tab, paneId]);
  const nativeView = canNative && owner === paneId;

  const [page, setPage] = useState<StreamPage | null>(null);
  const [addr, setAddr] = useState("");
  const [editing, setEditing] = useState(false);
  const [opening, setOpening] = useState(false);
  const [hasPic, setHasPic] = useState(false);
  const canvasRef = useRef<HTMLCanvasElement>(null);
  const boxRef = useRef<HTMLDivElement>(null);
  const keysRef = useRef<HTMLTextAreaElement>(null);
  const addrRef = useRef<HTMLInputElement>(null);
  const wsRef = useRef<WebSocket | null>(null);
  const seqRef = useRef(0);

  useEffect(() => {
    setPage(null);
    setHasPic(false);
    setStreamErr("");
  }, [tab]);

  const want = () => {
    const el = boxRef.current;
    const dpr = window.devicePixelRatio || 1;
    const vw = el ? Math.round(el.clientWidth) : 1280;
    const vh = el ? Math.round(el.clientHeight) : 800;
    // The page's own pixels (the project browser draws at 2x): capped any lower, a 1600 px pane on a Retina screen was
    // shrunk by a fraction and every glyph resampled, which read as blurry. 4000 is the server's limit.
    return { w: Math.min(4000, Math.max(320, Math.round(vw * dpr))), q: QUALITY, vw, vh };
  };
  const sendWS = (m: object) => {
    const ws = wsRef.current;
    if (ws?.readyState !== WebSocket.OPEN) return false;
    ws.send(JSON.stringify(m));
    return true;
  };
  const send = (input: BrowserInput) => sendWS({ t: "input", seq: ++seqRef.current, input });

  // One frame at a time, in order
  useEffect(() => {
    const u = page?.url;
    if (u && u !== "about:blank" && u !== lastURL) noteBrowserURL(paneId, u);
  }, [page?.url]);

  const draw = useCallback(async (buf: ArrayBuffer) => {
    const { h, body } = parseFrame(buf);
    if (h.page) setPage(h.page);
    if (h.text) void navigator.clipboard?.writeText(h.text).catch(() => {});
    if (h.inputErr) toast.error(h.inputErr);
    if (h.err) {
      setStreamErr(h.err);
      if (h.seq) sendWS({ t: "got", seq: h.seq });
      return;
    }
    if (!h.w) {
      // nothing to draw (an input's acknowledgement, a page update): acknowledged at once
      if (h.seq) sendWS({ t: "got", seq: h.seq });
      return;
    }
    const cv = canvasRef.current;
    if (!cv) return;
    await paintFrame(cv, buf, h, body);
    // drawn: the server sends the next picture only once this one is in (flow control, server/screenstream.go)
    if (h.seq) sendWS({ t: "got", seq: h.seq });
    setHasPic(true);
    setStreamErr("");
  }, []);

  // The stream: open while the page is alive and this page visible; reconnects with a backoff
  useEffect(() => {
    if (!live) return;
    let alive = true;
    let retry: ReturnType<typeof setTimeout> | undefined;
    let backoff = 500;
    let queue = Promise.resolve();
    const connect = () => {
      if (!alive || document.visibilityState !== "visible") return;
      const { w, q } = want();
      const ws = new WebSocket(wsURL(`/api/browser/view?tab=${tab}&w=${w}&q=${q}${nativeView ? "&pic=0" : ""}`));
      ws.binaryType = "arraybuffer";
      wsRef.current = ws;
      ws.onopen = () => {
        backoff = 500;
        if (!nativeView) ws.send(JSON.stringify({ t: "cfg", ...want() }));
      };
      ws.onmessage = (ev) => {
        const buf = ev.data as ArrayBuffer;
        queue = queue.then(() => draw(buf)).catch(() => {});
      };
      ws.onclose = () => {
        if (wsRef.current === ws) wsRef.current = null;
        if (!alive) return;
        retry = setTimeout(connect, backoff);
        backoff = Math.min(backoff * 2, 5000);
        void loadBrowserTabs(); // closed because the page went? the list says so
      };
    };
    const onVis = () => {
      if (document.visibilityState === "visible") {
        if (!wsRef.current) connect();
      } else wsRef.current?.close();
    };
    document.addEventListener("visibilitychange", onVis);
    connect();
    return () => {
      alive = false;
      clearTimeout(retry);
      document.removeEventListener("visibilitychange", onVis);
      wsRef.current?.close();
      wsRef.current = null;
    };
  }, [live, tab, draw, nativeView]);

  // The real page over the box: where the box is, kept up to date (a split dragged, the sidebar toggled, the window
  // resized), off screen while the box isn't shown. The page is a window of its own above ours, so nothing of ours can
  // be drawn over it: while one of our menus / dialogs / toasts overlaps the box, the box shows a still of the page
  // (taken as it opens) and the real page steps aside, back once they're gone. Cutting their shape out of the page
  // instead (Lightron's View.Mask) kept the page live but needed the clicks and the keyboard handed between the two
  // windows by hand, and each case was its own bug (a rename dialog's box that took no typing). Checked every frame
  // while any is open (they animate in), every 250 ms otherwise.
  // The still and where it goes: exactly where the page is (placeView rounds inwards and rounds its corners), at its
  // size; stretched over the whole box it read soft and a pixel off.
  // Drawn into a canvas from a decoded bitmap, by hand: an <img> decodes on its own schedule (a big image is left out
  // of the frame it is first drawn in, and a second Image() having decoded the same URL doesn't change that), so the
  // page stepped aside over an empty box for a frame: a black flash at most menu opens (a recording, frame by frame).
  // A canvas shows what was drawn into it in the very next frame.
  const stillRef = useRef<HTMLCanvasElement>(null);
  const placed = useRef({ left: 0, top: 0, width: 0, height: 0, radius: 0 });
  // A click in the App's page (a view drawn over this box, which never sees the click): this pane becomes the focused
  // one, as a click anywhere else in it does (desktop/core views.go OnFocus)
  useEffect(() => {
    if (!nativeView || !tab) return;
    const on = (e: Event) => (e as CustomEvent).detail === tab && focusPane(paneId);
    window.addEventListener("gerdr:browserfocus", on);
    return () => window.removeEventListener("gerdr:browserfocus", on);
  }, [nativeView, tab, paneId]);
  useEffect(() => {
    if (!nativeView || !tab) return;
    const box = boxRef.current;
    if (!box) return;
    let last = "";
    let raf = 0;
    let covered = false; // one of ours is over the box: the still shows, the page is off
    let shot = 0; // which cover the still being taken is for
    // A still kept warm for when one of ours comes up over the box: taken when the pointer comes back from the page to
    // our UI (it has to, before anything of ours is clicked) and again at each press. A menu opens on the press itself,
    // before a still taken then is ready: waiting for one let the live page cover the menu for a moment (half the
    // time, seen on a recording). It may be a little old: shown at once, and replaced by a fresh one when that comes.
    let warm: ImageBitmap | null = null;
    let taking: Promise<ImageBitmap | null> | null = null; // one still at a time; a second ask shares it
    const overlays = () =>
      Array.from(
        document.querySelectorAll<HTMLElement>(
          "[data-radix-popper-content-wrapper] > *, [role=dialog], [role=alertdialog], [role=tooltip], [data-sonner-toast]",
        ),
      );
    // The still: from the App's own process when it can (desktop/main.js gerdr:shot, a data URL in tens of ms, as
    // Claude Desktop does), else through the backend (an older App). Decoded here into a bitmap, so showing it is one
    // synchronous drawImage (see stillRef).
    const take = (): Promise<ImageBitmap | null> =>
      (taking ??= (async () => {
        const native = (window as any).gerdrNative?.browserShot as ((t: number) => Promise<string | null>) | undefined;
        const u = await (native ? native(tab) : api.browserShot(tab)).catch(() => null);
        if (!u) return null;
        try {
          const img = new Image();
          img.src = u;
          await img.decode();
          const bmp = await createImageBitmap(img);
          warm?.close(); // what it showed is in the canvas already
          warm = bmp;
          return bmp;
        } catch {
          return null;
        } finally {
          if (u.startsWith("blob:")) URL.revokeObjectURL(u);
        }
      })().finally(() => (taking = null)));
    const placedOnScreen = () => !covered && !!last && last !== "hidden";
    let inPage = true; // the pointer is over the page (our document gets no events then)
    const back = () => {
      if (!inPage) return;
      inPage = false;
      if (placedOnScreen()) void take();
    };
    const away = () => (inPage = true);
    const press = () => placedOnScreen() && void take();
    document.addEventListener("pointermove", back, true);
    document.documentElement.addEventListener("pointerleave", away);
    document.addEventListener("pointerdown", press, true);
    const drop = () => {
      const cv = stillRef.current;
      if (cv) cv.style.display = "none";
    };
    // false: nothing was drawn (the bitmap was closed or empty), the page has to stay
    const show = (bmp: ImageBitmap): boolean => {
      const cv = stillRef.current;
      if (!cv || !bmp.width || !bmp.height) return false;
      // setting a canvas's size clears it, even to the same value: only when it changed, then drawn in the same task
      if (cv.width !== bmp.width || cv.height !== bmp.height) {
        cv.width = bmp.width;
        cv.height = bmp.height;
      }
      try {
        cv.getContext("2d")!.drawImage(bmp, 0, 0);
      } catch {
        return false; // a closed bitmap
      }
      const p = placed.current;
      Object.assign(cv.style, {
        left: p.left + "px",
        top: p.top + "px",
        width: p.width + "px",
        height: p.height + "px",
        borderRadius: p.radius + "px",
        display: "block",
      });
      return true;
    };
    const cover = (bmp: ImageBitmap, n: number): boolean => {
      if (!show(bmp)) return false;
      last = "covered";
      // the page steps aside once the still is on screen (drawn above; the frame after this one has it)
      requestAnimationFrame(() =>
        requestAnimationFrame(() => {
          if (covered && n === shot) nativeBrowserView(tab, null);
        }),
      );
      return true;
    };
    const report = () => {
      const r = box.getBoundingClientRect();
      const shown = document.visibilityState === "visible" && r.width > 20 && r.height > 20 && box.offsetParent !== null;
      const open = overlays();
      const over = open.some((o) => {
        const q = o.getBoundingClientRect();
        return q.width > 0 && q.left < r.right && q.right > r.left && q.top < r.bottom && q.bottom > r.top;
      });
      if (open.length && !raf) raf = requestAnimationFrame(() => ((raf = 0), report()));
      if (over && !covered) {
        covered = true;
        const n = ++shot;
        const had = !!warm && cover(warm, n);
        // a fresh one: replaces the warm one when it comes (or is the first, the page staying until then)
        void take().then((b) => {
          if (!b || !covered || n !== shot) return;
          if (!had) cover(b, n);
          else show(b);
        });
      } else if (!over && covered) {
        covered = false;
        const n = ++shot;
        last = ""; // placed again below; the still goes once the page is back over it
        // only if nothing of ours came back over it meanwhile: dropping the still then left the box black
        requestAnimationFrame(() => requestAnimationFrame(() => !covered && n === shot && drop()));
      }
      if (covered) return;
      // the pane's corners, inside its border: the page window has to be rounded as they are
      const pane = box.closest<HTMLElement>("[data-pane-id]");
      const cs = pane ? getComputedStyle(pane) : null;
      const radius = cs ? Math.max(0, (parseFloat(cs.borderBottomLeftRadius) || 0) - (parseFloat(cs.borderBottomWidth) || 0)) : 0;
      const key = shown ? [r.x, r.y, r.width, r.height, window.devicePixelRatio, radius].map((n) => Math.round(n * 10) / 10).join(",") : "hidden";
      if (key === last) return;
      last = key;
      if (shown) {
        // where desktop/main.js placeView puts the page (at the page's zoom of 1): inwards to whole pixels
        const left = Math.ceil(r.x),
          top = Math.ceil(r.y);
        placed.current = { left: left - r.x, top: top - r.y, width: Math.floor(r.x + r.width) - left, height: Math.floor(r.y + r.height) - top, radius };
      }
      nativeBrowserView(tab, shown ? r : null, radius);
    };
    const ro = new ResizeObserver(report);
    ro.observe(box);
    const mo = new MutationObserver(report);
    mo.observe(document.body, { childList: true });
    const iv = setInterval(report, 250);
    window.addEventListener("resize", report);
    document.addEventListener("visibilitychange", report);
    report();
    return () => {
      cancelAnimationFrame(raf);
      ro.disconnect();
      mo.disconnect();
      clearInterval(iv);
      window.removeEventListener("resize", report);
      document.removeEventListener("visibilitychange", report);
      document.removeEventListener("pointerdown", press, true);
      document.removeEventListener("pointermove", back, true);
      document.documentElement.removeEventListener("pointerleave", away);
      nativeBrowserView(tab, null);
      covered = false;
      shot++;
      drop();
      warm?.close();
      warm = null;
    };
  }, [nativeView, tab]);

  // The page follows the pane's size (debounced: a drag of the split sends one resize at the end)
  useEffect(() => {
    const el = boxRef.current;
    if (!el || !live) return;
    let timer: ReturnType<typeof setTimeout> | undefined;
    const ro = new ResizeObserver(() => {
      clearTimeout(timer);
      timer = setTimeout(() => sendWS({ t: "cfg", ...want() }), 150);
    });
    ro.observe(el);
    return () => {
      clearTimeout(timer);
      ro.disconnect();
    };
  }, [live]);

  // ---- the picture: pointer → fractions of the page's viewport ----
  const at = (e: { clientX: number; clientY: number }) => {
    const cv = canvasRef.current;
    if (!cv || !cv.width) return null;
    // The canvas is as wide as the pane and as tall as its picture says (width-fit, top-aligned), so its box is
    // the picture
    const r = cv.getBoundingClientRect();
    const x = (e.clientX - r.left) / r.width;
    const y = (e.clientY - r.top) / r.height;
    if (x < 0 || x > 1 || y < 0 || y > 1) return null;
    return { x, y };
  };
  const pressed = useRef<"left" | "right" | "">("");
  const touch = useRef<{ x: number; y: number; lastY: number; lastX: number; moved: boolean } | null>(null);
  const moveAt = useRef(0);

  // Zoom on a phone: pinch with two fingers, double-tap to zoom in / out; zoomed in, a finger moves the picture and
  // what's left past its edge scrolls the page. Only the picture is scaled (a CSS transform), so a point still maps
  // through the canvas box (at). Lives here, per pane; a new page starts unzoomed.
  const [zoom, setZoomState] = useState({ s: 1, x: 0, y: 0 });
  const zoomRef = useRef(zoom);
  const setZoom = (z: { s: number; x: number; y: number }) => {
    const box = boxRef.current;
    const cv = canvasRef.current;
    if (box && cv) {
      // keep the picture over the box: no empty margin when zoomed in
      const w = box.clientWidth * z.s;
      const h = cv.clientHeight * z.s;
      z = { s: z.s, x: Math.min(0, Math.max(box.clientWidth - w, z.x)), y: Math.min(0, Math.max(Math.min(0, box.clientHeight - h), z.y)) };
    }
    if (z.s <= 1.02) z = { s: 1, x: 0, y: 0 };
    zoomRef.current = z;
    setZoomState(z);
  };
  useEffect(() => setZoom({ s: 1, x: 0, y: 0 }), [tab]);
  const fingers = useRef(new Map<number, { x: number; y: number }>());
  const pinch = useRef<{ d: number; s: number; cx: number; cy: number } | null>(null);
  const lastTap = useRef<{ t: number; x: number; y: number } | null>(null);
  const boxPoint = (cx: number, cy: number) => {
    const r = boxRef.current!.getBoundingClientRect();
    return { x: cx - r.left, y: cy - r.top };
  };
  const zoomAround = (s: number, px: number, py: number, from = zoomRef.current, at0 = { x: px, y: py }) => {
    // the content point under at0 (box px, at zoom `from`) ends up under (px, py) at zoom s
    const cx = (at0.x - from.x) / from.s;
    const cy = (at0.y - from.y) / from.s;
    setZoom({ s: Math.max(1, Math.min(6, s)), x: px - cx * s, y: py - cy * s });
  };

  const onPointerDown = (e: React.PointerEvent<HTMLCanvasElement>) => {
    const p = at(e);
    if (!p) return;
    e.preventDefault();
    setEditing(false);
    // A touch doesn't take the keys: on a phone that opens the keyboard on every tap. The keyboard comes up when the
    // page's focus lands in a field (page.editable, below) or from the ⌨ button
    if (e.pointerType !== "touch") keysRef.current?.focus({ preventScroll: true });
    if (e.pointerType === "touch") {
      fingers.current.set(e.pointerId, { x: e.clientX, y: e.clientY });
      if (fingers.current.size === 2) {
        // a second finger: a pinch, not a tap or a scroll
        const [a, b] = [...fingers.current.values()];
        const m = boxPoint((a.x + b.x) / 2, (a.y + b.y) / 2);
        pinch.current = { d: Math.hypot(a.x - b.x, a.y - b.y), s: zoomRef.current.s, cx: m.x, cy: m.y };
        pinchFrom.current = zoomRef.current;
        touch.current = null;
        return;
      }
      touch.current = { x: e.clientX, y: e.clientY, lastX: e.clientX, lastY: e.clientY, moved: false };
      return;
    }
    e.currentTarget.setPointerCapture(e.pointerId);
    const button = e.button === 2 ? "right" : "left";
    pressed.current = button;
    send({ kind: "down", ...p, button, count: Math.max(1, e.detail || 1), mods: modsOf(e) });
  };
  const pinchFrom = useRef(zoom);
  const onPointerMove = (e: React.PointerEvent<HTMLCanvasElement>) => {
    if (e.pointerType === "touch") {
      if (fingers.current.has(e.pointerId)) fingers.current.set(e.pointerId, { x: e.clientX, y: e.clientY });
      const pc = pinch.current;
      if (pc && fingers.current.size >= 2) {
        const [a, b] = [...fingers.current.values()];
        const m = boxPoint((a.x + b.x) / 2, (a.y + b.y) / 2);
        zoomAround((pc.s * Math.hypot(a.x - b.x, a.y - b.y)) / pc.d, m.x, m.y, pinchFrom.current, { x: pc.cx, y: pc.cy });
        return;
      }
    }
    const tc = touch.current;
    if (e.pointerType === "touch" && tc) {
      const dx = e.clientX - tc.lastX;
      const dy = e.clientY - tc.lastY;
      if (Math.abs(e.clientX - tc.x) + Math.abs(e.clientY - tc.y) > 8) tc.moved = true;
      if (tc.moved && (dx || dy)) {
        tc.lastX = e.clientX;
        tc.lastY = e.clientY;
        // zoomed in: the finger moves the picture first; what's left past its edge scrolls the page
        const z = zoomRef.current;
        let restX = dx;
        let restY = dy;
        if (z.s > 1) {
          setZoom({ s: z.s, x: z.x + dx, y: z.y + dy });
          restX = dx - (zoomRef.current.x - z.x);
          restY = dy - (zoomRef.current.y - z.y);
        }
        if (restX || restY) {
          // a finger drags the page like a phone's browser (a pixel here is about a CSS pixel there, divided by the zoom)
          const p = at(e) ?? { x: 0.5, y: 0.5 };
          send({ kind: "scroll", ...p, dxPx: -restX / z.s, dyPx: -restY / z.s });
        }
      }
      return;
    }
    // hover menus need the pointer's moves: about one a frame (a drag gets every one)
    const now = performance.now();
    if (!pressed.current && now - moveAt.current < 16) return;
    moveAt.current = now;
    const p = at(e);
    if (p) send({ kind: "move", ...p, button: pressed.current === "left" ? "left" : "", mods: modsOf(e) });
  };
  const onPointerUp = (e: React.PointerEvent<HTMLCanvasElement>) => {
    if (e.pointerType === "touch") {
      fingers.current.delete(e.pointerId);
      if (pinch.current) {
        if (fingers.current.size < 2) pinch.current = null;
        return;
      }
    }
    const tc = touch.current;
    if (e.pointerType === "touch" && tc) {
      touch.current = null;
      if (tc.moved) return;
      // a double tap zooms in on that spot, or back out; its second tap isn't a click
      const now = performance.now();
      const lt = lastTap.current;
      if (lt && now - lt.t < 300 && Math.hypot(e.clientX - lt.x, e.clientY - lt.y) < 30) {
        lastTap.current = null;
        const b = boxPoint(e.clientX, e.clientY);
        if (zoomRef.current.s > 1) setZoom({ s: 1, x: 0, y: 0 });
        else zoomAround(2.5, b.x, b.y);
        return;
      }
      lastTap.current = { t: now, x: e.clientX, y: e.clientY };
      const p = at(e);
      if (p) send({ kind: "click", ...p });
      return;
    }
    if (!pressed.current) return;
    const p = at(e) ?? { x: 0, y: 0 };
    send({ kind: "up", ...p, button: pressed.current, count: Math.max(1, e.detail || 1) });
    pressed.current = "";
  };

  // The wheel: a non-passive listener so the pane itself doesn't scroll; deltas summed per frame
  useEffect(() => {
    const cv = canvasRef.current;
    if (!cv || !live) return;
    let sum = { dx: 0, dy: 0, x: 0.5, y: 0.5 };
    let raf = 0;
    const onWheel = (e: WheelEvent) => {
      e.preventDefault();
      const k = e.deltaMode === 1 ? 40 : e.deltaMode === 2 ? 800 : 1;
      sum.dx += e.deltaX * k;
      sum.dy += e.deltaY * k;
      const p = at(e);
      if (p) sum = { ...sum, ...p };
      if (!raf)
        raf = requestAnimationFrame(() => {
          raf = 0;
          send({ kind: "scroll", x: sum.x, y: sum.y, dxPx: sum.dx, dyPx: sum.dy });
          sum = { ...sum, dx: 0, dy: 0 };
        });
    };
    cv.addEventListener("wheel", onWheel, { passive: false });
    return () => {
      cv.removeEventListener("wheel", onWheel);
      cancelAnimationFrame(raf);
    };
  }, [live]);

  // ---- keys ----
  const onKeyDown = (e: React.KeyboardEvent<HTMLTextAreaElement>) => {
    if (e.nativeEvent.isComposing || e.key === "Process") return;
    const mods = modsOf(e);
    const cmd = e.metaKey || e.ctrlKey;
    if (NAMED.has(e.key)) {
      e.preventDefault();
      e.stopPropagation();
      send({ kind: "key", key: e.key, mods: mods as any });
      return;
    }
    if (!cmd) return; // text: arrives as input
    const k = /^Key[A-Z]$/.test(e.code) ? e.code.slice(3).toLowerCase() : /^Digit\d$/.test(e.code) ? e.code.slice(5) : e.key.toLowerCase();
    if (k === "l") {
      // ⌘L: the address bar, as in a browser
      e.preventDefault();
      e.stopPropagation();
      setEditing(true);
      setAddr(page?.url ?? cur?.url ?? "");
      requestAnimationFrame(() => addrRef.current?.select());
      return;
    }
    if (k === "v") return; // the paste event brings this device's clipboard
    if (!EDITING.has(k)) return; // Gerdr's own shortcuts
    e.preventDefault();
    e.stopPropagation();
    if (k === "c" || k === "x") sendWS({ t: "copy" }); // answered with the selection, before a cut removes it
    send({ kind: "key", key: k, mods: mods as any });
  };
  const onInput = (e: React.FormEvent<HTMLTextAreaElement>) => {
    if ((e.nativeEvent as InputEvent).isComposing) return;
    const ta = e.currentTarget;
    const text = ta.value;
    ta.value = "";
    if (text) send({ kind: "text", text });
  };

  // ---- the address bar ----
  const go = async (raw: string) => {
    const url = toURL(raw);
    setEditing(false);
    if (!url) return;
    if (live) {
      send({ kind: "go", text: url });
      keysRef.current?.focus({ preventScroll: true });
      return;
    }
    setOpening(true);
    try {
      const opened = await api.browserOpen(url, project);
      showBrowserTab(paneId, project, opened.tab);
      void loadBrowserTabs();
    } catch (err) {
      toast.error((err as Error).message);
    } finally {
      setOpening(false);
    }
  };
  // a new empty pane: straight into the address bar
  useEffect(() => {
    if (!tab && focused) addrRef.current?.focus();
  }, [tab, focused]);

  // A phone's keyboard follows the page's focus: up when it lands in a field, down when it leaves. Android opens it
  // from here; iOS only from a tap, which the ⌨ button is
  const coarse = typeof matchMedia === "function" && matchMedia("(pointer: coarse)").matches;
  useEffect(() => {
    if (!coarse || !live) return;
    const ta = keysRef.current;
    if (!ta) return;
    if (page?.editable) ta.focus({ preventScroll: true });
    else if (document.activeElement === ta) ta.blur();
  }, [page?.editable, live]);
  const toggleKeys = () => {
    const ta = keysRef.current;
    if (!ta) return;
    if (document.activeElement === ta) ta.blur();
    else ta.focus({ preventScroll: true });
  };

  const shown = editing ? addr : (page?.url ?? cur?.url ?? (gone ? (lastURL ?? "") : ""));
  const title = page?.title || (cur ? hostOf(cur.url) : "");

  return (
    <>
      <div onPointerDown={onHeadPointerDown} className={cn("flex shrink-0 items-center gap-1 border-b px-2 text-[12px] max-md:pl-4", headClassName)}>
        <Globe className="mr-1 size-4 shrink-0 text-muted-foreground" />
        <Button
          variant="ghost"
          size="icon"
          className="size-6 shrink-0"
          disabled={!live || !page?.back}
          title={t("browser.back")}
          onClick={() => send({ kind: "back" })}
        >
          <ArrowLeft className="size-3.5" />
        </Button>
        <Button
          variant="ghost"
          size="icon"
          className="size-6 shrink-0 max-md:hidden"
          disabled={!live || !page?.forward}
          title={t("browser.forward")}
          onClick={() => send({ kind: "forward" })}
        >
          <ArrowRight className="size-3.5" />
        </Button>
        <Button variant="ghost" size="icon" className="size-6 shrink-0" disabled={!live} title={t("browser.reload")} onClick={() => send({ kind: "reload" })}>
          <RotateCw className={cn("size-3.5", page?.loading && "animate-spin")} />
        </Button>
        {coarse && (
          <Button
            variant="ghost"
            size="icon"
            className="size-6 shrink-0"
            disabled={!live}
            title={t("browser.keyboard")}
            onPointerDown={(e) => e.preventDefault()}
            onClick={toggleKeys}
          >
            <Keyboard className="size-3.5" />
          </Button>
        )}
        <form
          className="min-w-0 flex-1"
          onSubmit={(e) => {
            e.preventDefault();
            void go(addr);
          }}
        >
          <input
            ref={addrRef}
            value={shown}
            placeholder={t("browser.address")}
            title={title}
            disabled={opening}
            onPointerDown={(e) => e.stopPropagation()}
            onFocus={(e) => {
              if (!editing) {
                setEditing(true);
                setAddr(shown);
                const el = e.currentTarget;
                requestAnimationFrame(() => el.select());
              }
            }}
            onBlur={() => setEditing(false)}
            onChange={(e) => setAddr(e.target.value)}
            onKeyDown={(e) => {
              e.stopPropagation();
              if (e.key === "Escape") {
                setEditing(false);
                e.currentTarget.blur();
              }
            }}
            className="h-6 w-full min-w-0 rounded-md border border-transparent bg-muted/60 px-2 font-mono text-[11px] outline-none focus:border-brand focus:bg-background"
          />
        </form>
        {extra}
        {/* One ⋯ like every pane's: this pane's pages first, then the pane moves (split, new tab, close…) */}
        <DropdownMenu>
          <DropdownMenuTrigger asChild>
            <Button variant="ghost" size="icon" className="size-6 shrink-0" title={t("browser.pages")}>
              <MoreHorizontal className="size-3.5" />
            </Button>
          </DropdownMenuTrigger>
          <DropdownMenuContent align="end" className="max-w-80" onCloseAutoFocus={(e) => e.preventDefault()}>
            {pages.map((p) => (
              <DropdownMenuItem key={p.tab} onClick={() => showBrowserTab(paneId, project, p.tab)} className={cn(p.tab === tab && "font-medium")}>
                <Globe className={cn(p.tab === tab ? "text-brand" : "text-muted-foreground")} />
                <span className="truncate">{p.tab === tab && page?.title ? page.title : hostOf(p.url)}</span>
                {p.tab === tab && <Check className="ml-auto" />}
              </DropdownMenuItem>
            ))}
            <DropdownMenuItem onClick={() => (showBrowserTab(paneId, project, undefined), requestAnimationFrame(() => addrRef.current?.focus()))}>
              <Plus /> {t("browser.newPage")}
            </DropdownMenuItem>
            {live && (
              <DropdownMenuItem
                onClick={() => {
                  void api
                    .browserClose(tab!)
                    .then(() => loadBrowserTabs())
                    .catch((err) => toast.error((err as Error).message));
                  const next = pages.filter((p) => p.tab !== tab).at(-1);
                  showBrowserTab(paneId, project, next?.tab);
                }}
              >
                <XCircle /> {t("browser.closePage")}
              </DropdownMenuItem>
            )}
            <DropdownMenuSeparator />
            {paneItems}
          </DropdownMenuContent>
        </DropdownMenu>
      </div>
      <Body>
        <div ref={boxRef} className="relative h-full w-full overflow-hidden bg-background">
          {nativeView && <canvas ref={stillRef} className="pointer-events-none absolute hidden max-w-none select-none" />}
          {live && !nativeView && (
            <canvas
              ref={canvasRef}
              // Width-fit and top-aligned, not "contain": a phone's keyboard makes the pane shorter, and fitting the
              // whole picture into that shrank it (and the page under the finger) mid-tap
              className={cn("absolute top-0 left-0 h-auto w-full touch-none", !hasPic && "invisible")}
              style={zoom.s !== 1 ? { transform: `translate(${zoom.x}px, ${zoom.y}px) scale(${zoom.s})`, transformOrigin: "0 0" } : undefined}
              onPointerDown={onPointerDown}
              onPointerMove={onPointerMove}
              onPointerUp={onPointerUp}
              onPointerCancel={(e) => {
                pressed.current = "";
                touch.current = null;
                fingers.current.delete(e.pointerId);
                if (fingers.current.size < 2) pinch.current = null;
              }}
              onContextMenu={(e) => e.preventDefault()}
            />
          )}
          {/* Keys while the picture has the focus; offscreen, not display:none (it must take focus and the IME) */}
          <textarea
            ref={keysRef}
            aria-hidden
            autoCapitalize="off"
            autoCorrect="off"
            spellCheck={false}
            className="pointer-events-none absolute top-0 left-0 h-px w-px resize-none opacity-0"
            onKeyDown={onKeyDown}
            onInput={onInput}
            onCompositionUpdate={(e) => send({ kind: "compose", text: e.data })}
            onCompositionEnd={(e) => {
              e.currentTarget.value = "";
              send({ kind: "text", text: e.data });
            }}
            onPaste={(e) => {
              e.preventDefault();
              const text = e.clipboardData.getData("text");
              if (text) send({ kind: "text", text });
            }}
          />
          {live && !nativeView && !hasPic && (
            <div className="absolute inset-0 grid place-items-center p-6 text-center text-[13px] text-muted-foreground">
              {streamErr || t("browser.loading")}
            </div>
          )}
          {!tab && (
            <div className="absolute inset-0 grid place-items-center p-6 text-center">
              <div className="max-w-sm space-y-2">
                <Globe className="mx-auto size-8 text-muted-foreground" />
                <div className="text-[13px] font-medium">{t("browser.empty")}</div>
                <div className="text-[12px] text-muted-foreground">{t("browser.emptyHint", { project })}</div>
              </div>
            </div>
          )}
          {gone && (
            <div className="absolute inset-0 grid place-items-center p-6 text-center">
              <div className="max-w-sm space-y-3">
                <div className="text-[13px] text-muted-foreground">{cur?.gone || t("browser.gone")}</div>
                {(page?.url || cur?.url || lastURL) && (
                  <Button size="sm" variant="outline" disabled={opening} onClick={() => void go(page?.url || cur?.url || lastURL!)}>
                    {t("browser.reopen")}
                  </Button>
                )}
              </div>
            </div>
          )}
        </div>
      </Body>
    </>
  );
}
