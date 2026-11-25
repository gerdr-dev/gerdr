import { useEffect, useState } from "react";
import { Bot, Copy, Download, Ellipsis, Pencil, RefreshCw, RotateCcw, SquarePlus, Trash2 } from "lucide-react";
import { toast } from "sonner";
import { Button } from "@/components/ui/button";
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger } from "@/components/ui/dropdown-menu";
import { Tabs, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { api } from "@/lib/api";
import { currentSpace, spaceList } from "@/lib/layout";
import { panelsChanged, usePanels, type Panel } from "@/lib/panels";
import { usePhone } from "@/lib/phone";
import { closeDialog, set, useStore } from "@/lib/store";
import { addTab } from "@/lib/actions";
import { showCode } from "@/lib/actions/views";
import { copyText } from "@/lib/clipboard";
import { t } from "@/lib/i18n";
import { removePanel, updatePanel } from "@/lib/panelActions";
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
  // Opening the page (or another project's tab) checks its panels' repositories now: the periodic check is hours apart
  useEffect(() => {
    let live = true;
    api.checkPanels(project).then(
      () => live && panelsChanged(),
      () => {},
    );
    return () => {
      live = false;
    };
  }, [project]);
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
  // A clone of a repository on this computer (file://…, a panel being made here and installed from it) read as a file,
  // and its 更新 as nonsense: it says 本机仓库 and the folder, from home
  const home = useStore((s) => s.home);
  const localRepo = p.source.remote?.startsWith("file://") ? decodeURI(p.source.remote.slice(7)) : undefined;
  const remote = localRepo
    ? t("panelSettings.localRepo", { path: home && localRepo.startsWith(home + "/") ? "~" + localRepo.slice(home.length) : localRepo })
    : p.source.remote?.replace(/^https?:\/\/(github\.com\/)?/, "");
  const update = async () => {
    setBusy("update");
    await updatePanel(p.key);
    setBusy("");
  };
  const remove = () => removePanel({ key: p.key, title: p.title });
  // Changing a panel is an agent's work in its folder, started from here: nobody has to know where that folder is
  // (the person, 2026-11-25: a panel assistant always on screen takes room, and the secretary isn't for this). A
  // new conversation's page opens on the folder; the agent reads the gerdr-ui skill when asked to change a panel.
  const askAgent = () => {
    if (!p.dir) return;
    closeDialog();
    addTab({ cwd: p.dir });
    showCode();
  };
  const copyPath = () => p.dir && copyText(p.dir).then(() => toast.success(t("panelSettings.copied"), { description: p.dir }));
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
          {remote && p.source.web ? (
            // A repository on the web opens in the browser (asked for); a local one (file://) has nothing to open
            <a href={p.source.web} target="_blank" rel="noreferrer" title={p.source.web} className="hover:text-foreground hover:underline">
              {t("panelSettings.from", { remote })}
            </a>
          ) : remote ? (
            t("panelSettings.from", { remote })
          ) : p.source.git ? (
            t("panelSettings.localGit")
          ) : (
            t("panelSettings.local")
          )}
          {p.source.dirty && ` · ${t("panelSettings.dirty")}`}
        </div>
      </div>
      {!!p.source.behind && (
        // Only told: updating stays this row's button (an update can change how the panel works)
        <span className="shrink-0 rounded-full bg-brand/15 px-2 py-0.5 text-[11px] text-brand" title={t("panelSettings.behindHint", { n: p.source.behind })}>
          {t("panelSettings.behind")}
        </span>
      )}
      {phone ? (
        // A phone's row is too narrow for three buttons: they left the name and source a few characters (reported)
        <DropdownMenu>
          <DropdownMenuTrigger asChild>
            <Button size="icon-sm" variant="ghost" className="shrink-0" aria-label={t("common.more")}>
              {busy ? <RefreshCw className="animate-spin" /> : <Ellipsis />}
            </Button>
          </DropdownMenuTrigger>
          <DropdownMenuContent align="end">
            {p.dir && (
              <>
                <DropdownMenuItem onClick={askAgent}>
                  <Bot /> {t("panelSettings.askAgent")}
                </DropdownMenuItem>
                <DropdownMenuItem onClick={() => void copyPath()}>
                  <Copy /> {t("panelSettings.copyPath")}
                </DropdownMenuItem>
              </>
            )}
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
        // One group, tight: as three items of the row they were spaced like the row's parts (gap-3) on top of their own padding
        <div className="flex shrink-0 items-center gap-0.5">
          {p.dir && (
            <>
              <Button size="sm" variant="ghost" title={t("panelSettings.askAgentHint", { dir: p.dir })} onClick={askAgent}>
                <Bot /> {t("panelSettings.askAgentShort")}
              </Button>
              <Button size="icon-sm" variant="ghost" title={t("panelSettings.copyPathHint", { dir: p.dir })} onClick={() => void copyPath()}>
                <Copy />
              </Button>
            </>
          )}
          <Button
            size="icon-sm"
            variant="ghost"
            title={t("panelSettings.rename")}
            onClick={() => set({ dialog: { kind: "rename", target: "panel", id: p.key, value: p.title } })}
          >
            <Pencil />
          </Button>
          {remote && (
            <Button size="sm" variant="ghost" disabled={!!busy} onClick={() => void update()}>
              <RefreshCw className={busy === "update" ? "animate-spin" : undefined} /> {t("panelSettings.update")}
            </Button>
          )}
          <Button size="sm" variant="ghost" className="text-bad hover:text-bad" onClick={() => void remove()}>
            <Trash2 /> {t("workSource.remove")}
          </Button>
        </div>
      )}
    </div>
  );
}
