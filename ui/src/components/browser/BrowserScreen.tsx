import { useCallback, useEffect, useRef, useState } from "react";
import { toast } from "sonner";
import { ArrowLeft, ArrowRight, Check, Globe, Keyboard, MoreHorizontal, Plus, RotateCw, XCircle } from "lucide-react";
import { Button } from "@/components/ui/button";
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuSeparator, DropdownMenuTrigger } from "@/components/ui/dropdown-menu";
import { api, type BrowserInput } from "@/lib/api";
import { loadBrowserTabs, noteBrowserURL, showBrowserTab, useBrowserTabs } from "@/lib/actions";
import { t } from "@/lib/i18n";
import { paintFrame, parseFrame, wsURL, type StreamPage } from "@/lib/screenStream";
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
      return;
    }
    if (!h.w) return;
    const cv = canvasRef.current;
    if (!cv) return;
    await paintFrame(cv, buf, h, body);
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
      const ws = new WebSocket(wsURL(`/api/browser/view?tab=${tab}&w=${w}&q=${q}`));
      ws.binaryType = "arraybuffer";
      wsRef.current = ws;
      ws.onopen = () => {
        backoff = 500;
        ws.send(JSON.stringify({ t: "cfg", ...want() }));
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
  }, [live, tab, draw]);

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

  const onPointerDown = (e: React.PointerEvent<HTMLCanvasElement>) => {
    const p = at(e);
    if (!p) return;
    e.preventDefault();
    setEditing(false);
    // A touch doesn't take the keys: on a phone that opens the keyboard on every tap. The keyboard comes up when the
    // page's focus lands in a field (page.editable, below) or from the ⌨ button
    if (e.pointerType !== "touch") keysRef.current?.focus({ preventScroll: true });
    if (e.pointerType === "touch") {
      touch.current = { x: e.clientX, y: e.clientY, lastX: e.clientX, lastY: e.clientY, moved: false };
      return;
    }
    e.currentTarget.setPointerCapture(e.pointerId);
    const button = e.button === 2 ? "right" : "left";
    pressed.current = button;
    send({ kind: "down", ...p, button, count: Math.max(1, e.detail || 1), mods: modsOf(e) });
  };
  const onPointerMove = (e: React.PointerEvent<HTMLCanvasElement>) => {
    const tc = touch.current;
    if (e.pointerType === "touch" && tc) {
      // a finger drags the page like a phone's browser: scroll by how far it moved
      const dx = e.clientX - tc.lastX;
      const dy = e.clientY - tc.lastY;
      if (Math.abs(e.clientX - tc.x) + Math.abs(e.clientY - tc.y) > 8) tc.moved = true;
      if (tc.moved && (dx || dy)) {
        // the page is laid out at the pane's size, so a pixel here is a CSS pixel there
        const p = at(e) ?? { x: 0.5, y: 0.5 };
        send({ kind: "scroll", ...p, dxPx: -dx, dyPx: -dy });
        tc.lastX = e.clientX;
        tc.lastY = e.clientY;
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
    const tc = touch.current;
    if (e.pointerType === "touch" && tc) {
      touch.current = null;
      const p = at(e);
      if (!tc.moved && p) send({ kind: "click", ...p });
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
          {live && (
            <canvas
              ref={canvasRef}
              // Width-fit and top-aligned, not "contain": a phone's keyboard makes the pane shorter, and fitting the
              // whole picture into that shrank it (and the page under the finger) mid-tap
              className={cn("absolute top-0 left-0 h-auto w-full touch-none", !hasPic && "invisible")}
              onPointerDown={onPointerDown}
              onPointerMove={onPointerMove}
              onPointerUp={onPointerUp}
              onPointerCancel={() => ((pressed.current = ""), (touch.current = null))}
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
          {live && !hasPic && (
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
