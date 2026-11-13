import { Code2, Command, Keyboard, Plus, Settings } from "lucide-react";
import { ContextMenu, ContextMenuContent, ContextMenuTrigger } from "@/components/ui/context-menu";
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip";
import { switchSpace } from "@/lib/actions";
import { isAndroid } from "@/lib/android";
import { currentSpace, spaceInitials, spaceList, type Space } from "@/lib/layout";
import { isCoarse } from "@/lib/phone";
import { set, setMode, toggleSidebar, useStore } from "@/lib/store";
import { cn } from "@/lib/utils";
import { updateWaitsForClick } from "@/lib/native";
import { StatusDot } from "./AgentIcon";
import { RailAddonButtons } from "./RailAddons";
import { RouteLabel } from "./ConnStatus";
import { InboxButton } from "./Inbox";
import { PanelButtons } from "./PanelButtons";
import { useBadges } from "@/lib/badges";
import { SpaceDropLine, startSpaceDrag } from "./SpaceDrag";
import { newSpaceDialog, SpaceMenuItems, spaceStatus } from "./SpaceSwitcher";
import { t } from "@/lib/i18n";

/*
 * The rail: gerdr's own strip down the left edge on wider screens, the same in every mode — projects, the Code / Work
 * switch, the add-on buttons (RailAddons.tsx), the inbox, settings. Everything gerdr needs at hand lives here, so nothing of gerdr's is laid
 * over the Work site: that page is a plain full-size frame its agent can lay out any way it likes (it used to share the
 * left column with gerdr's header and settings row, which every site had to leave room for and line its menu up with,
 * and which broke whenever one didn't). Code mode's list sits to the rail's right. The phone has no rail: its drawer
 * (UnifiedSidebar with its own header and bottom row) and top bar carry the same things.
 */

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
  const badges = useBadges((s) => s.byPanel);
  const mode = useStore((s) => s.mode);
  const sidebar = useStore((s) => s.sidebar);
  const doctorBad = useStore((s) => s.doctor?.level === "bad");
  const updateWaits = useStore((s) => updateWaitsForClick(s.appUpdate)); // an update closed with "Later"
  const connected = useStore((s) => s.connected);
  const spaces = spaceList(layout);
  const here = currentSpace(layout);
  const cur = mode === "work" ? "work" : "agents";
  const coarse = isCoarse();

  const project = (sp: Space) => {
    const on = sp.id === here;
    // Another project's agents are out of every list on screen: its dot is the only thing that tells you it needs you
    const st = on ? undefined : spaceStatus(layout, sp, sessions, unread, badges);
    return (
      // Tooltip outside, ContextMenuTrigger right on the button: a trigger with asChild hands its handlers to its child,
      // and a <Tooltip> (not an element) dropped them, so right-click opened nothing.
      <ContextMenu key={sp.id}>
        <Tooltip>
          <TooltipTrigger asChild>
            <ContextMenuTrigger asChild>
              <button
                data-space-id={sp.id}
                aria-label={sp.name}
                aria-current={on || undefined}
                className={cn(
                  "relative flex size-9 shrink-0 items-center justify-center rounded-lg border text-[12px] font-semibold",
                  on ? "border-foreground bg-foreground text-background" : "bg-background text-muted-foreground hover:text-foreground",
                  "data-[state=open]:ring-2 data-[state=open]:ring-brand/60",
                )}
                onClick={() => switchSpace(sp.id)}
                onPointerDown={(e) => startSpaceDrag(e, sp.id, "y")}
              >
                {on && <span className="absolute top-2 -left-1.5 h-5 w-1 rounded-r bg-foreground" />}
                <span className="max-w-full truncate px-0.5">{spaceInitials(sp.name)}</span>
                {st && <StatusDot status={st.status} unread={st.unread} className="absolute -top-0.5 -right-0.5 ring-2 ring-sidebar" />}
              </button>
            </ContextMenuTrigger>
          </TooltipTrigger>
          <TooltipContent side="right">{sp.name}</TooltipContent>
        </Tooltip>
        <ContextMenuContent>
          <SpaceMenuItems sp={sp} axis="y" />
        </ContextMenuContent>
      </ContextMenu>
    );
  };

  const modeItem = (m: typeof cur, label: string, Icon: typeof Code2) => {
    const on = cur === m;
    // The mode you're in again: Code toggles its list (as an activity bar does); Work has nothing to toggle.
    // Icons in one capsule, like the horizontal Code / Work switch this replaced, stood on end: the capsule is as wide
    // as the project chips above (36px), the current mode raised inside it. Separate labelled buttons looked out of
    // place next to the chips whatever their size; the names are in the tooltips.
    const title = on && m === "agents" ? `Code · ${sidebar ? t("nav.hideSessionList") : t("rail.showSessionList")}` : label;
    return tip(
      title,
      <button
        aria-label={label}
        aria-pressed={on}
        className={cn(
          "flex size-8 items-center justify-center rounded-md",
          on ? "bg-background text-foreground shadow-sm" : "text-muted-foreground hover:text-foreground",
        )}
        onClick={() => {
          if (on) return m === "agents" && toggleSidebar();
          // Back to Code keeps the grid as it was: App.tsx only hides it while a panel shows (asked for)
          setMode(m);
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
        <div data-space-list="rail" className="relative flex w-full flex-col items-center gap-2 pt-2.5 pb-1 short:gap-1.5 short:pt-2">
          {spaces.map(project)}
          <SpaceDropLine list="rail" axis="y" />
          {tip(
            t("rail.newProjectHint"),
            <button
              aria-label={t("nav.newProject")}
              className="flex size-9 shrink-0 items-center justify-center rounded-lg border border-dashed text-muted-foreground hover:text-foreground"
              onClick={newSpaceDialog}
            >
              <Plus className="size-4" />
            </button>,
          )}
        </div>
        <div className="my-2 h-px w-7 shrink-0 bg-border short:my-1.5" />
        <div className="flex shrink-0 flex-col items-center gap-0.5 rounded-lg bg-muted p-0.5">
          {modeItem("agents", "Code", Code2)}
          <PanelButtons
            className={(on) =>
              cn(
                "flex size-8 items-center justify-center rounded-md",
                on ? "bg-background text-foreground shadow-sm" : "text-muted-foreground hover:text-foreground",
              )
            }
          />
        </div>
        <div className="min-h-2 flex-1" />
        <div className="flex shrink-0 flex-col items-center gap-1 pb-2">
          <RailAddonButtons />
          <InboxButton rail />
          {/* A touch screen seldom has a keyboard to need a shortcut list: the command palette instead, as in the phone's drawer */}
          {coarse
            ? tip(
                t("nav.commandPalette"),
                <button aria-label={t("nav.commandPalette")} className={railBtn} onClick={() => set({ dialog: { kind: "palette" } })}>
                  <Command className="size-4" />
                </button>,
              )
            : tip(
                t("nav.shortcuts"),
                <button aria-label={t("nav.shortcuts")} className={railBtn} onClick={() => set({ dialog: { kind: "help" } })}>
                  <Keyboard className="size-4" />
                </button>,
              )}
          {tip(
            doctorBad ? t("rail.settingsDoctorBad") : updateWaits ? t("rail.settingsUpdate") : t("nav.settings"),
            <button
              aria-label={t("nav.settings")}
              className={cn(railBtn, "relative")}
              onClick={() => set({ dialog: { kind: "settings", tab: doctorBad ? "labs" : undefined } })}
            >
              <Settings className="size-4" />
              {(doctorBad || updateWaits) && <span className={cn("absolute top-1.5 right-1.5 size-1.5 rounded-full", doctorBad ? "bg-bad" : "bg-brand")} />}
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
