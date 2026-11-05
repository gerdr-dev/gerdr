import { ArrowDown, ArrowLeft, ArrowRight, ArrowUp, Blocks, Check, ChevronsUpDown, Download, Pencil, Plus, SquarePlus, Trash2, X } from "lucide-react";
import { useLayoutEffect, useRef, useState } from "react";
import { ContextMenu, ContextMenuContent, ContextMenuItem, ContextMenuSeparator, ContextMenuTrigger } from "@/components/ui/context-menu";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { toast } from "sonner";
import { closeSpaceUndoable, deleteSpace, moveSpace, switchSpace } from "@/lib/actions";
import { currentSpace, DEFAULT_SPACE, spaceList, spaceWorkspaces, type Layout, type Space } from "@/lib/layout";
import { get, set, useStore } from "@/lib/store";
import type { SessionSummary } from "@/lib/types";
import { cn } from "@/lib/utils";
import { StatusDot, dotRank } from "./AgentIcon";
import { askConfirm } from "@/lib/confirm";
import { isNative, nativeNewWindow } from "@/lib/native";
import { SpaceDropLine, startSpaceDrag } from "./SpaceDrag";
import { tabStatus } from "./TabBar";
import { t } from "@/lib/i18n";

/** A session's overall state for the switcher: the most urgent of its workspaces' (same ranking as WsDot). Muted ones say nothing. */
export function spaceStatus(l: Layout, sp: Space, sessions: Record<string, SessionSummary>, unread: Record<string, true>) {
  let best: { status?: string; unread: boolean } = { unread: false };
  let rank = 9;
  for (const w of spaceWorkspaces(l, sp.id))
    for (const t of w.muted ? [] : w.tabs) {
      const st = tabStatus(t, sessions, unread);
      const r = dotRank(st.status, st.unread);
      if (r < rank) {
        rank = r;
        best = st;
      }
    }
  return rank === 9 ? undefined : best;
}

/**
 * Which herdr-style session the sidebar shows (layout.ts Space). One line above the workspaces: the current one's name, and a dot when
 * another session has something for you — its agents are out of the lists below, so this is the only place that tells you.
 */
export async function askCloseSpace(sp: Space) {
  const n = spaceWorkspaces(get().layout, sp.id).length;
  const ok = await askConfirm({
    title: t("spaceSwitcher.closeTitle", { name: sp.name }),
    description: t("spaceSwitcher.closeDesc", { n, id: sp.id }),
    action: t("common.close"),
    destructive: true,
  });
  if (!ok) return;
  const undo = closeSpaceUndoable(sp.id);
  toast.success(t("spaceSwitcher.closed", { name: sp.name }), {
    duration: 10000,
    action: {
      label: t("spaceSwitcher.undo"),
      onClick: () => {
        try {
          undo();
          toast.success(t("spaceSwitcher.reopened", { name: sp.name }));
        } catch (e: any) {
          toast.error(t("spaceSwitcher.restoreFailed", { error: e?.message ?? e }));
        }
      },
    },
  });
}

export async function askDeleteSpace(sp: Space) {
  const n = spaceWorkspaces(get().layout, sp.id).length;
  const ok = await askConfirm({
    title: t("spaceSwitcher.deleteTitle", { name: sp.name }),
    description: t("spaceSwitcher.deleteDesc", { n }),
    action: t("common.delete"),
    destructive: true,
  });
  if (!ok) return;
  try {
    const { trash, undo } = await deleteSpace(sp.id);
    toast.success(t("spaceSwitcher.deleted", { name: sp.name }), {
      description: trash ? t("spaceSwitcher.movedTo", { path: trash }) : undefined,
      duration: 10000,
      action: {
        label: t("spaceSwitcher.undo"),
        onClick: () =>
          undo().then(
            () => toast.success(t("spaceSwitcher.restored", { name: sp.name })),
            (e) => toast.error(t("spaceSwitcher.restoreFailed", { error: e?.message ?? e })),
          ),
      },
    });
  } catch (e: any) {
    toast.error(t("spaceSwitcher.deleteFailed", { error: e?.message ?? e }));
  }
}

export const newSpaceDialog = () => set({ dialog: { kind: "rename", target: "newSpace", id: "", value: "" } });
export const renameSpaceDialog = (sp: Space) => set({ dialog: { kind: "rename", target: "space", id: sp.id, value: sp.name } });
export const projectExtDialog = (sp: Space) => set({ dialog: { kind: "projectExt", project: sp.id, name: sp.name } });

/**
 * A project chip's right-click (long-press) menu, the same on the rail and in the sidebar header. Moving is also here for
 * touch screens, where a chip can't be dragged; `axis` names the directions after how the list runs.
 */
