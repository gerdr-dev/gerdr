import { usePanelUpdates } from "@/lib/panels";
import { Code2, Command, Plus, Settings } from "lucide-react";
import { ContextMenu, ContextMenuContent, ContextMenuTrigger } from "@/components/ui/context-menu";
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip";
import { switchSpace } from "@/lib/actions";
import { useLanRoute } from "@/lib/android";
import { hasProjectIcon, ProjectIcon } from "./PanelIcon";
import { iconTextColor } from "./IconPicker";
import { solidIconColor } from "./AgentGlyph";
import { TEXT_ICON } from "@/lib/iconImage";
import { currentSpace, spaceInitials, spaceList, type Space } from "@/lib/layout";
import { set, setMode, toggleSidebar, useStore } from "@/lib/store";
import { cn } from "@/lib/utils";
import { nativeTitleBar, updateWaitsForClick } from "@/lib/native";
import { StatusDot } from "./AgentIcon";
import { RailAddonButtons } from "./RailAddons";
import { RouteLabel } from "./ConnStatus";
import { InboxButton } from "./Inbox";
import { PanelButtons, panelList } from "./PanelButtons";
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

/**
 * The project chips. Their own component because only they need the whole layout (another project's status dot): the
 * layout changes on every tab switch, and the rest of the rail (panel buttons, each with its menu and tooltip) re-rendered
 * with it (tools/scrolltest/tabrenders.mjs).
 */
function RailProjects() {
  const layout = useStore((s) => s.layout);
  const sessions = useStore((s) => s.sessions);
  const unread = useStore((s) => s.unread);
  const badges = useBadges((s) => s.byPanel);
  const spaces = spaceList(layout);
  const here = currentSpace(layout);

  const project = (sp: Space) => {
    const on = sp.id === here;
    const custom = hasProjectIcon(sp);
    const initials = spaceInitials(sp.name);
    const text = sp.icon?.startsWith(TEXT_ICON) ? sp.icon.slice(TEXT_ICON.length) || initials : !sp.icon && custom ? initials : "";
    const fill = solidIconColor(sp.color);
    const wideChars = [...text].filter((ch) => ch.codePointAt(0)! >= 0x2e80).length;
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
                  custom
                    ? on
                      ? "border-foreground bg-background"
                      : "border-transparent text-muted-foreground hover:text-foreground"
                    : on
                      ? "border-foreground bg-foreground text-background"
                      : "bg-background text-muted-foreground hover:text-foreground",
                  "data-[state=open]:ring-2 data-[state=open]:ring-brand/60",
                )}
                onClick={() => switchSpace(sp.id)}
                onPointerDown={(e) => startSpaceDrag(e, sp.id, "y")}
              >
                {on && <span className="absolute top-2 -left-1.5 h-5 w-1 rounded-r bg-foreground" />}
                {text ? (
                  <span
                    className="flex size-7 items-center justify-center rounded-md"
                    style={on ? { backgroundColor: fill ?? "var(--foreground)", color: iconTextColor(fill) } : undefined}
                  >
                    <span className={cn("max-w-full truncate", wideChars === 3 ? "text-[9px]" : wideChars === 2 && text.length >= 3 && "text-[10px]")}>
                      {text}
                    </span>
                  </span>
                ) : custom ? (
                  <ProjectIcon name={sp.name} icon={sp.icon} color={on ? sp.color : undefined} className="size-7" />
                ) : (
                  <span className="max-w-full truncate px-0.5">{initials}</span>
                )}
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

  return <>{spaces.map(project)}</>;
}

/**
 * The desktop App's rounded corner where the rail's right line meets the bottom line of the header beside it (asked
 * for). Drawn over both from the layout (App.tsx): the rail clips what overflows it, and the header beside it changes
 * (the list's, the tab bar, a panel's strip). Outside the arc it paints the headers' colour over the square corner,
 * inside it is see-through, so it suits whatever is below. Every header there is bg-sidebar.
 */
export function RailCorner() {
  if (!nativeTitleBar()) return null;
  const r = 8;
  return (
    <div
      aria-hidden
      className="pointer-events-none absolute z-20"
      style={{
        left: 47, // the rail's right line (w-12, its last pixel)
        top: (nativeTitleBar()?.height ?? 40) - 1, // the headers' border-b, their last pixel
        width: r,
        height: r,
        background: `radial-gradient(circle at 100% 100%, transparent ${r - 1.5}px, var(--border) ${r - 1}px, var(--border) ${r - 0.5}px, var(--sidebar) ${r}px)`,
      }}
    />
  );
}

