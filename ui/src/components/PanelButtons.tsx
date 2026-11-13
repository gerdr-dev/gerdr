import { useEffect } from "react";
import { Pencil, Plus, RefreshCw, SlidersHorizontal, Smile, Trash2 } from "lucide-react";
import { toast } from "sonner";
import { ContextMenu, ContextMenuContent, ContextMenuItem, ContextMenuSeparator, ContextMenuTrigger } from "@/components/ui/context-menu";
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip";
import { api } from "@/lib/api";
import { currentSpace } from "@/lib/layout";
import { usePanels, type Panel } from "@/lib/panels";
import { set, setMode, setWorkPanel, useStore } from "@/lib/store";
import { cn } from "@/lib/utils";
import { PanelIcon } from "./PanelIcon";
import { t } from "@/lib/i18n";
import { removePanel } from "@/lib/panelActions";
import { useBadges } from "@/lib/badges";

/**
 * One button per panel of the project on screen, under Code: its own Work (the briefcase), then each panel installed
 * or added (its first letters, its name in the tooltip). Right-click: update it from the repository it came from,
 * remove it (to the trash). The rail stacks them (axis y); the phone drawer's header lays them in a row.
 */
export function PanelButtons({ className, size = "rail" }: { className: (on: boolean) => string; size?: "rail" | "row" }) {
  const space = useStore((s) => currentSpace(s.layout));
  const { panels, loaded } = usePanels(space);
  const mode = useStore((s) => s.mode);
  const chosen = useStore((s) => s.workPanels[space] ?? "");
  // The chosen one is gone (removed here or elsewhere, the project's own Work too): the first there is
  const cur = !loaded || panels.some((p) => p.name === chosen) ? chosen : (panels[0]?.name ?? "");
  useEffect(() => {
    if (cur !== chosen) setWorkPanel(space, cur);
  }, [cur, chosen, space]);
  return (
    <>
      {panels.map((p) => (
        <PanelButton key={p.key} p={p} on={mode === "work" && p.name === cur} className={className} size={size} space={space} />
      ))}
      {/* Add one: install it from a repository, or a blank one to build (WorkSourceDialog). Not on a phone's row: it
          crowds the switch there, and adding a panel is a rare thing done from Settings → Panels */}
      {size === "rail" && (
        <Tooltip>
          <TooltipTrigger asChild>
            <button
              aria-label={t("workSource.add")}
              className={cn(className(false), "opacity-70 hover:opacity-100")}
              onClick={() => set({ dialog: { kind: "workSource", project: space, action: "install" } })}
            >
              <Plus className={size === "rail" ? "size-3.5" : "size-3"} />
            </button>
          </TooltipTrigger>
          <TooltipContent side="right">{t("workSource.add")}</TooltipContent>
        </Tooltip>
      )}
    </>
  );
}

function PanelButton({ p, on, className, size, space }: { p: Panel; on: boolean; className: (on: boolean) => string; size: "rail" | "row"; space: string }) {
  const src = p.source;
  const label = p.title;
  const go = () => {
    setWorkPanel(space, p.name);
    setMode("work");
  };
  const update = () =>
    api.updateWork(p.key).then(
      (r) => toast.success(t("workSource.updated"), { description: r.output }),
      (e) => toast.error(e.message),
    );
  const remove = () => removePanel({ key: p.key, title: label });
  const badge = useBadges((s) => s.byPanel[p.key]);
  return (
    <ContextMenu>
      <Tooltip>
        <TooltipTrigger asChild>
          <ContextMenuTrigger asChild>
            <button aria-label={label} aria-pressed={on} className={cn(className(on), "relative")} onClick={go}>
              <PanelIcon icon={p.icon} color={p.color} title={p.title} className={size === "rail" ? "size-4" : "size-3.5"} />
              <PanelBadge panel={p.key} />
            </button>
          </ContextMenuTrigger>
        </TooltipTrigger>
        <TooltipContent side={size === "rail" ? "right" : "bottom"}>{badge ? t("panelBadge.tooltipN", { name: label, n: badge }) : label}</TooltipContent>
      </Tooltip>
      {/* Never wider than the screen, kept off its edges: on a phone the long-pressed button sits near the right edge,
          and a menu wider than the room left of it ran off the screen */}
      <ContextMenuContent collisionPadding={8} className="max-w-[calc(100vw-16px)]">
        <ContextMenuItem onClick={() => set({ dialog: { kind: "rename", target: "panel", id: p.key, value: p.title } })}>
          <Pencil /> {t("panelSettings.rename")}
        </ContextMenuItem>
        <ContextMenuItem onClick={() => set({ dialog: { kind: "panelIcon", key: p.key, title: label, icon: p.icon, color: p.color } })}>
          <Smile /> {t("panelIcon.menu")}
        </ContextMenuItem>
        {src?.git && src.remote && (
          <ContextMenuItem onClick={update} title={src.remote}>
            <RefreshCw /> {t("workSource.menuUpdate")}
          </ContextMenuItem>
        )}
        <ContextMenuItem variant="destructive" onClick={remove}>
          <Trash2 /> {t("workSource.menuRemove")}
        </ContextMenuItem>
        <ContextMenuSeparator />
        <ContextMenuItem onClick={() => set({ dialog: { kind: "settings", tab: "panels" } })}>
          <SlidersHorizontal /> {t("workSource.menuSettings")}
        </ContextMenuItem>
      </ContextMenuContent>
    </ContextMenu>
  );
}

/**
 * The panel's badge (ctx.badge, `gerdr panel badge`; lib/badges.ts): a dot like the inbox's, or its number in a small
 * pill over the icon's corner. Goes once the user looks at the panel (WorkView useClearBadge).
 */
function PanelBadge({ panel }: { panel: string }) {
  const n = useBadges((s) => s.byPanel[panel]);
  if (n === undefined) return null;
  if (n <= 0) return <span className="pointer-events-none absolute top-1 right-1 size-1.5 rounded-full bg-bad" />;
  return (
    <span className="pointer-events-none absolute -top-0.5 -right-0.5 flex h-3.5 min-w-3.5 items-center justify-center rounded-full bg-bad px-1 text-[9px] leading-none font-semibold text-white tabular-nums">
      {n > 99 ? "99+" : n}
    </span>
  );
}
