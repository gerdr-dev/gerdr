import { Briefcase, Code2, Command, Gauge, Keyboard, Plus, Settings } from "lucide-react";
import { useEffect, useLayoutEffect, useRef, useState } from "react";
import { createPortal } from "react-dom";
import { ContextMenu, ContextMenuContent, ContextMenuItem, ContextMenuSeparator, ContextMenuTrigger } from "@/components/ui/context-menu";
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip";
import { switchSpace } from "@/lib/actions";
import { slots } from "@/lib/addons";
import { isAndroid } from "@/lib/android";
import { currentSpace, spaceList, type Space } from "@/lib/layout";
import { isNative, nativeNewWindow } from "@/lib/native";
import { isCoarse } from "@/lib/phone";
import { toggleOverview } from "@/lib/overview";
import { set, setMode, toggleSidebar, useStore } from "@/lib/store";
import { cn } from "@/lib/utils";
import { StatusDot } from "./AgentIcon";
import { AddonSlot } from "./AddonSlot";
import { RouteLabel } from "./ConnStatus";
import { InboxButton } from "./Inbox";
import { askCloseSpace, newSpaceDialog, spaceStatus } from "./SpaceSwitcher";

/*
 * The rail: gerdr's own strip down the left edge on wider screens, the same in every mode — projects, the Code / Work
 * switch, the add-on panel, the inbox, settings. Everything gerdr needs at hand lives here, so nothing of gerdr's is laid
 * over the Work site: that page is a plain full-size frame its agent can lay out any way it likes (it used to share the
 * left column with gerdr's header and settings row, which every site had to leave room for and line its menu up with,
 * and which broke whenever one didn't). Code mode's list sits to the rail's right. The phone has no rail: its drawer
 * (UnifiedSidebar with its own header and bottom row) and top bar carry the same things.
 */

/** A project's short label for its chip: the initials of its first two words, a short name as is, else its first letter. */
function initials(name: string) {
  const words = name.split(/[\s\-_./]+/).filter(Boolean);
  if (words.length >= 2) return (words[0][0] + words[1][0]).toUpperCase();
  if ([...name].length <= 2) return name;
  return [...name][0].toUpperCase();
}

const tip = (label: React.ReactNode, el: React.ReactElement) => (
  <Tooltip>
    <TooltipTrigger asChild>{el}</TooltipTrigger>
    <TooltipContent side="right">{label}</TooltipContent>
  </Tooltip>
);

