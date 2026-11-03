import { useEffect, useState } from "react";
import { Plus, RefreshCw, Smile, Trash2 } from "lucide-react";
import { toast } from "sonner";
import { ContextMenu, ContextMenuContent, ContextMenuItem, ContextMenuTrigger } from "@/components/ui/context-menu";
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip";
import { api } from "@/lib/api";
import { askConfirm } from "@/lib/confirm";
import { currentSpace } from "@/lib/layout";
import { usePanels, type Panel } from "@/lib/mounts";
import { set, setMode, setWorkPanel, useStore } from "@/lib/store";
import { cn } from "@/lib/utils";
import { PanelIcon } from "./PanelIcon";
import { t } from "@/lib/i18n";

/**
 * One button per panel of the project on screen, under Code: its own Work (the briefcase), then each panel installed
 * or added (its first letters, its name in the tooltip). Right-click: update it from the repository it came from,
 * remove it (to the trash). The rail stacks them (axis y); the phone drawer's header lays them in a row.
 */
export function PanelButtons({ className, size = "rail" }: { className: (on: boolean) => string; size?: "rail" | "row" }) {
  const space = useStore((s) => currentSpace(s.layout));
  const panels = usePanels(space);
  const mode = useStore((s) => s.mode);
  const chosen = useStore((s) => s.workPanels[space] ?? "");
  // A panel removed elsewhere: fall back to the project's Work
  const cur = panels.some((p) => p.name === chosen) ? chosen : "";
  return (
    <>
      {panels.map((p) => (
        <PanelButton key={p.key} p={p} on={mode === "work" && p.name === cur} className={className} size={size} space={space} />
      ))}
      {/* Add one: install it from a repository, or a blank one to build (WorkSourceDialog) */}
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
        <TooltipContent side={size === "rail" ? "right" : "bottom"}>{t("workSource.add")}</TooltipContent>
      </Tooltip>
    </>
  );
}

function PanelButton({ p, on, className, size, space }: { p: Panel; on: boolean; className: (on: boolean) => string; size: "rail" | "row"; space: string }) {
  const [src, setSrc] = useState<{ git: boolean; remote?: string }>();
  const [menu, setMenu] = useState(false);
  useEffect(() => {
    if (menu) api.workSource(p.key).then(setSrc, () => {});
  }, [menu, p.key]);
  const label = p.name ? p.title : "Work";
  const go = () => {
    setWorkPanel(space, p.name);
    setMode("work");
  };
  const update = () =>
    api.updateWork(p.key).then(
      (r) => toast.success(t("workSource.updated"), { description: r.output }),
      (e) => toast.error(e.message),
    );
  const remove = async () => {
    if (
      !(await askConfirm({
        title: t("workSource.removeTitle", { name: label }),
        description: t("workSource.removeBody"),
        action: t("workSource.remove"),
        destructive: true,
      }))
    )
      return;
    try {
      const r = await api.removePanel(p.key);
      if (on) setWorkPanel(space, "");
      toast.success(t("workSource.removed", { name: label }), { description: t("workSource.oldIn", { path: r.trash }) });
    } catch (e: any) {
      toast.error(e.message);
    }
  };
  return (
    <ContextMenu onOpenChange={setMenu}>
      <Tooltip>
        <TooltipTrigger asChild>
          <ContextMenuTrigger asChild>
            <button aria-label={label} aria-pressed={on} className={className(on)} onClick={go}>
              <PanelIcon icon={p.icon} color={p.color} title={p.title} own={!p.name} className={size === "rail" ? "size-4" : "size-3.5"} />
            </button>
          </ContextMenuTrigger>
        </TooltipTrigger>
        <TooltipContent side={size === "rail" ? "right" : "bottom"}>{label}</TooltipContent>
      </Tooltip>
      <ContextMenuContent>
        <ContextMenuItem onClick={() => set({ dialog: { kind: "panelIcon", key: p.key, title: label, icon: p.icon, color: p.color } })}>
          <Smile /> {t("panelIcon.menu")}
        </ContextMenuItem>
        {src?.git && src.remote && (
          <ContextMenuItem onClick={update}>
            <RefreshCw /> {t("workSource.menuUpdate", { remote: src.remote.replace(/^https?:\/\/(github\.com\/)?/, "") })}
          </ContextMenuItem>
        )}
        {p.name ? (
          <ContextMenuItem variant="destructive" onClick={remove}>
            <Trash2 /> {t("workSource.menuRemove")}
          </ContextMenuItem>
        ) : null}
      </ContextMenuContent>
    </ContextMenu>
  );
}
