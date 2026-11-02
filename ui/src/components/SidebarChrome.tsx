import { Command, Keyboard, PanelLeft, Settings } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip";
import { ModeSwitch } from "@/components/WorkView";
import { currentSpace, spaceList } from "@/lib/layout";
import { get, set, setSidebarW, SIDEBAR_W, toggleSidebar, useStore } from "@/lib/store";
import { cn } from "@/lib/utils";
import { PhoneGroupButtons } from "./RailGroups";
import { isAndroid } from "@/lib/android";
import { usePhone } from "@/lib/phone";
import { InboxButton } from "./Inbox";
import { ConnStatus, RouteLabel } from "./ConnStatus";
import { SpaceSwitcher } from "./SpaceSwitcher";

/*
 * The phone drawer's top and bottom rows. Wider screens have the rail instead (Rail.tsx), which carries the same things
 * (projects, Code / Work, add-ons, inbox, settings) in every mode; their list only gets ListHeader.
 */

/**
 * The bottom of the phone drawer. The add-on blocks (g.slot("panel"): quota meter, today's numbers…) used to sit
 * above it for good and took a quarter of a phone's height from the list; they open from the footer's button instead
 * (PhoneGroupButtons, one per group of Settings → 扩展 → 分组), as they open from the rail on wider screens.
 */
export function SidebarBottom({ sidebarToggle }: { sidebarToggle?: boolean }) {
  return <SidebarFooter sidebarToggle={sidebarToggle} />;
}

/**
 * The top of Code mode's list beside the rail: which project it lists, and the button that hides it (the rail's Code
 * button brings it back, as does the tab bar's). The project switcher and the mode switch are on the rail.
 */
export function ListHeader() {
  const name = useStore((s) => spaceList(s.layout).find((x) => x.id === currentSpace(s.layout))?.name);
  return (
    <div className="flex h-10 shrink-0 items-center gap-1 border-b pr-1.5 pl-3" style={{ WebkitAppRegion: "drag" } as any}>
      <span className="min-w-0 flex-1 truncate text-[12px] font-semibold">{name}</span>
      <Tooltip>
        <TooltipTrigger asChild>
          <Button variant="ghost" size="icon" className="size-7 text-muted-foreground" style={{ WebkitAppRegion: "no-drag" } as any} onClick={toggleSidebar}>
            <PanelLeft className="size-3.5" />
          </Button>
        </TooltipTrigger>
        <TooltipContent>隐藏会话列表</TooltipContent>
      </Tooltip>
    </div>
  );
}

/** The top row of the phone drawer: project switcher, then the Code / Work switch. */
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

/** The bottom row of the phone drawer: settings, connection / route status, the command palette. */
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
      <PhoneGroupButtons />
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