export function SpaceMenuItems({ sp, axis }: { sp: Space; axis: "x" | "y" }) {
  const spaces = spaceList(useStore((s) => s.layout)); // not inside the selector: a new array each call loops the store
  const i = spaces.findIndex((x) => x.id === sp.id);
  const [Back, Fwd] = axis === "y" ? [ArrowUp, ArrowDown] : [ArrowLeft, ArrowRight];
  return (
    <>
      {isNative() && <ContextMenuItem onClick={() => nativeNewWindow(sp.id)}>{t("nav.openInNewWindow")}</ContextMenuItem>}
      <ContextMenuItem onClick={() => renameSpaceDialog(sp)}>
        <Pencil /> {t("spaceSwitcher.renameEllipsis")}
      </ContextMenuItem>
      <ContextMenuItem onClick={() => projectExtDialog(sp)}>
        <Blocks /> {t("spaceSwitcher.extEllipsis")}
      </ContextMenuItem>
      {i > 0 && (
        <ContextMenuItem onClick={() => moveSpace(sp.id, i - 1)}>
          <Back /> {axis === "y" ? t("spaceSwitcher.moveUp") : t("spaceSwitcher.moveLeft")}
        </ContextMenuItem>
      )}
      {i < spaces.length - 1 && (
        <ContextMenuItem onClick={() => moveSpace(sp.id, i + 1)}>
          <Fwd /> {axis === "y" ? t("spaceSwitcher.moveDown") : t("spaceSwitcher.moveRight")}
        </ContextMenuItem>
      )}
      <ContextMenuSeparator />
      <WorkSourceItems project={sp.id} />
      {(spaces.length > 1 || sp.id !== DEFAULT_SPACE) && <ContextMenuSeparator />}
      {spaces.length > 1 && (
        <ContextMenuItem onClick={() => askCloseSpace(sp)}>
          <X /> {t("spaceSwitcher.closeProject")}
        </ContextMenuItem>
      )}
      {sp.id !== DEFAULT_SPACE && (
        <ContextMenuItem variant="destructive" onClick={() => askDeleteSpace(sp)}>
          <Trash2 /> {t("spaceSwitcher.deleteProject")}
        </ContextMenuItem>
      )}
    </>
  );
}

/** Add a panel to the project: one someone published (a git repository), or a blank one to build. */
function WorkSourceItems({ project }: { project: string }) {
  return (
    <>
      <ContextMenuItem onClick={() => set({ dialog: { kind: "workSource", project, action: "install" } })}>
        <Download /> {t("workSource.menuInstall")}
      </ContextMenuItem>
      <ContextMenuItem onClick={() => set({ dialog: { kind: "workSource", project, action: "new" } })}>
        <SquarePlus /> {t("workSource.menuNew")}
      </ContextMenuItem>
    </>
  );
}

/**
 * The session switcher in the sidebar header. When every session's name fits on one line they sit side by side like tabs
 * (one click to switch, asked for); when they don't, it folds into a dropdown (SpaceMenu). Fitting is measured on an invisible
 * copy of the row, re-measured when the header resizes or a name changes.
 */
export function SpaceSwitcher() {
  const layout = useStore((s) => s.layout);
  const sessions = useStore((s) => s.sessions);
  const unread = useStore((s) => s.unread);
  const spaces = spaceList(layout);
  const here = currentSpace(layout);
  const box = useRef<HTMLDivElement>(null);
  const ruler = useRef<HTMLDivElement>(null);
  const [fits, setFits] = useState(true);
  const names = spaces.map((s) => s.name).join("\0");
  useLayoutEffect(() => {
    const el = box.current;
    const r = ruler.current;
    if (!el || !r) return;
    const check = () => setFits(r.scrollWidth <= el.clientWidth);
    check();
    const ro = new ResizeObserver(check);
    ro.observe(el);
    return () => ro.disconnect();
  }, [names]);
  const chip = (sp: Space, live: boolean) => {
    const st = sp.id === here ? undefined : spaceStatus(layout, sp, sessions, unread);
    return (
      <button
        key={sp.id}
        data-space-id={live ? sp.id : undefined}
        tabIndex={live ? undefined : -1}
        className={cn(
          "flex h-6 max-w-28 shrink-0 items-center gap-1 rounded-md px-1.5 text-[12px] font-semibold whitespace-nowrap",
          sp.id === here ? "bg-accent text-foreground" : "text-muted-foreground hover:bg-accent/60 hover:text-foreground",
          // Mark which session the context menu belongs to while it's open
          "data-[state=open]:ring-1 data-[state=open]:ring-brand/60",
        )}
        style={{ WebkitAppRegion: "no-drag" } as any}
        onClick={live ? () => switchSpace(sp.id) : undefined}
        onPointerDown={live ? (e) => startSpaceDrag(e, sp.id, "x") : undefined}
      >
        <span className="min-w-0 truncate">{sp.name}</span>
        {st && <StatusDot status={st.status} unread={st.unread} />}
      </button>
    );
  };
  const plus = (live: boolean) => (
    <button
      tabIndex={live ? undefined : -1}
      title={t("spaceSwitcher.newProjectHint")}
      className="flex size-6 shrink-0 items-center justify-center rounded-md text-muted-foreground hover:bg-accent/60 hover:text-foreground"
      style={{ WebkitAppRegion: "no-drag" } as any}
      onClick={live ? newSpaceDialog : undefined}
    >
      <Plus className="size-3.5" />
    </button>
  );
  return (
    <div ref={box} className="relative flex min-w-0 flex-1 items-center">
      <div ref={ruler} aria-hidden className="pointer-events-none invisible absolute top-0 left-0 flex w-max items-center gap-0.5">
        {spaces.map((sp) => chip(sp, false))}
        {plus(false)}
      </div>
      {fits ? (
        <div data-space-list="header" className="relative -ml-1 flex min-w-0 items-center gap-0.5">
          {spaces.map((sp) => (
            <ContextMenu key={sp.id}>
              <ContextMenuTrigger asChild>{chip(sp, true)}</ContextMenuTrigger>
              <ContextMenuContent>
                <SpaceMenuItems sp={sp} axis="x" />
              </ContextMenuContent>
            </ContextMenu>
          ))}
          {plus(true)}
          <SpaceDropLine list="header" axis="x" />
        </div>
      ) : (
        <SpaceMenu />
      )}
    </div>
  );
}