export function Rail() {
  const titleBar = nativeTitleBar();
  const mode = useStore((s) => s.mode);
  const sidebar = useStore((s) => s.sidebar);
  const doctorBad = useStore((s) => s.doctor?.level === "bad");
  const updateWaits = useStore((s) => updateWaitsForClick(s.appUpdate)); // an update closed with "Later"
  const panelUpdates = useStore((s) => s.panelUpdates);
  usePanelUpdates();
  const connected = useStore((s) => s.connected);
  const lanRoute = useLanRoute();
  const cur = mode === "work" ? "work" : "agents";

  // The mode you're in again: Code toggles its list (as an activity bar does). An icon in the capsule the panels share,
  // as wide as the project chips above (36px), the current one raised; the name is in the tooltip. Dragged and ordered
  // like a panel (PanelButtons), which hands it the drag props.
  const codeOn = cur === "agents";
  const code = {
    tip: codeOn ? `Code · ${sidebar ? t("nav.hideSessionList") : t("rail.showSessionList")}` : "Code",
    button: (drag: object) => (
      <button
        {...drag}
        aria-label="Code"
        aria-pressed={codeOn}
        className={cn(
          "flex size-8 items-center justify-center rounded-md",
          codeOn ? "bg-background text-foreground shadow-sm" : "text-muted-foreground hover:text-foreground",
        )}
        // Back to Code keeps the grid as it was: App.tsx only hides it while a panel shows (asked for)
        onClick={() => (codeOn ? toggleSidebar() : setMode("agents"))}
      >
        <Code2 className="size-4" />
      </button>
    ),
  };

  return (
    // The whole rail scrolls when the window is too short for it (a phone on its side is ~400px tall, ~150px with the
    // soft keyboard up). Only the projects scrolling squeezed them to nothing there, under the fixed rest. min-h-full
    // inside keeps the spacer pushing the bottom group down when there is room.
    <div
      className={cn("h-full w-12 shrink-0 overflow-y-auto bg-sidebar [scrollbar-width:none] [&::-webkit-scrollbar]:hidden", !titleBar && "border-r")}
      // The desktop App: the right edge's line starts below the traffic lights' row (it ran through the middle light),
      // drawn as a background so it stays put while the rail scrolls
      style={
        titleBar
          ? {
              backgroundImage: "linear-gradient(var(--border), var(--border))",
              backgroundSize: `1px calc(100% - ${titleBar.height}px)`,
              backgroundPosition: "right bottom",
              backgroundRepeat: "no-repeat",
            }
          : undefined
      }
    >
      <div className="flex min-h-full flex-col items-center">
        {/* The desktop App's traffic lights start here (lib/native.ts nativeTitleBar): their row, which drags the window */}
        {titleBar && <div className="w-full shrink-0" style={{ height: titleBar.height, WebkitAppRegion: "drag" } as React.CSSProperties} />}
        {/* Under the App's title bar the first chip's top is level with the line beside it, the header's bottom border (asked for) */}
        <div
          data-space-list="rail"
          className={cn("relative flex w-full flex-col items-center gap-2 pb-1 short:gap-1.5", titleBar ? "pt-0" : "pt-2.5 short:pt-2")}
        >
          <RailProjects />
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
        <div data-space-list={panelList("rail")} className="relative flex shrink-0 flex-col items-center gap-0.5 rounded-lg bg-muted p-0.5">
          <PanelButtons className={panelBtn} code={code} />
        </div>
        <div className="min-h-2 flex-1" />
        <div className="flex shrink-0 flex-col items-center gap-1 pb-2">
          <RailAddonButtons />
          <InboxButton rail />
          {/* The command palette (⌘K), not a shortcut list: the list is one of its commands, and the palette also reaches
              add-on commands and works on a touch screen with no keyboard */}
          {tip(
            t("nav.commandPalette"),
            <button aria-label={t("nav.commandPalette")} className={railBtn} onClick={() => set({ dialog: { kind: "palette" } })}>
              <Command className="size-4" />
            </button>,
          )}
          {tip(
            doctorBad
              ? t("rail.settingsDoctorBad")
              : updateWaits
                ? t("rail.settingsUpdate")
                : panelUpdates
                  ? t("rail.settingsPanelUpdates", { n: panelUpdates })
                  : t("nav.settings"),
            <button
              aria-label={t("nav.settings")}
              className={cn(railBtn, "relative")}
              onClick={() => set({ dialog: { kind: "settings", tab: doctorBad ? "labs" : !updateWaits && panelUpdates ? "panels" : undefined } })}
            >
              <Settings className="size-4" />
              {(doctorBad || updateWaits || panelUpdates > 0) && (
                <span className={cn("absolute top-1.5 right-1.5 size-1.5 rounded-full", doctorBad ? "bg-bad" : "bg-brand")} />
              )}
            </button>,
          )}
          {/* Which way the phone App reaches gerdr (details in Settings → General → this phone) */}
          {connected && lanRoute && (
            <div className="pt-1 [&>span]:flex-col [&>span]:gap-0.5 [&>span]:text-[10px]">
              <RouteLabel />
            </div>
          )}
        </div>
      </div>
    </div>
  );
}

const panelBtn = (on: boolean) =>
  cn("flex size-8 items-center justify-center rounded-md", on ? "bg-background text-foreground shadow-sm" : "text-muted-foreground hover:text-foreground");
const railBtn = "flex size-9 items-center justify-center rounded-md text-muted-foreground hover:bg-accent hover:text-foreground";
