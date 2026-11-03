import { useCallback, useEffect, useRef, useState } from "react";
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
const ZOOMS = [1, 1.5, 2, 3];

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
  const [zoom, setZoom] = useState(0);
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
  const zoomRef = useRef(0);
  zoomRef.current = zoom;
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
      const w = ZOOMS[zoomRef.current] >= 2 ? Math.max(ql.w, QUALITY.high.w) : ql.w;
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
                disabled={zoom === 0}
                onClick={() => setZoom((z) => Math.max(0, z - 1))}
              >
                <Minus className="size-4" />
              </Button>
              <Button
                size="icon"
                variant="ghost"
                className="size-7"
                title={t("remoteScreen.zoomIn")}
                disabled={zoom === ZOOMS.length - 1}
                onClick={() => setZoom((z) => Math.min(ZOOMS.length - 1, z + 1))}
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
        <div ref={boxRef} className="relative flex min-h-0 flex-1 overflow-auto rounded-md border bg-muted/40">
          {img ? (
            // m-auto centres it while it fits and lets it scroll from the top-left edge once it doesn't
            <div className="relative m-auto shrink-0" style={{ width: fitW ? fitW * ZOOMS[zoom] : "100%" }}>
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
