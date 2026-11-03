import { useEffect, useState } from "react";
import { Briefcase } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { api } from "@/lib/api";
import { t } from "@/lib/i18n";
import { set, useStore } from "@/lib/store";
import { cn } from "@/lib/utils";
import { IconPicker, StoredIcon } from "./IconPicker";

function short(name: string) {
  const chars = [...name];
  return /^[\x00-\x7f]/.test(name) ? chars.slice(0, 2).join("").toUpperCase() : chars[0];
}

/** Its default face: the briefcase for a project's own Work, the first letters of its name for a panel. */
function PanelDefault({ title, own, className }: { title: string; own: boolean; className?: string }) {
  if (own) return <Briefcase className={cn("size-4", className)} />;
  return <span className="text-[11px] font-semibold">{short(title)}</span>;
}

/**
 * A panel's face on the rail: the icon in its ui.json, the same kinds an agent's can be (a glyph, a line icon, a
 * sprite from an add-on, IconPicker), with its colour; else its default.
 */
export function PanelIcon({ icon, color, title, own, className }: { icon?: string; color?: string; title: string; own: boolean; className?: string }) {
  return <StoredIcon icon={icon} color={color} className={cn("size-4", className)} fallback={<PanelDefault title={title} own={own} className={className} />} />;
}

/** Right-click → change icon: the agent icon picker; saved into the panel's ui.json (it travels with the panel). */
export function PanelIconDialog() {
  const d = useStore((s) => s.dialog);
  const ask = d?.kind === "panelIcon" ? d : undefined;
  const [icon, setIcon] = useState("");
  const [color, setColor] = useState("");
  const [err, setErr] = useState("");
  useEffect(() => {
    setIcon(ask?.icon ?? "");
    setColor(ask?.color ?? "");
    setErr("");
  }, [ask?.key]);
  const save = async () => {
    if (!ask) return;
    try {
      await api.setPanelIcon(ask.key, icon, color);
      set({ dialog: null });
    } catch (e: any) {
      setErr(e.message ?? String(e));
    }
  };
  const own = !ask?.key.includes("/");
  return (
    <Dialog open={!!ask} onOpenChange={(o) => !o && set({ dialog: null })}>
      <DialogContent className="sm:max-w-md">
        <DialogHeader>
          <DialogTitle>{t("panelIcon.title", { name: ask?.title ?? "" })}</DialogTitle>
          <DialogDescription>{t("panelIcon.desc")}</DialogDescription>
        </DialogHeader>
        <IconPicker
          icon={icon}
          color={color}
          onChange={(p) => {
            if (p.icon !== undefined) setIcon(p.icon);
            if (p.color !== undefined) setColor(p.color);
          }}
          colorable={!icon.startsWith("sprite:")}
          def={{ title: t("panelIcon.default"), node: <PanelDefault title={ask?.title ?? ""} own={own} className="size-5" />, glyph: "tiles" }}
        />
        {err && <div className="text-[12px] text-bad">{err}</div>}
        <DialogFooter>
          <Button variant="outline" onClick={() => set({ dialog: null })}>
            {t("common.cancel")}
          </Button>
          <Button onClick={() => void save()}>{t("common.save")}</Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
