import { Check, ListPlus, Plus } from "lucide-react";
import { useEffect, useState } from "react";
import { Button } from "@/components/ui/button";
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger } from "@/components/ui/dropdown-menu";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { basename, newAgentInOverview } from "@/lib/actions";
import { currentSpace, spaceList } from "@/lib/layout";
import { compareSessions } from "@/lib/order";
import { activeAllProjects, activeIds, addActive, focusTile } from "@/lib/overview";
import { openSessionsWithProject } from "@/lib/overviewState";
import { useStore } from "@/lib/store";
import { cn } from "@/lib/utils";
import { AgentIcon, StatusDot, dotStatus } from "./AgentIcon";
import { whereOf } from "./tileInfo";
import { t } from "@/lib/i18n";

/**
 * "Pick a few" on an empty grid: opens PickActive. It used to open the sidebar drawer, but there tapping an agent goes
 * to its tab (leaving the grid) and "add to active" hides in the long-press menu, so on a phone nothing could be added.
 */
export function PickButton() {
  const [open, setOpen] = useState(false);
  return (
    <>
      <Button size="sm" variant="outline" onClick={() => setOpen(true)}>
        <Plus className="size-3.5" /> {t("overviewPick.pickSome")}
      </Button>
      <PickActive open={open} onClose={() => setOpen(false)} />
    </>
  );
}

/**
 * The phone bar's +. It used to start an agent like the shown tile's on a single tap (easy to hit by accident, and no
 * way to add one already open) and, with no tile, open the drawer (where nothing can be added, see PickButton). Now:
 * with a tile, a menu of the two (a new one like it, Cmd+D in the grid; or pick open ones); without, the picker.
 */
export function AddMenu({ focus }: { focus?: string }) {
  const s = useStore((st) => (focus ? st.sessions[focus] : undefined));
  const [pick, setPick] = useState(false);
  const button = (onClick?: () => void) => (
    <Button variant="ghost" size="icon" className="size-9 shrink-0" aria-label={t("overviewPick.addAgent")} onClick={onClick}>
      <Plus className="size-[18px]" />
    </Button>
  );
  return (
    <>
      {s ? (
        <DropdownMenu>
          <DropdownMenuTrigger asChild>{button()}</DropdownMenuTrigger>
          <DropdownMenuContent align="end" className="max-w-72">
            <DropdownMenuItem onClick={() => newAgentInOverview()}>
              <AgentIcon agent={s.agent} className="size-4" />
              <span className="min-w-0 truncate">{t("overviewPick.newLike", { dir: basename(s.cwd) })}</span>
            </DropdownMenuItem>
            <DropdownMenuItem onClick={() => setPick(true)}>
              <ListPlus /> {t("overviewPick.addExisting")}
            </DropdownMenuItem>
          </DropdownMenuContent>
        </DropdownMenu>
      ) : (
        button(() => setPick(true))
      )}
      <PickActive open={pick} onClose={() => setPick(false)} />
    </>
  );
}

/**
 * Tick open agents that aren't in "active" yet and put them in at once. Lists the same scope "active" shows (this
 * project, or all with allIn): one from elsewhere would be added but never appear.
 */
function PickActive({ open, onClose }: { open: boolean; onClose: () => void }) {
  const layout = useStore((s) => s.layout);
  const sessions = useStore((s) => s.sessions);
  const unread = useStore((s) => s.unread);
  const config = useStore((s) => s.config);
  const overview = useStore((s) => s.overview);
  const [picked, setPicked] = useState<string[]>([]);
  useEffect(() => {
    if (open) setPicked([]);
  }, [open]);
  const space = currentSpace(layout);
  const all = activeAllProjects({ config, layout });
  const inActive = new Set(activeIds({ overview, layout, sessions, config, unread }));
  const list = openSessionsWithProject(layout, sessions, config)
    .filter((x) => (all || x.project === space) && !inActive.has(x.s.id))
    .map((x) => x.s)
    .sort((a, b) => compareSessions(a, b, unread));
  const toggle = (id: string) => setPicked((p) => (p.includes(id) ? p.filter((x) => x !== id) : [...p, id]));
  const confirm = () => {
    addActive(picked);
    // The first one ticked is the one shown (a phone shows one tile at a time)
    focusTile(picked[0]);
    onClose();
  };
  return (
    <Dialog open={open} onOpenChange={(o) => !o && onClose()}>
      <DialogContent className="gap-3 p-4" onOpenAutoFocus={(e) => e.preventDefault()}>
        <DialogHeader>
          <DialogTitle className="text-[15px]">{t("overviewPick.title")}</DialogTitle>
          <DialogDescription className="text-[12px]">{t("overviewPick.desc")}</DialogDescription>
        </DialogHeader>
        {list.length ? (
          <div className="-mx-1 max-h-[55vh] overflow-y-auto">
            {list.map((s) => {
              const on = picked.includes(s.id);
              return (
                <button
                  key={s.id}
                  className={cn("flex h-11 w-full items-center gap-2.5 rounded-md px-2 text-left text-[13px]", on ? "bg-brand/10" : "hover:bg-accent/60")}
                  onClick={() => toggle(s.id)}
                >
                  <span
                    className={cn(
                      "flex size-4 shrink-0 items-center justify-center rounded-sm border",
                      on ? "border-brand bg-brand text-white" : "border-muted-foreground/50",
                    )}
                  >
                    {on && <Check className="size-3" />}
                  </span>
                  <AgentIcon agent={s.agent} className="size-4 shrink-0" />
                  <span className="min-w-0 flex-1">
                    <span className="block truncate">{s.title || basename(s.cwd)}</span>
                    <span className="block truncate text-[11px] text-muted-foreground">{whereOf(layout, config, s.id)}</span>
                  </span>
                  <StatusDot status={dotStatus(s)} unread={!!unread[s.id]} />
                </button>
              );
            })}
          </div>
        ) : (
          <div className="py-6 text-center text-[13px] text-muted-foreground">{all ? t("overviewPick.allIn") : t("overviewPick.allInProject")}</div>
        )}
        {!all && spaceList(layout).length > 1 && <div className="text-[11px] text-muted-foreground">{t("overviewPick.onlyThisProject")}</div>}
        <DialogFooter className="flex-row justify-end">
          <Button size="sm" variant="outline" onClick={onClose}>
            {t("common.cancel")}
          </Button>
          <Button size="sm" disabled={!picked.length} onClick={confirm}>
            {picked.length ? t("overviewPick.addN", { n: picked.length }) : t("overviewPick.add")}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
