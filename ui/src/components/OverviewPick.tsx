import { ArrowDownWideNarrow, Check, ListPlus, LogOut, MoreHorizontal, Plus, SquarePlus } from "lucide-react";
import { useEffect, useMemo, useState } from "react";
import { Button } from "@/components/ui/button";
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuSeparator, DropdownMenuTrigger } from "@/components/ui/dropdown-menu";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { basename, focusSession, newAgentInOverview, newTabForOverview } from "@/lib/actions";
import { currentSpace, spaceList } from "@/lib/layout";
import { compareSessions } from "@/lib/order";
import { activeAllProjects, activeIds, addActive, focusTile, placeTilesAfter, toggleOverview } from "@/lib/overview";
import { openSessionsWithProject } from "@/lib/overviewState";
import { useStore, useStoreWhile } from "@/lib/store";
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
 * The phone bar's ⋯ menu. The + used to start an agent like the shown tile's on a single tap (easy to hit by accident,
 * and no way to add one already open). Now a menu: a new one like the shown tile's (Cmd+D in the grid; only with a
 * tile), a new tab to pick any agent / folder in (it joins "active" once started), pick open ones, and re-sort the strip by "look at first" (its order is frozen on opening, phoneOrder, so one
 * finished or waiting since stays where it was until asked for).
 */
export function PhoneMenu({ focus, panel, onResort }: { focus?: string; panel?: boolean; onResort: () => void }) {
  const s = useStore((st) => (focus ? st.sessions[focus] : undefined));
  const [pick, setPick] = useState(false);
  return (
    <>
      <DropdownMenu>
        <DropdownMenuTrigger asChild>
          <Button variant="ghost" size="icon" className="size-9 shrink-0" aria-label={t("overviewPick.more")}>
            <MoreHorizontal className="size-[18px]" />
          </Button>
        </DropdownMenuTrigger>
        <DropdownMenuContent align="end" className="max-w-72">
          {s && (
            <DropdownMenuItem onClick={() => newAgentInOverview()}>
              <AgentIcon agent={s.agent} className="size-4" />
              <span className="min-w-0 truncate">{t("overviewPick.newLike", { dir: basename(s.cwd) })}</span>
            </DropdownMenuItem>
          )}
          {/* Any agent / folder / earlier conversation, on the new-session screen; what starts there joins "active" */}
          <DropdownMenuItem onClick={() => newTabForOverview()}>
            <SquarePlus /> {t("overviewPick.newTab")}
          </DropdownMenuItem>
          {/* A hosting panel's overview (`panel`, core useCodeOverview) is its list in its order: nothing to add to it by
              hand (picks go into Code's "active"), no order of ours to re-sort */}
          {!panel && (
            <DropdownMenuItem onClick={() => setPick(true)}>
              <ListPlus /> {t("overviewPick.addExisting")}
            </DropdownMenuItem>
          )}
          {!panel && (
            <DropdownMenuItem onClick={onResort}>
              <ArrowDownWideNarrow /> {t("overviewPick.resort")}
            </DropdownMenuItem>
          )}
          <DropdownMenuSeparator />
          {/* Back to the tabs, on the agent shown here (the drawer's Overview toggle was the only way out, two taps deep) */}
          <DropdownMenuItem onClick={() => (toggleOverview(false), focus && focusSession(focus))}>
            <LogOut /> {t("overviewPick.exit")}
          </DropdownMenuItem>
        </DropdownMenuContent>
      </DropdownMenu>
      <PickActive open={pick} onClose={() => setPick(false)} after={s?.id} />
    </>
  );
}

/**
 * Tick open agents that aren't in "active" yet and put them in at once. Lists the same scope "active" shows (this
 * project, or all with allIn): one from elsewhere would be added but never appear.
 */
function PickActive({ open, onClose, after }: { open: boolean; onClose: () => void; after?: string }) {
  // Every tile's + menu has one of these, closed nearly always: follow the store only while open
  const layout = useStoreWhile(open, (s) => s.layout);
  const sessions = useStoreWhile(open, (s) => s.sessions);
  const unread = useStoreWhile(open, (s) => s.unread);
  const config = useStoreWhile(open, (s) => s.config);
  const overview = useStoreWhile(open, (s) => s.overview);
  const [picked, setPicked] = useState<string[]>([]);
  useEffect(() => {
    if (open) setPicked([]);
  }, [open]);
  const space = currentSpace(layout);
  const all = activeAllProjects({ config, layout });
  // Memoized on the frozen store values above: the parent re-renders on every push of its tile's session
  const list = useMemo(() => {
    const inActive = new Set(activeIds({ overview, layout, sessions, config, unread }));
    return openSessionsWithProject(layout, sessions)
      .filter((x) => (all || x.project === space) && !inActive.has(x.s.id))
      .map((x) => x.s)
      .sort((a, b) => compareSessions(a, b, unread));
  }, [overview, layout, sessions, config, unread, all, space]);
  const toggle = (id: string) => setPicked((p) => (p.includes(id) ? p.filter((x) => x !== id) : [...p, id]));
  const confirm = () => {
    addActive(picked);
    // From a tile's + menu: they go right after that tile, not on the end of the strip
    if (after) placeTilesAfter(picked, after);
    // The first one ticked is the one shown (a phone shows one tile at a time)
    focusTile(picked[0]);
    onClose();
  };
  const rows = useMemo(
    () =>
      list.map((s) => {
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
              <span className="block truncate text-[11px] text-muted-foreground">{whereOf(layout, s.id)}</span>
            </span>
            <StatusDot status={dotStatus(s)} unread={!!unread[s.id]} />
          </button>
        );
      }),
    [list, picked, layout, config, unread],
  );
  return (
    <Dialog open={open} onOpenChange={(o) => !o && onClose()}>
      <DialogContent className="gap-3 p-4" onOpenAutoFocus={(e) => e.preventDefault()}>
        <DialogHeader>
          <DialogTitle className="text-[15px]">{t("overviewPick.title")}</DialogTitle>
          <DialogDescription className="text-[12px]">{t("overviewPick.desc")}</DialogDescription>
        </DialogHeader>
        {list.length ? (
          <div className="-mx-1 max-h-[55vh] overflow-y-auto">{rows}</div>
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