export function Rail() {
  const layout = useStore((s) => s.layout);
  const sessions = useStore((s) => s.sessions);
  const unread = useStore((s) => s.unread);
  const mode = useStore((s) => s.mode);
  const sidebar = useStore((s) => s.sidebar);
  const doctorBad = useStore((s) => s.doctor?.level === "bad");
  const connected = useStore((s) => s.connected);
  const spaces = spaceList(layout);
  const here = currentSpace(layout);
  const cur = mode === "work" ? "work" : "agents";
  const coarse = isCoarse();

  const project = (sp: Space) => {
    const on = sp.id === here;
    // Another project's agents are out of every list on screen: its dot is the only thing that tells you it needs you
    const st = on ? undefined : spaceStatus(layout, sp, sessions, unread);
    return (
      <ContextMenu key={sp.id}>
        <ContextMenuTrigger asChild>
          {tip(
            sp.name,
            <button
              aria-label={sp.name}
              aria-current={on || undefined}
              className={cn(
                "relative flex size-9 shrink-0 items-center justify-center rounded-lg border text-[12px] font-semibold",
                on ? "border-foreground bg-foreground text-background" : "bg-background text-muted-foreground hover:text-foreground",
                "data-[state=open]:ring-2 data-[state=open]:ring-brand/60",
              )}
              onClick={() => switchSpace(sp.id)}
            >
              {on && <span className="absolute top-2 -left-1.5 h-5 w-1 rounded-r bg-foreground" />}
              <span className="max-w-full truncate px-0.5">{initials(sp.name)}</span>
              {st && <StatusDot status={st.status} unread={st.unread} className="absolute -top-0.5 -right-0.5 ring-2 ring-sidebar" />}
            </button>,
          )}
        </ContextMenuTrigger>
        <ContextMenuContent>
          {isNative() && <ContextMenuItem onClick={() => nativeNewWindow(sp.id)}>在新窗口打开</ContextMenuItem>}
          {isNative() && spaces.length > 1 && <ContextMenuSeparator />}
          {spaces.length > 1 && (
            <ContextMenuItem variant="destructive" onClick={() => askCloseSpace(sp)}>
              关闭项目
            </ContextMenuItem>
          )}
        </ContextMenuContent>
      </ContextMenu>
    );
  };

  const modeItem = (m: typeof cur, label: string, Icon: typeof Code2) => {
    const on = cur === m;
    // The mode you're in again: Code toggles its list (as an activity bar does); Work has nothing to toggle.
    // Icon only, the size of the rail's other buttons: icon + label made them a size bigger than everything around
    const title = on && m === "agents" ? `Code · ${sidebar ? "隐藏会话列表" : "显示会话列表"}` : label;
    return tip(
      title,
      <button
        aria-label={label}
        aria-pressed={on}
        className={cn(
          "flex size-9 items-center justify-center rounded-md",
          on ? "bg-background text-foreground shadow-sm" : "text-muted-foreground hover:bg-accent hover:text-foreground",
        )}
        onClick={() => {
          if (on) return m === "agents" && toggleSidebar();
          setMode(m);
          if (m !== "work") toggleOverview(false);
        }}
      >
        <Icon className="size-4" />
      </button>,
    );
  };

  return (
    // The whole rail scrolls when the window is too short for it (a phone on its side is ~400px tall, ~150px with the
    // soft keyboard up). Only the projects scrolling squeezed them to nothing there, under the fixed rest. min-h-full
    // inside keeps the spacer pushing the bottom group down when there is room.
    <div className="h-full w-12 shrink-0 overflow-y-auto border-r bg-sidebar [scrollbar-width:none] [&::-webkit-scrollbar]:hidden">
      <div className="flex min-h-full flex-col items-center">
        <div className="flex w-full flex-col items-center gap-2 pt-2.5 pb-1 short:gap-1.5 short:pt-2">
          {spaces.map(project)}
          {tip(
            "新项目（每个项目有自己的工作空间、Agent 和 Work，互相看不见）",
            <button
              aria-label="新项目"
              className="flex size-9 shrink-0 items-center justify-center rounded-lg border border-dashed text-muted-foreground hover:text-foreground"
              onClick={newSpaceDialog}
            >
              <Plus className="size-4" />
            </button>,
          )}
        </div>
        <div className="my-2 h-px w-7 shrink-0 bg-border short:my-1.5" />
        <div className="flex shrink-0 flex-col items-center gap-1">
          {modeItem("agents", "Code", Code2)}
          {modeItem("work", "Work", Briefcase)}
        </div>
        <div className="min-h-2 flex-1" />
        <div className="flex shrink-0 flex-col items-center gap-1 pb-2">
          <AddonPanel />
          <InboxButton className="size-9" />
          {/* A touch screen seldom has a keyboard to need a shortcut list: the command palette instead, as in the phone's drawer */}
          {coarse
            ? tip(
                "命令面板",
                <button aria-label="命令面板" className={railBtn} onClick={() => set({ dialog: { kind: "palette" } })}>
                  <Command className="size-4" />
                </button>,
              )
            : tip(
                "快捷键",
                <button aria-label="快捷键" className={railBtn} onClick={() => set({ dialog: { kind: "help" } })}>
                  <Keyboard className="size-4" />
                </button>,
              )}
          {tip(
            doctorBad ? "设置（诊断发现了问题：设置 → 实验室 → 诊断）" : "设置",
            <button
              aria-label="设置"
              className={cn(railBtn, "relative")}
              onClick={() => set({ dialog: { kind: "settings", tab: doctorBad ? "labs" : undefined } })}
            >
              <Settings className="size-4" />
              {doctorBad && <span className="absolute top-1.5 right-1.5 size-1.5 rounded-full bg-bad" />}
            </button>,
          )}
          {/* Which way the Android app reaches gerdr (details in Settings → General → Android app) */}
          {connected && isAndroid() && (
            <div className="pt-1 [&>span]:flex-col [&>span]:gap-0.5 [&>span]:text-[10px]">
              <RouteLabel />
            </div>
          )}
        </div>
      </div>
    </div>
  );
}

