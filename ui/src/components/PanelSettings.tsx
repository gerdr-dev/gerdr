import { useState } from "react";
import { Download, Ellipsis, Pencil, RefreshCw, RotateCcw, SquarePlus, Trash2 } from "lucide-react";
import { toast } from "sonner";
import { Button } from "@/components/ui/button";
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger } from "@/components/ui/dropdown-menu";
import { Tabs, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { api } from "@/lib/api";
import { currentSpace, spaceList } from "@/lib/layout";
import { panelsChanged, usePanels, type Panel } from "@/lib/panels";
import { usePhone } from "@/lib/phone";
import { set, useStore } from "@/lib/store";
import { t } from "@/lib/i18n";
import { removePanel } from "@/lib/panelActions";
import { PanelIcon } from "./PanelIcon";

/**
 * Settings → Panels: a project's panels in one place. Add one (install from a repository, a blank one), and for each:
 * its icon, update it from where it came from, remove it (to the trash). The project's own Work can be removed too,
 * and restored (blank) here.
 */
export function PanelSettings() {
  const layout = useStore((s) => s.layout);
  const spaces = spaceList(layout);
  const [project, setProject] = useState(() => currentSpace(layout));
  const { panels, ownRemoved, loaded } = usePanels(project);
  const name = spaces.find((s) => s.id === project)?.name ?? project;
  return (
    <div className="space-y-4">
      <p className="text-[12px] text-muted-foreground">{t("panelSettings.intro")}</p>
      {/* The projects as a row of tabs, not a dropdown (asked for): which one is shown, and the others, at a glance. It
          scrolls sideways when they don't fit */}
      <Tabs value={project} onValueChange={setProject}>
        <TabsList className="max-w-full justify-start overflow-x-auto overflow-y-hidden [scrollbar-width:none] [&::-webkit-scrollbar]:hidden">
          {spaces.map((s) => (
            <TabsTrigger key={s.id} value={s.id} className="flex-none">
              {s.name}
            </TabsTrigger>
          ))}
        </TabsList>
      </Tabs>
      <div className="flex flex-wrap items-center gap-2">
        <Button size="sm" variant="outline" onClick={() => set({ dialog: { kind: "workSource", project, action: "new" } })}>
          <SquarePlus /> {t("workSource.newTitle")}
        </Button>
        <Button size="sm" onClick={() => set({ dialog: { kind: "workSource", project, action: "install" } })}>
          <Download /> {t("workSource.installShort")}
        </Button>
      </div>
      <div className="divide-y rounded-lg border">
        {panels.map((p) => (
          <PanelRow key={p.key} p={p} />
        ))}
        {ownRemoved && (
          // The button goes under the text on a phone (it left the text a column four characters wide)
          <div className="flex flex-wrap items-center gap-x-3 gap-y-2 px-3 py-2.5">
            <div className="flex size-8 items-center justify-center rounded-md border border-dashed text-muted-foreground">
              <PanelIcon title={name} />
            </div>
            <div className="min-w-0 flex-1 basis-40">
              <div className="text-[13px] text-muted-foreground">{t("panelSettings.ownName")}</div>
              <div className="text-[12px] text-muted-foreground">{t("panelSettings.ownRemoved")}</div>
            </div>
            <Button
              size="sm"
              variant="outline"
              className="max-md:ml-11"
              onClick={() =>
                api.restoreWork(project).then(
                  () => panelsChanged(),
                  (e) => toast.error(e.message),
                )
              }
            >
              <RotateCcw /> {t("panelSettings.restore")}
            </Button>
          </div>
        )}
        {loaded && !panels.length && !ownRemoved && (
          <div className="px-3 py-6 text-center text-[12px] text-muted-foreground">{t("panelSettings.none", { name })}</div>
        )}
      </div>
    </div>
  );
}

function PanelRow({ p }: { p: Panel }) {
  const phone = usePhone();
  const [busy, setBusy] = useState("");
  const remote = p.source.remote?.replace(/^https?:\/\/(github\.com\/)?/, "");
  const update = async () => {
    setBusy("update");
    try {
      const r = await api.updateWork(p.key);
      toast.success(t("workSource.updated"), { description: r.output });
      panelsChanged();
    } catch (e: any) {
      toast.error(e.message);
    } finally {
      setBusy("");
    }
  };
  const remove = () => removePanel({ key: p.key, title: p.title });
  return (
    <div className="flex items-center gap-3 px-3 py-2.5">
      <button
        title={t("panelIcon.menu")}
        className="flex size-8 shrink-0 items-center justify-center rounded-md border hover:bg-accent"
        onClick={() => set({ dialog: { kind: "panelIcon", key: p.key, title: p.title, icon: p.icon, color: p.color } })}
      >
        <PanelIcon icon={p.icon} color={p.color} title={p.title} />
      </button>
      <div className="min-w-0 flex-1">
        <div className="truncate text-[13px]">{p.title}</div>
        <div className="truncate text-[12px] text-muted-foreground">
          {remote ? t("panelSettings.from", { remote }) : p.source.git ? t("panelSettings.localGit") : t("panelSettings.local")}
          {p.source.dirty && ` · ${t("panelSettings.dirty")}`}
        </div>
      </div>
      {phone ? (
        // A phone's row is too narrow for three buttons: they left the name and source a few characters (reported)
        <DropdownMenu>
          <DropdownMenuTrigger asChild>
            <Button size="icon-sm" variant="ghost" className="shrink-0" aria-label={t("common.more")}>
              {busy ? <RefreshCw className="animate-spin" /> : <Ellipsis />}
            </Button>
          </DropdownMenuTrigger>
          <DropdownMenuContent align="end">
            <DropdownMenuItem onClick={() => set({ dialog: { kind: "rename", target: "panel", id: p.key, value: p.title } })}>
              <Pencil /> {t("panelSettings.rename")}
            </DropdownMenuItem>
            {remote && (
              <DropdownMenuItem disabled={!!busy} onClick={() => void update()}>
                <RefreshCw /> {t("panelSettings.update")}
              </DropdownMenuItem>
            )}
            <DropdownMenuItem variant="destructive" onClick={() => void remove()}>
              <Trash2 /> {t("workSource.remove")}
            </DropdownMenuItem>
          </DropdownMenuContent>
        </DropdownMenu>
      ) : (
        <>
          <Button
            size="sm"
            variant="ghost"
            title={t("panelSettings.rename")}
            onClick={() => set({ dialog: { kind: "rename", target: "panel", id: p.key, value: p.title } })}
          >
            <Pencil />
          </Button>
          {remote && (
            <Button size="sm" variant="ghost" disabled={!!busy} onClick={() => void update()} title={p.source.remote}>
              <RefreshCw className={busy === "update" ? "animate-spin" : undefined} /> {t("panelSettings.update")}
            </Button>
          )}
          <Button size="sm" variant="ghost" className="text-bad hover:text-bad" onClick={() => void remove()}>
            <Trash2 /> {t("workSource.remove")}
          </Button>
        </>
      )}
    </div>
  );
}
