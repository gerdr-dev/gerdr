import { useCallback, useEffect, useLayoutEffect, useRef, useState } from "react";
import { toast } from "sonner";
import { ArrowDown, ArrowUp, Eye, EyeOff, Minus, Plus, RefreshCw } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogTitle } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { api, ApiError, type ScreenInput, type ScreenStatus } from "@/lib/api";
import { t } from "@/lib/i18n";
import type { MessageKey } from "@/lib/messages";
import { closeDialog } from "@/lib/store";
import { cn } from "@/lib/utils";

// Settings → Labs → Remote screen (backend: internal/screen). Not a remote desktop: a still screenshot, taken again
// every few seconds and right after each action. Tapping the picture clicks the same spot on the computer; the text
// box below types into whatever has focus there (an app's password field, say).

type Mode = "click" | "double" | "right";
const MODES: { mode: Mode; label: MessageKey }[] = [
  { mode: "click", label: "remoteScreen.mode.click" },
  { mode: "double", label: "remoteScreen.mode.double" },
  { mode: "right", label: "remoteScreen.mode.right" },
];

type Key = { label: string; key: string; mods?: ("cmd" | "shift" | "opt" | "ctrl")[] };
const KEYS: Key[] = [
  { label: "Esc", key: "esc" },
  { label: "Tab", key: "tab" },
  { label: "⌫", key: "backspace" },
  { label: "⏎", key: "enter" },
  { label: "←", key: "left" },
  { label: "↑", key: "up" },
  { label: "↓", key: "down" },
  { label: "→", key: "right" },
  // Spotlight: the quickest way to open an app from here
  { label: "⌘Space", key: "space", mods: ["cmd"] },
  { label: "⌘Tab", key: "tab", mods: ["cmd"] },
  { label: "⌘A", key: "a", mods: ["cmd"] },
];

const IDLE_REFRESH = 3000;
// Zoom: 1 = the whole screen fits the box; pinch (touch), ⌃-wheel / trackpad pinch (desktop) or the ± buttons.
const MIN_SCALE = 1;
const MAX_SCALE = 8;
const clampScale = (v: number) => Math.max(MIN_SCALE, Math.min(MAX_SCALE, v));

// Picture quality: width the screenshot is shrunk to, JPEG quality. Each device keeps its own (a phone on mobile
// data wants the small one, a computer the sharp one). Zoomed to 2× or more, at least Sharp's width.
type Quality = "low" | "std" | "high";
const QUALITY: Record<Quality, { w: number; q: number; label: MessageKey }> = {
  low: { w: 1280, q: 45, label: "remoteScreen.quality.low" },
  std: { w: 1600, q: 60, label: "remoteScreen.quality.std" },
  high: { w: 2400, q: 75, label: "remoteScreen.quality.high" },
};
const QUALITY_KEY = "gerdr.screenQuality";
function loadQuality(): Quality {
  try {
    const v = localStorage.getItem(QUALITY_KEY);
    return v === "low" || v === "high" ? v : "std";
  } catch {
    return "std";
  }
}

