import { useEffect, useState } from "react";
import { Briefcase, icons, type LucideIcon } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { api } from "@/lib/api";
import { t } from "@/lib/i18n";
import { set, useStore } from "@/lib/store";
import { cn } from "@/lib/utils";

/** A lucide icon by the name people write ("kanban-square", "KanbanSquare"), or undefined. */
function lucide(name: string): LucideIcon | undefined {
  const pascal = name
    .trim()
    .split(/[-_\s]+/)
    .map((w) => w.charAt(0).toUpperCase() + w.slice(1))
    .join("");
  return (icons as Record<string, LucideIcon>)[pascal];
}

function short(name: string) {
  const chars = [...name];
  return /^[\x00-\x7f]/.test(name) ? chars.slice(0, 2).join("").toUpperCase() : chars[0];
}

/**
 * A panel's face on the rail: its ui.json icon (a lucide name, else the text itself: an emoji, a letter or two), else
 * the briefcase for a project's own Work and the first letters of its name for a panel.
 */
export function PanelIcon({ icon, title, own, className }: { icon?: string; title: string; own: boolean; className?: string }) {
  const L = icon ? lucide(icon) : undefined;
  if (L) return <L className={cn("size-4", className)} />;
  // Text that isn't an icon name (an emoji, a character or two) is the face; a name lucide doesn't have falls back
  if (icon && !/^[a-z][a-z0-9-]{2,}$/i.test(icon.trim())) return <span className="text-[13px] leading-none">{[...icon].slice(0, 2).join("")}</span>;
  if (own) return <Briefcase className={cn("size-4", className)} />;
  return <span className="text-[11px] font-semibold">{short(title)}</span>;
}

// Offered in the picker: what a panel tends to be about. Any lucide name or an emoji can be typed instead.
const SUGGESTED = [
  "briefcase",
  "square-kanban",
  "list-todo",
  "layout-dashboard",
  "calendar",
  "git-pull-request",
  "bug",
  "rocket",
  "target",
  "chart-line",
  "book-open",
  "notebook-pen",
  "inbox",
  "mail",
  "message-square",
  "users",
  "server",
  "database",
  "shield-check",
  "wrench",
  "sparkles",
  "flame",
  "star",
  "bot",
];

/** Right-click → change icon: pick one, or type a lucide name / an emoji. Saved into the panel's ui.json. */
export function PanelIconDialog() {
  const d = useStore((s) => s.dialog);
  const ask = d?.kind === "panelIcon" ? d : undefined;
  const [v, setV] = useState("");
  const [err, setErr] = useState("");
  useEffect(() => {
    setV(ask?.icon ?? "");
    setErr("");
  }, [ask?.key]);
  const save = async (icon: string) => {
    if (!ask) return;
    try {
      await api.setPanelIcon(ask.key, icon);
      set({ dialog: null });
    } catch (e: any) {
      setErr(e.message ?? String(e));
    }
  };
  return (
    <Dialog open={!!ask} onOpenChange={(o) => !o && set({ dialog: null })}>
      <DialogContent className="sm:max-w-md">
        <DialogHeader>
          <DialogTitle>{t("panelIcon.title", { name: ask?.title ?? "" })}</DialogTitle>
          <DialogDescription>{t("panelIcon.desc")}</DialogDescription>
        </DialogHeader>
        <div className="grid grid-cols-8 gap-1">
          {SUGGESTED.map((n) => {
            const L = lucide(n);
            if (!L) return null; // renamed in another lucide version
            return (
              <button
                key={n}
                title={n}
                onClick={() => setV(n)}
                className={cn("flex size-9 items-center justify-center rounded-md hover:bg-muted", v === n && "bg-muted ring-1 ring-ring")}
              >
                <L className="size-4" />
              </button>
            );
          })}
        </div>
        <form
          className="flex items-center gap-2"
          onSubmit={(e) => {
            e.preventDefault();
            void save(v);
          }}
        >
          <div className="flex size-9 shrink-0 items-center justify-center rounded-md border">
            <PanelIcon icon={v} title={ask?.title ?? ""} own={false} />
          </div>
          <Input value={v} onChange={(e) => setV(e.target.value)} placeholder={t("panelIcon.placeholder")} />
        </form>
        {err && <div className="text-[12px] text-bad">{err}</div>}
        <DialogFooter>
          <Button variant="ghost" onClick={() => void save("")}>
            {t("panelIcon.reset")}
          </Button>
          <Button onClick={() => void save(v)}>{t("common.save")}</Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