/** The folded form of the switcher: the current session's name, a dot when another one needs you, everything else in a menu. */
function SpaceMenu() {
  const layout = useStore((s) => s.layout);
  const sessions = useStore((s) => s.sessions);
  const unread = useStore((s) => s.unread);
  const spaces = spaceList(layout);
  const here = currentSpace(layout);
  const cur = spaces.find((s) => s.id === here)!;
  const elsewhere = spaces
    .filter((s) => s.id !== here)
    .map((s) => spaceStatus(layout, s, sessions, unread))
    .filter((x) => x)
    .sort((a, b) => +(b!.status === "waiting") - +(a!.status === "waiting"))[0];
  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <button
          className="-ml-1 flex h-7 max-w-full min-w-0 items-center gap-1.5 rounded-md px-1.5 text-left text-[12px] font-semibold text-muted-foreground hover:bg-accent/60 hover:text-foreground data-[state=open]:bg-accent"
          style={{ WebkitAppRegion: "no-drag" } as any}
        >
          <span className="min-w-0 truncate text-foreground">{cur.name}</span>
          {elsewhere && <StatusDot status={elsewhere.status} unread={elsewhere.unread} />}
          <ChevronsUpDown className="size-3 shrink-0" />
        </button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="start" className="w-52">
        <DropdownMenuLabel className="text-[11px] font-normal text-muted-foreground">{t("spaceSwitcher.isolated")}</DropdownMenuLabel>
        {spaces.map((sp) => {
          const st = spaceStatus(layout, sp, sessions, unread);
          return (
            <DropdownMenuItem key={sp.id} onClick={() => switchSpace(sp.id)}>
              <Check className={cn("size-3.5", sp.id !== here && "invisible")} />
              <span className="min-w-0 flex-1 truncate">{sp.name}</span>
              <span className="text-[11px] text-muted-foreground tabular-nums">{spaceWorkspaces(layout, sp.id).length}</span>
              <StatusDot status={st?.status} unread={!!st?.unread} />
            </DropdownMenuItem>
          );
        })}
        <DropdownMenuSeparator />
        {isNative() && (
          <DropdownMenuItem onClick={() => nativeNewWindow(cur.id)}>{t("spaceSwitcher.openNamedInNewWindow", { name: cur.name })}</DropdownMenuItem>
        )}
        <DropdownMenuItem onClick={newSpaceDialog}>
          <Plus className="size-3.5" /> {t("nav.newProject")}
        </DropdownMenuItem>
        <DropdownMenuItem onClick={() => renameSpaceDialog(cur)}>
          <Pencil className="size-3.5" /> {t("spaceSwitcher.renameNamed", { name: cur.name })}
        </DropdownMenuItem>
        <DropdownMenuItem onClick={() => projectExtDialog(cur)}>
          <Blocks className="size-3.5" /> {t("spaceSwitcher.extNamed", { name: cur.name })}
        </DropdownMenuItem>
        {spaces.length > 1 && (
          <DropdownMenuItem onClick={() => askCloseSpace(cur)}>
            <X className="size-3.5" /> {t("spaceSwitcher.closeNamed", { name: cur.name })}
          </DropdownMenuItem>
        )}
        {cur.id !== DEFAULT_SPACE && (
          <DropdownMenuItem variant="destructive" onClick={() => askDeleteSpace(cur)}>
            <Trash2 className="size-3.5" /> {t("spaceSwitcher.deleteNamed", { name: cur.name })}
          </DropdownMenuItem>
        )}
      </DropdownMenuContent>
    </DropdownMenu>
  );
}
