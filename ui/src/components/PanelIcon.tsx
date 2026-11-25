import { useEffect, useState } from "react";
import { PanelsTopLeft } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { api } from "@/lib/api";
import { t } from "@/lib/i18n";
import { panelsChanged } from "@/lib/panels";
import { set, useStore } from "@/lib/store";
import { setSpaceIcon } from "@/lib/actions";
import { spaceInitials } from "@/lib/layout";
import { cn } from "@/lib/utils";
import { IconPicker, StoredIcon, TextIcon } from "./IconPicker";

/** Its default face, the same for every panel without an icon of its own: a generic panel icon (a letter of its name
 * read as a word, 「空」 for 空白面板, and two panels starting alike looked the same). */
function PanelDefault({ className }: { className?: string }) {
  return <PanelsTopLeft className={cn("size-4", className)} />;
}

/**
 * A panel's face on the rail: the icon in its ui.json, the same kinds an agent's can be (a glyph, a line icon, a
 * sprite from an add-on, IconPicker), with its colour; else its default.
 */
export function PanelIcon({ icon, color, className }: { icon?: string; color?: string; title?: string; className?: string }) {
  return <StoredIcon icon={icon} color={color} className={cn("size-4", className)} fallback={<PanelDefault className={className} />} />;
}

/**
 * A project's face where it has an icon of its own (layout.ts Space icon / color): the icon, or its initials on a tile
 * of its colour when only a colour was picked. Callers draw their plain initials when it has neither (hasProjectIcon).
 */
export function ProjectIcon({ name, icon, color, className }: { name: string; icon?: string; color?: string; className?: string }) {
  const initials = <TextIcon text={spaceInitials(name)} color={color} className={className} />;
  return <StoredIcon icon={icon} color={color} className={className} fallback={initials} />;
}

export const hasProjectIcon = (sp: { icon?: string; color?: string }) => !!(sp.icon || sp.color);

/**
 * Change icon, for a panel (right-click on the rail, Settings → Projects & panels; saved into its user/ui.json, it
 * travels with the panel) or a project (saved into the layout's project entry): the icon picker with its custom
 * kinds, text and an uploaded picture.
 */
export function PanelIconDialog() {
  const d = useStore((s) => s.dialog);
  const ask = d?.kind === "panelIcon" || d?.kind === "projectIcon" ? d : undefined;
  const isProject = ask?.kind === "projectIcon";
  const name = ask?.kind === "projectIcon" ? ask.name : (ask?.title ?? "");
  const [icon, setIcon] = useState("");
  const [color, setColor] = useState("");
  const [err, setErr] = useState("");
  const [saving, setSaving] = useState(false);
  const id = ask?.kind === "projectIcon" ? "p:" + ask.project : ask?.key;
  useEffect(() => {
    setIcon(ask?.icon ?? "");
    setColor(ask?.color ?? "");
    setErr("");
  }, [id]);
  // Back to Settings when it came from there, else just closed
  const close = () => set({ dialog: ask?.back === "settings" ? { kind: "settings", tab: "panels" } : null });
  const save = async () => {
    if (!ask) return;
    setSaving(true);
    try {
      if (ask.kind === "projectIcon") setSpaceIcon(ask.project, icon, color);
      else {
        await api.setPanelIcon(ask.key, icon, color);
        panelsChanged();
      }
      close();
    } catch (e: any) {
      setErr(e.message ?? String(e));
    } finally {
      setSaving(false);
    }
  };
  return (
    <Dialog open={!!ask} onOpenChange={(o) => !o && close()}>
      <DialogContent className="sm:max-w-md">
        <DialogHeader>
          <DialogTitle>{t(isProject ? "projectIcon.title" : "panelIcon.title", { name })}</DialogTitle>
          <DialogDescription>{t(isProject ? "projectIcon.desc" : "panelIcon.desc")}</DialogDescription>
        </DialogHeader>
        <div className="flex items-center gap-3 rounded-lg border px-3 py-2">
          {/* What it will look like, at the rail's size */}
          <div className="flex size-9 items-center justify-center rounded-lg border">
            {isProject ? <ProjectIcon name={name} icon={icon} color={color} className="size-7" /> : <PanelIcon icon={icon} color={color} className="size-5" />}
          </div>
          <span className="min-w-0 truncate text-[13px]">{name}</span>
        </div>
        <IconPicker
          icon={icon}
          color={color}
          onChange={(p) => {
            if (p.icon !== undefined) setIcon(p.icon);
            if (p.color !== undefined) setColor(p.color);
          }}
          colorable={!icon.startsWith("sprite:") && !icon.startsWith("data:")}
          custom
          def={
            isProject
              ? { title: t("projectIcon.default"), node: <TextIcon text={spaceInitials(name)} color={color} className="size-5" />, glyph: "tiles" }
              : { title: t("panelIcon.default"), node: <PanelDefault className="size-5" />, glyph: "tiles" }
          }
        />
        {err && <div className="text-[12px] text-bad">{err}</div>}
        <DialogFooter>
          <Button variant="outline" onClick={close}>
            {t("common.cancel")}
          </Button>
          <Button disabled={saving} onClick={() => void save()}>
            {t("common.save")}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