export function RemoteScreen({ open }: { open: boolean }) {
  const [st, setSt] = useState<ScreenStatus | null>(null);
  const [img, setImg] = useState<string | null>(null);
  const [shotErr, setShotErr] = useState("");
  const [loading, setLoading] = useState(false);
  const [mode, setMode] = useState<Mode>("click");
  const [scale, setScaleState] = useState(1);
  const [mark, setMark] = useState<{ x: number; y: number } | null>(null);
  const [text, setText] = useState("");
  const [showText, setShowText] = useState(false);
  const [granting, setGranting] = useState(false);

  // One screenshot in flight at a time; asking again meanwhile just takes another when it lands.
  const busy = useRef(false);
  const again = useRef(false);
  const timer = useRef<ReturnType<typeof setTimeout>>(undefined);
  const alive = useRef(false);
  const imgUrl = useRef<string | null>(null);
  const scaleRef = useRef(1);
  scaleRef.current = scale;
  const [quality, setQualityState] = useState<Quality>(loadQuality);
  const qualityRef = useRef(quality);
  qualityRef.current = quality;
  // The picture's box: fitted inside the scroll area (both ways) at zoom 1, then scaled; zoomed in, the area scrolls
  const boxRef = useRef<HTMLDivElement>(null);
  const [box, setBox] = useState({ w: 0, h: 0 });
  const [aspect, setAspect] = useState(0); // width / height of the screenshot
  useEffect(() => {
    const el = boxRef.current;
    if (!open || !el) return;
    const ro = new ResizeObserver(() => setBox({ w: el.clientWidth, h: el.clientHeight }));
    ro.observe(el);
    return () => ro.disconnect();
  }, [open, img]);
  const ar = aspect || (st?.width && st.height ? st.width / st.height : 16 / 10);
  const fitW = box.w && box.h ? Math.min(box.w, box.h * ar) : 0;

  // Zooming keeps one spot of the picture under one point on the page (the fingers, the cursor, the box's centre):
  // remember which fraction of the picture is there, re-scroll after the new size is laid out.
  const picRef = useRef<HTMLDivElement>(null);
  const anchor = useRef<{ fx: number; fy: number; cx: number; cy: number } | null>(null);
  const zoomTo = useCallback((next: number, cx: number, cy: number, from?: { fx: number; fy: number }) => {
    const pic = picRef.current;
    if (!pic) return;
    let f = from;
    if (!f) {
      const r = pic.getBoundingClientRect();
      f = { fx: (cx - r.left) / r.width, fy: (cy - r.top) / r.height };
    }
    anchor.current = { ...f, cx, cy };
    next = clampScale(next);
    scaleRef.current = next;
    setScaleState(next);
  }, []);
  useLayoutEffect(() => {
    const a = anchor.current;
    const el = boxRef.current;
    const pic = picRef.current;
    if (!a || !el || !pic) return;
    anchor.current = null;
    const r = pic.getBoundingClientRect();
    el.scrollLeft += r.left + a.fx * r.width - a.cx;
    el.scrollTop += r.top + a.fy * r.height - a.cy;
  }, [scale]);
  const zoomCentre = (factor: number) => {
    const r = boxRef.current?.getBoundingClientRect();
    if (r) zoomTo(scaleRef.current * factor, r.left + r.width / 2, r.top + r.height / 2);
  };

  // Pinch on touch screens. touch-action on the box leaves one-finger panning to the browser and takes two-finger
  // zoom away from it (it would zoom the whole page). The click a lifted finger may send right after is ignored.
  const pinchedAt = useRef(0);
  useEffect(() => {
    const el = boxRef.current;
    if (!open || !el) return;
    let start: { dist: number; scale: number; fx: number; fy: number } | null = null;
    const two = (e: TouchEvent) => {
      const [a, b] = [e.touches[0], e.touches[1]];
      return { dist: Math.hypot(a.clientX - b.clientX, a.clientY - b.clientY), cx: (a.clientX + b.clientX) / 2, cy: (a.clientY + b.clientY) / 2 };
    };
    const onStart = (e: TouchEvent) => {
      if (e.touches.length !== 2 || !picRef.current) return;
      const p = two(e);
      const r = picRef.current.getBoundingClientRect();
      start = { dist: p.dist || 1, scale: scaleRef.current, fx: (p.cx - r.left) / r.width, fy: (p.cy - r.top) / r.height };
    };
    const onMove = (e: TouchEvent) => {
      if (!start || e.touches.length !== 2) return;
      e.preventDefault();
      const p = two(e);
      zoomTo(start.scale * (p.dist / start.dist), p.cx, p.cy, start);
    };
    const onEnd = (e: TouchEvent) => {
      if (start && e.touches.length < 2) {
        start = null;
        pinchedAt.current = Date.now();
      }
    };
    // ⌃-wheel: what a trackpad pinch arrives as in Chromium / Electron
    const onWheel = (e: WheelEvent) => {
      if (!e.ctrlKey) return;
      e.preventDefault();
      zoomTo(scaleRef.current * Math.exp(-e.deltaY * 0.01), e.clientX, e.clientY);
    };
    el.addEventListener("touchstart", onStart, { passive: true });
    el.addEventListener("touchmove", onMove, { passive: false });
    el.addEventListener("touchend", onEnd);
    el.addEventListener("touchcancel", onEnd);
    el.addEventListener("wheel", onWheel, { passive: false });
    return () => {
      el.removeEventListener("touchstart", onStart);
      el.removeEventListener("touchmove", onMove);
      el.removeEventListener("touchend", onEnd);
      el.removeEventListener("touchcancel", onEnd);
      el.removeEventListener("wheel", onWheel);
    };
  }, [open, img !== null, zoomTo]);

  const shoot = useCallback(async () => {
    if (!alive.current) return;
    clearTimeout(timer.current);
    if (busy.current) {
      again.current = true;
      return;
    }
    busy.current = true;
    setLoading(true);
    try {
      const ql = QUALITY[qualityRef.current];
      // Zoomed in, a sharper picture is worth the bytes
      const w = scaleRef.current >= 2 ? Math.max(ql.w, QUALITY.high.w) : ql.w;
      const res = await fetch(`/api/screen/shot?w=${w}&q=${ql.q}`, { credentials: "same-origin", cache: "no-store" });
      if (!res.ok) {
        const body = await res.json().catch(() => ({}));
        throw new Error(body.error || res.statusText);
      }
      const url = URL.createObjectURL(await res.blob());
      if (!alive.current) {
        URL.revokeObjectURL(url);
        return;
      }
      if (imgUrl.current) URL.revokeObjectURL(imgUrl.current);
      imgUrl.current = url;
      setImg(url);
      setShotErr("");
    } catch (e) {
      setShotErr(e instanceof Error ? e.message : String(e));
    } finally {
      busy.current = false;
      setLoading(false);
    }
    if (!alive.current) return;
    if (again.current) {
      again.current = false;
      void shoot();
      return;
    }
    timer.current = setTimeout(() => {
      if (document.visibilityState === "visible") void shoot();
      else timer.current = setTimeout(() => void shoot(), IDLE_REFRESH);
    }, IDLE_REFRESH);
  }, []);

  const loadStatus = useCallback(
    () =>
      api
        .screen()
        .then((s) => {
          setSt(s);
          return s;
        })
        .catch(() => null),
    [],
  );

  useEffect(() => {
    if (!open) return;
    alive.current = true;
    void loadStatus().then((s) => {
      if (s?.enabled && s.screen) void shoot();
    });
    return () => {
      alive.current = false;
      clearTimeout(timer.current);
      if (imgUrl.current) URL.revokeObjectURL(imgUrl.current);
      imgUrl.current = null;
      setImg(null);
      setMark(null);
      setText("");
      setScaleState(1);
    };
  }, [open, loadStatus, shoot]);

  // Waiting on a grant: the user flips it in System Settings, nothing tells us, so ask again every few seconds
  const missing = !!st && (!st.enabled || !st.screen || !st.input);
  useEffect(() => {
    if (!open || !missing) return;
    const iv = setInterval(
      () =>
        void loadStatus().then((s) => {
          if (s?.enabled && s.screen && !imgUrl.current) void shoot();
        }),
      4000,
    );
    return () => clearInterval(iv);
  }, [open, missing, loadStatus, shoot]);

  const setQuality = (q: Quality) => {
    setQualityState(q);
    qualityRef.current = q;
    try {
      localStorage.setItem(QUALITY_KEY, q);
    } catch {}
    void shoot();
  };

  const send = async (i: ScreenInput) => {
    try {
      await api.screenInput(i);
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : String(e));
      void loadStatus();
      return;
    }
    // Most UIs react within a few hundred ms; a slow one (an app launching) is caught by the next idle refresh
    setTimeout(() => void shoot(), 350);
  };

  const onPicture = (e: React.MouseEvent<HTMLImageElement>, right = false) => {
    if (Date.now() - pinchedAt.current < 400) return;
    const r = e.currentTarget.getBoundingClientRect();
    const x = (e.clientX - r.left) / r.width;
    const y = (e.clientY - r.top) / r.height;
    if (x < 0 || x > 1 || y < 0 || y > 1) return;
    setMark({ x, y });
    const m = right ? "right" : mode;
    void send({ kind: "click", x, y, button: m === "right" ? "right" : "left", count: m === "double" ? 2 : 1 });
  };

  const typeIt = async (enter: boolean) => {
    if (text) await send({ kind: "text", text });
    if (enter) await send({ kind: "key", key: "enter" });
    setText("");
  };

  const grant = () => {
    setGranting(true);
    api
      .screenRequestAccess()
      .then((s) => {
        setSt(s);
        if (s.screen) void shoot();
      })
      .catch((e) => toast.error(e instanceof ApiError ? e.message : String(e)))
      .finally(() => setGranting(false));
  };

  const at = mark ?? { x: 0.5, y: 0.5 };
  const ready = !!st?.enabled && st.supported;

  return (
    <Dialog open={open} onOpenChange={(o) => !o && closeDialog()}>
      <DialogContent
        className="flex h-[calc(var(--vvh,100dvh)-1rem)] max-w-[calc(100%-1rem)] flex-col gap-2 overflow-hidden p-2 sm:max-w-[calc(100%-2rem)] sm:p-3"
        aria-describedby={undefined}
      >
        <div className="flex flex-wrap items-center gap-1.5 pr-8">
          <DialogTitle className="mr-1 text-[14px]">{t("remoteScreen.title")}</DialogTitle>
          {ready && (
            <>
              <div className="flex rounded-md border p-0.5">
                {MODES.map((m) => (
                  <button
                    key={m.mode}
                    type="button"
                    className={cn("rounded-sm px-2 py-0.5 text-[12px]", mode === m.mode ? "bg-accent text-foreground" : "text-muted-foreground")}
                    onClick={() => setMode(m.mode)}
                  >
                    {t(m.label)}
                  </button>
                ))}
              </div>
              <Button
                size="icon"
                variant="ghost"
                className="size-7"
                title={t("remoteScreen.scrollUp")}
                onClick={() => void send({ kind: "scroll", ...at, dy: 5 })}
              >
                <ArrowUp className="size-4" />
              </Button>
              <Button
                size="icon"
                variant="ghost"
                className="size-7"
                title={t("remoteScreen.scrollDown")}
                onClick={() => void send({ kind: "scroll", ...at, dy: -5 })}
              >
                <ArrowDown className="size-4" />
              </Button>
              <Button
                size="icon"
                variant="ghost"
                className="size-7"
                title={t("remoteScreen.zoomOut")}
                disabled={scale <= MIN_SCALE}
                onClick={() => zoomCentre(1 / 1.5)}
              >
                <Minus className="size-4" />
              </Button>
              <Button
                size="icon"
                variant="ghost"
                className="size-7"
                title={t("remoteScreen.zoomIn")}
                disabled={scale >= MAX_SCALE}
                onClick={() => zoomCentre(1.5)}
              >
                <Plus className="size-4" />
              </Button>
              <select
                value={quality}
                onChange={(e) => setQuality(e.target.value as Quality)}
                title={t("remoteScreen.quality")}
                className="h-7 rounded-md border bg-background px-1.5 text-[12px] text-muted-foreground"
              >
                {(Object.keys(QUALITY) as Quality[]).map((k) => (
                  <option key={k} value={k}>
                    {t(QUALITY[k].label)}
                  </option>
                ))}
              </select>
              <Button size="icon" variant="ghost" className="size-7" title={t("remoteScreen.refresh")} onClick={() => void shoot()}>
                <RefreshCw className={cn("size-4", loading && "animate-spin")} />
              </Button>
            </>
          )}
        </div>

        {st && <Problems st={st} granting={granting} onGrant={grant} />}

        {/* The picture: fit to the width, zoomed = wider than the box, pan by scrolling */}
        <div ref={boxRef} className="relative flex min-h-0 flex-1 touch-pan-x touch-pan-y overflow-auto overscroll-contain rounded-md border bg-muted/40">
          {img ? (
            // m-auto centres it while it fits and lets it scroll from the top-left edge once it doesn't
            <div ref={picRef} className="relative m-auto shrink-0" style={{ width: fitW ? fitW * scale : "100%" }}>
              <img
                src={img}
                alt=""
                draggable={false}
                className="block h-auto w-full cursor-crosshair select-none"
                onLoad={(e) => e.currentTarget.naturalHeight && setAspect(e.currentTarget.naturalWidth / e.currentTarget.naturalHeight)}
                onClick={(e) => onPicture(e)}
                onContextMenu={(e) => {
                  e.preventDefault();
                  onPicture(e, true);
                }}
              />
              {mark && (
                <div
                  className="pointer-events-none absolute size-4 -translate-x-1/2 -translate-y-1/2 rounded-full border-2 border-brand"
                  style={{ left: `${mark.x * 100}%`, top: `${mark.y * 100}%` }}
                />
              )}
            </div>
          ) : (
            <div className="m-auto p-4 text-center text-[12px] text-muted-foreground">{shotErr || (ready && st?.screen ? t("remoteScreen.loading") : "")}</div>
          )}
          {img && shotErr && <div className="absolute left-2 top-2 rounded-md bg-background/90 px-2 py-1 text-[12px] text-bad">{shotErr}</div>}
        </div>

        {ready && (
          <>
            <div className="flex flex-wrap gap-1">
              {KEYS.map((k) => (
                <Button
                  key={k.label}
                  size="sm"
                  variant="outline"
                  className="h-7 px-2 text-[12px]"
                  onClick={() => void send({ kind: "key", key: k.key, mods: k.mods })}
                >
                  {k.label}
                </Button>
              ))}
            </div>
            <form
              className="flex items-center gap-1.5"
              onSubmit={(e) => {
                e.preventDefault();
                void typeIt(false);
              }}
            >
              <div className="relative min-w-0 flex-1">
                <Input
                  value={text}
                  onChange={(e) => setText(e.target.value)}
                  type={showText ? "text" : "password"}
                  autoComplete="off"
                  autoCapitalize="off"
                  autoCorrect="off"
                  spellCheck={false}
                  placeholder={t("remoteScreen.textPlaceholder")}
                  className="h-8 pr-8 text-[13px]"
                />
                <button
                  type="button"
                  className="absolute right-1.5 top-1/2 -translate-y-1/2 text-muted-foreground"
                  title={showText ? t("remoteScreen.hide") : t("remoteScreen.show")}
                  onClick={() => setShowText((v) => !v)}
                >
                  {showText ? <EyeOff className="size-4" /> : <Eye className="size-4" />}
                </button>
              </div>
              <Button type="submit" size="sm" variant="outline" className="h-8" disabled={!text}>
                {t("remoteScreen.type")}
              </Button>
              <Button type="button" size="sm" className="h-8" onClick={() => void typeIt(true)}>
                {t("remoteScreen.typeEnter")}
              </Button>
            </form>
            <div className="text-[11px] text-muted-foreground max-md:hidden">{t("remoteScreen.tapHint")}</div>
          </>
        )}
      </DialogContent>
    </Dialog>
  );
}

function Problems({ st, granting, onGrant }: { st: ScreenStatus; granting: boolean; onGrant: () => void }) {
  if (!st.enabled) return <div className="text-[12px] text-warn">{t("remoteScreen.off")}</div>;
  if (!st.supported) return <div className="text-[12px] text-warn">{t("remoteScreen.unsupported")}</div>;
  if (st.screen && st.input) return null;
  return (
    <div className="space-y-1 rounded-md border border-warn/40 px-2 py-1.5 text-[12px]">
      {!st.screen && <div className="text-warn">{t("remoteScreen.needScreen")}</div>}
      {!st.input && <div className="text-warn">{t("remoteScreen.needInput")}</div>}
      <div className="flex items-center gap-2 text-muted-foreground">
        <span className="min-w-0 flex-1">{t("remoteScreen.grantHint")}</span>
        <Button size="sm" variant="outline" className="h-7 shrink-0 text-[12px]" disabled={granting} onClick={onGrant}>
          {t("remoteScreen.grant")}
        </Button>
      </div>
    </div>
  );
}
