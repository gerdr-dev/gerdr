import { Command, Keyboard, PanelLeft, Settings } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip";
import { ModeSwitch } from "@/components/WorkView";
import { get, set, setSidebarW, SIDEBAR_W, toggleSidebar, useStore } from "@/lib/store";
import { cn } from "@/lib/utils";
import { AddonSlot } from "./AddonSlot";
import { isAndroid } from "@/lib/android";
import { usePhone } from "@/lib/phone";
import { InboxButton } from "./Inbox";
import { ConnStatus, RouteLabel } from "./ConnStatus";
import { SpaceSwitcher } from "./SpaceSwitcher";

/**
 * The bottom of the left column in all three modes: add-ons' sidebar blocks (g.slot("sidebar"): the quota meter,
 * today's numbers…) over the settings row. Shared so the add-ons are in sight whichever mode is open (asked for).
 */
export function SidebarBottom({ sidebarToggle }: { sidebarToggle?: boolean }) {
  return (
    <>
      {/* The vertical padding is given once here, not by each block: two blocks each padding themselves left a wide gap.
          bg-sidebar: in Work mode this is laid over the site's menu */}
      <AddonSlot name="sidebar" className="shrink-0 border-t bg-sidebar py-1 empty:hidden" />
      <SidebarFooter sidebarToggle={sidebarToggle} />
    </>
  );
}

/**
 * The top row of the left column, the same in all three modes (asked for): the Code sidebar, the overview's list, and
 * Work mode's top bar. Project first, the Code / overview / Work switch at the right, so switching modes moves nothing.
 * Only these two: the connection status used to sit here too and squeezed the project name to "def…" (it's in the
 * bottom row now, SidebarFooter).
 */
export function SidebarHeader({ className, style }: { className?: string; style?: React.CSSProperties }) {
  // As tall as the phone's top bar (PhoneBar, h-11): the drawer slides in beside it, and the two bottom borders should line up
  const phone = usePhone();
  return (
    <div
      className={cn("flex shrink-0 items-center gap-1 border-b px-1.5", phone ? "h-11" : "h-10", className)}
      style={{ WebkitAppRegion: "drag", ...style } as any}
    >
      <div className="native-traffic-light-gap" />
      {/* The session switcher takes the title's place (asked for). Only the button is no-drag, so the rest of the bar still moves the window */}
      <div className="flex min-w-16 flex-1">
        <SpaceSwitcher />
      </div>
      <ModeSwitch />
    </div>
  );
}

/**
 * The bottom row of the left column, the same in all three modes (asked for: settings always at the bottom): the Code
 * sidebar, the overview's list, and over the bottom of the Work site's menu (WorkView). `sidebarToggle`: the Code
 * sidebar and the overview's list (one setting) can be hidden; Work mode's menu can't.
 */