const railBtn = "flex size-9 items-center justify-center rounded-md text-muted-foreground hover:bg-accent hover:text-foreground";

/**
 * The add-on panel: the "sidebar" slot's blocks (quota meters, today's numbers…), which used to sit at the bottom of the
 * left column and don't fit a 48px rail. On the rail: the add-ons' own glance ("rail" slot, e.g. a few tiny bars) or a
 * plain icon; hovering it (mouse) or tapping it opens the full blocks beside it. The panel stays mounted while closed,
 * as the blocks did in the sidebar, so they keep their state and timers instead of reloading on every open.
 */
function AddonPanel() {
  useStore((s) => s.addonVersion);
  const [open, setOpen] = useState(false);
  const btn = useRef<HTMLButtonElement>(null);
  const panel = useRef<HTMLDivElement>(null);
  const timer = useRef(0);
  const [pos, setPos] = useState({ left: 0, bottom: 0 });
  // Mouse only: a tap on a touch screen sends pointerenter too, which would open it and the click would close it again
  const hover = (v: boolean) => (e: React.PointerEvent) => {
    if (e.pointerType !== "mouse") return;
    clearTimeout(timer.current);
    timer.current = window.setTimeout(() => setOpen(v), v ? 250 : 200);
  };
  useLayoutEffect(() => {
    if (!open || !btn.current) return;
    const r = btn.current.getBoundingClientRect();
    setPos({ left: r.right + 8, bottom: Math.max(8, window.innerHeight - r.bottom) });
  }, [open]);
  useEffect(() => {
    if (!open) return;
    const down = (e: PointerEvent) => {
      const t = e.target as Node;
      if (!panel.current?.contains(t) && !btn.current?.contains(t)) setOpen(false);
    };
    const key = (e: KeyboardEvent) => e.key === "Escape" && setOpen(false);
    document.addEventListener("pointerdown", down, true);
    document.addEventListener("keydown", key);
    return () => (document.removeEventListener("pointerdown", down, true), document.removeEventListener("keydown", key));
  }, [open]);
  useEffect(() => () => clearTimeout(timer.current), []);
  const glance = slots.rail.length > 0;
  if (!slots.sidebar.length && !glance) return null;
  return (
    <>
      <button
        ref={btn}
        aria-label="插件"
        aria-expanded={open}
        title={glance ? undefined : "插件"}
        className={cn(
          "flex min-h-9 w-10 items-center justify-center rounded-md text-muted-foreground hover:bg-accent hover:text-foreground",
          open && "bg-accent text-foreground",
        )}
        onPointerEnter={hover(true)}
        onPointerLeave={hover(false)}
        onClick={() => (clearTimeout(timer.current), setOpen(!open))}
      >
        {glance ? <AddonSlot name="rail" className="flex w-10 flex-col items-center gap-1 py-1.5" /> : <Gauge className="size-4" />}
      </button>
      {slots.sidebar.length > 0 &&
        createPortal(
          <div
            ref={panel}
            className={cn("fixed z-50 w-72 rounded-lg border bg-popover py-1 text-popover-foreground shadow-md", !open && "hidden")}
            style={pos}
            onPointerEnter={hover(true)}
            onPointerLeave={hover(false)}
          >
            <AddonSlot name="sidebar" />
          </div>,
          document.body,
        )}
    </>
  );
}