export function SidebarFooter({ sidebarToggle }: { sidebarToggle?: boolean }) {
  const connected = useStore((s) => s.connected);
  const doctorBad = useStore((s) => s.doctor?.level === "bad");
  const phone = usePhone();
  return (
    <div className="flex h-10 shrink-0 items-center gap-1 border-t bg-sidebar px-1.5">
      <Button
        variant="ghost"
        size="sm"
        className="h-7 shrink-0 justify-start gap-2 text-[12px] text-muted-foreground"
        onClick={() => set({ dialog: { kind: "settings", tab: doctorBad ? "labs" : undefined } })}
      >
        <Settings className="size-3.5" /> 设置
        {/* Labs → Diagnosis found something broken (agents missing PATH dirs, a blocked codex…) */}
        {doctorBad && (
          <span className="ml-auto flex items-center gap-1 text-[11px] text-bad" title="诊断发现了问题：设置 → 实验室 → 诊断">
            <span className="size-1.5 rounded-full bg-bad" /> 有问题
          </span>
        )}
      </Button>
      {/* Status lives down here, where there's room: the connection to the backend when it's lost, and on Android which route
          the app takes (details in Settings → General → Android app) */}
      <div className="flex min-w-0 flex-1 items-center justify-end gap-2 overflow-hidden">
        <ConnStatus />
        {connected && isAndroid() && <RouteLabel />}
      </div>
      {/* On a phone the inbox is in the top bar (PhoneBar); this row is in the drawer there */}
      {!phone && <InboxButton />}
      {/* A phone has no ⌘K and no keyboard to need a shortcut list: the slot opens the palette instead, the only way
          to reach add-on commands there. The drawer closes so the palette isn't under it on the way back. */}
      {phone ? (
        <Button
          variant="ghost"
          size="icon"
          className="size-7 text-muted-foreground"
          aria-label="命令面板"
          onClick={() => set({ drawer: false, dialog: { kind: "palette" } })}
        >
          <Command className="size-3.5" />
        </Button>
      ) : (
        <Tooltip>
          <TooltipTrigger asChild>
            <Button variant="ghost" size="icon" className="size-7 text-muted-foreground" onClick={() => set({ dialog: { kind: "help" } })}>
              <Keyboard className="size-3.5" />
            </Button>
          </TooltipTrigger>
          <TooltipContent>快捷键</TooltipContent>
        </Tooltip>
      )}
      {sidebarToggle && (
        <Tooltip>
          <TooltipTrigger asChild>
            <Button variant="ghost" size="icon" className="size-7 text-muted-foreground" onClick={toggleSidebar}>
              <PanelLeft className="size-3.5" />
            </Button>
          </TooltipTrigger>
          <TooltipContent>隐藏侧栏</TooltipContent>
        </Tooltip>
      )}
    </div>
  );
}

/**
 * A column resize handle's look. touch-none: without it a touch drag that moves fast is taken over by the browser as a
 * pan (pointercancel), so on a phone or tablet only a slow drag got anywhere. The callers make it wider for fingers.
 */
export const COL_EDGE = "z-20 cursor-col-resize touch-none bg-clip-content transition-colors hover:bg-brand/50";

/**
 * Drag on a column resize handle: onDx gets the distance from where it started, at most once a frame (each call
 * re-renders the sidebar or the chat column; per pointer event that's several times a frame on a phone). Captured, so a
 * frame next to the handle (Work mode's site) can't swallow the moves.
 */
export function startColResize(e: React.PointerEvent, onDx: (dx: number) => void, axis: "x" | "y" = "x") {
  e.preventDefault();
  const el = e.currentTarget as HTMLElement;
  el.setPointerCapture(e.pointerId);
  const pos = (ev: { clientX: number; clientY: number }) => (axis === "x" ? ev.clientX : ev.clientY);
  const x0 = pos(e);
  let x = x0;
  let raf = 0;
  const move = (ev: PointerEvent) => {
    x = pos(ev);
    raf ||= requestAnimationFrame(() => {
      raf = 0;
      onDx(x - x0);
    });
  };
  const end = () => {
    if (raf) (cancelAnimationFrame(raf), onDx(x - x0));
    raf = 0;
    for (const t of ["pointerup", "pointercancel", "lostpointercapture"]) el.removeEventListener(t, end);
    el.removeEventListener("pointermove", move);
    document.body.style.cursor = "";
  };
  document.body.style.cursor = axis === "x" ? "col-resize" : "row-resize";
  el.addEventListener("pointermove", move);
  for (const t of ["pointerup", "pointercancel", "lostpointercapture"]) el.addEventListener(t, end);
}

/**
 * The sidebar's right edge: drag to resize it (asked for), double-click for the default width. Also on the overview's
 * list, which shares the width so switching between them doesn't jump.
 */
export function SidebarEdge() {
  const start = (e: React.PointerEvent) => {
    const w0 = get().sidebarW;
    startColResize(e, (dx) => setSidebarW(w0 + dx));
  };
  return (
    <div
      className={cn("absolute top-0 -right-1 bottom-0 w-2 px-[3px] pointer-coarse:-right-3 pointer-coarse:w-6 pointer-coarse:px-[11px]", COL_EDGE)}
      onPointerDown={start}
      onDoubleClick={() => setSidebarW(SIDEBAR_W)}
      title="拖动调宽窄，双击还原"
      style={{ WebkitAppRegion: "no-drag" } as any}
    />
  );
}
