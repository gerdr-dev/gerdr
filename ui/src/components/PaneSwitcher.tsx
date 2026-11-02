import { Plus, Search, X } from "lucide-react";
import { useEffect, useMemo, useRef, useState } from "react";
import { Button } from "@/components/ui/button";
import { PopoverContent } from "@/components/ui/popover";
import { addTab, basename, focusPane, selectWorkspace, tabName } from "@/lib/actions";
import { allLeaves, currentSpace, leaves, spaceOf, spaceWorkspaces, type PaneLeaf } from "@/lib/layout";
import { isCoarse } from "@/lib/phone";
import { applyWorkspaceLabel } from "@/lib/addons";
import { set, useStore } from "@/lib/store";
import type { SessionSummary } from "@/lib/types";
import { cn } from "@/lib/utils";
import { StatusDot, dotStatus } from "./AgentIcon";
import { compareSessions, sessionRank } from "@/lib/order";
import { WsDot } from "./WsDot";

/**
 * 面板一览：**手机上换 pane 的那条路**（照 herdr-web 的 PaneSwitcher）。
 *
 * 手机上一次只看得见一个 pane（多 pane 平铺读不了），tab 栏也收掉了，于是「另一个工作空间里那个在等我的 agent」
 * 得有地方一眼找到、点一下就过去：这里把**所有工作空间的所有 pane** 拍平成一张表，按「多想让你看一眼」排。
 *
 * 它是**索引，不是第二个管理界面**：只有「去哪个」和「新开一个」，关 / 改名 / 分屏还在 pane 标题栏的菜单里。
 */
/** tab 带序号、分屏的 pane 带「第几个」：同一个 tab 里两个空 pane、或两个同名 tab，只有这两样分得开。 */
type Row = { pane: PaneLeaf; session?: SessionSummary; ws: string; wsId: string; tab: string; at: string; i: number };

/**
 * 优先级分档：和侧栏 agents 那张表同一套（lib/order.ts），只是多了最后一档 5 = 还没开会话的空 pane
 * （手机上新开的空 pane 也得能找回来）。
 */
function rankOf(r: Row, unread: Record<string, true>) {
  return r.session ? sessionRank(r.session, unread) : 5;
}

/** 只给最该被看见的几个状态加字：其余靠那个点的颜色，别把每行都塞满标签 */
const CHIP: Record<number, { text: string; cls: string }> = {
  0: { text: "等你", cls: "border-bad/50 bg-bad/15 text-bad" },
  1: { text: "出错", cls: "border-bad/50 bg-bad/10 text-bad" },
  2: { text: "完成", cls: "border-ok/40 bg-ok/12 text-ok" },
};

const shortCwd = (p: string) => p.replace(/^\/(?:Users|home)\/[^/]+/, "~");

/** 「上次动过」多久了：`3m` `2h` `4d` 这种紧凑写法 —— 这一列在手机上只有几十像素，完整时间在 title 里。 */
function ago(ms: number | undefined, now: number) {
  if (!ms) return "";
  const s = Math.max(0, Math.round((now - ms) / 1000));
  if (s < 45) return "刚刚";
  const m = Math.round(s / 60);
  if (m < 60) return `${m}m`;
  const h = Math.round(m / 60);
  if (h < 24) return `${h}h`;
  return `${Math.round(h / 24)}d`;
}

/**
 * The panel itself, as the content of the phone bar's popover (PhoneBar.tsx owns the Popover and anchors it on
 * the bar): the same component as the inbox, so it gets the same open / close animation, Esc and tap-outside handling.
 * Body sits inside PopoverContent so it is remounted on every open (filter / workspace start fresh).
 */
export function PaneSwitcher() {
  return (
    <PopoverContent
      align="center"
      sideOffset={6}
      collisionPadding={8}
      // Touch: don't focus the search box on open, it would push the keyboard up (the rows are for thumbs, not typing)
      onOpenAutoFocus={(e) => isCoarse() && e.preventDefault()}
      className="flex max-h-[calc(var(--vvh,100vh)-64px)] w-[min(480px,calc(100vw-16px))] flex-col gap-0 overflow-hidden rounded-xl p-0 shadow-[0_24px_60px_-16px_rgba(0,0,0,.45)]"
    >
      <Body />
    </PopoverContent>
  );
}

function Body() {
  const layout = useStore((s) => s.layout);
  const sessions = useStore((s) => s.sessions);
  const unread = useStore((s) => s.unread);
  useStore((s) => s.addonVersion);
  const close = () => set({ switcher: false });

  const [q, setQ] = useState("");
  /** 只看这个工作空间（空 = 全部）。不存盘：它是「我这会儿在找什么」，不是偏好。 */
  // Opens on the current workspace (asked for): mostly you're switching panes within the project you're in, and
  // "全部" is one tap away. The panel is remounted on every open, so this is read fresh each time.
  const [ws, setWs] = useState(() => layout.active ?? "");
  const [adding, setAdding] = useState(false);

  // 「3m」那一列的 now：最小刻度是分钟，15 秒推一次足够。状态本身是 WebSocket 实时推的，不用轮询。
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    const t = setInterval(() => setNow(Date.now()), 15000);
    return () => clearInterval(t);
  }, []);

  const focusedPane = useMemo(() => {
    const w = layout.workspaces.find((x) => x.id === layout.active);
    return w?.tabs.find((t) => t.id === w.activeTab)?.focus;
  }, [layout]);

  const rows = useMemo(() => {
    // Only the session being shown (herdr-style isolation; switching sessions is in the sidebar)
    const space = currentSpace(layout);
    const mine = spaceWorkspaces(layout, space);
    const out: Row[] = allLeaves(layout)
      .filter((x) => spaceOf(x.ws) === space)
      .map(({ ws: w, tab, pane }, i) => {
        const wi = mine.indexOf(w);
        const sid = pane.sub?.sessionId ?? pane.sessionId;
        const ls = leaves(tab.root);
        const at = ls.length > 1 ? `${ls.indexOf(pane) + 1}/${ls.length}` : "";
        return {
          pane,
          session: sid ? sessions[sid] : undefined,
          ws: applyWorkspaceLabel(w, wi, w.name),
          wsId: w.id,
          tab: tabName(tab),
          at,
          i,
        };
      });
    const kw = q.trim().toLowerCase();
    const hit = (r: Row) =>
      (!ws || r.wsId === ws) &&
      (!kw ||
        `${r.session?.title ?? ""} ${r.session?.agent ?? ""} ${r.session?.cwd ?? ""} ${r.ws} ${r.tab} ${r.pane.sub?.title ?? ""} ${r.pane.term ? "终端 " + r.pane.term.cwd : ""} ${r.pane.file?.path ?? ""}`
          .toLowerCase()
          .includes(kw));
    return out
      .filter(hit)
      .sort((a, b) => rankOf(a, unread) - rankOf(b, unread) || (a.session && b.session ? compareSessions(a.session, b.session, unread) : 0) || a.i - b.i);
  }, [layout, sessions, unread, q, ws]);

  const go = (paneId: string) => {
    focusPane(paneId);
    close();
  };

  /**
   * 点一行**一下就跳过去**，所以收工在 `pointerup` 上，不在 `click` 上（herdr-web 实测出来的）：
   * 软键盘弹着的时候点这一行，同一下会先把键盘收掉 → 可视区域一变（--vvh）整个面板重排，手指底下那一行已经挪了位置，
   * 浏览器于是把 click 派给别人或者干脆不派，表现就是「第一下只收键盘，第二下才跳」。
   * touch 的 pointer 事件有隐式捕获：pointerdown 落在哪一行，pointerup 就还在那一行。鼠标照旧走 click。
   * 落下那一刻就记住 pane id：列表会在手底下实时重排（状态是推过来的），抬手时同一个 DOM 上可能已经是别的 pane。
   */
  const down = useRef<{ x: number; y: number; id: string } | null>(null);
  const took = useRef(0);
  const tap = (id: string) => ({
    onPointerDown: (e: React.PointerEvent) => {
      down.current = e.pointerType === "mouse" ? null : { x: e.clientX, y: e.clientY, id };
    },
    onPointerCancel: () => {
      down.current = null;
    },
    onPointerUp: (e: React.PointerEvent) => {
      const d = down.current;
      down.current = null;
      // 手指走了超过 10px 当成在滚列表（pointercancel 不是每次都来）
      if (!d || Math.abs(e.clientX - d.x) > 10 || Math.abs(e.clientY - d.y) > 10) return;
      took.current = e.timeStamp;
      go(d.id);
    },
    onClick: (e: React.MouseEvent) => {
      if (e.timeStamp - took.current < 800) return; // pointerup 刚接走过
      go(id);
    },
  });

  return (
    <>
      {/* Search, "new" and the workspace chips are one header block: no lines between them, one line under it */}
      <div className="shrink-0 border-b">
        <div className="flex items-center gap-1 p-2 pb-1.5">
          <div className="relative min-w-0 flex-1">
            <Search className="pointer-events-none absolute top-1/2 left-2.5 size-3.5 -translate-y-1/2 text-muted-foreground" />
            {/* 13px matches the list; iOS only gets 16px: a smaller input makes iOS zoom the page on focus */}
            <input
              className="h-8 w-full rounded-lg bg-muted/60 pr-2 pl-8 text-[13px] outline-none placeholder:text-muted-foreground/70 focus-visible:ring-2 focus-visible:ring-ring/40 supports-[-webkit-touch-callout:none]:text-[16px]"
              placeholder="筛 标题 / Tab / 路径"
              value={q}
              autoFocus={!isCoarse()}
              onChange={(e) => setQ(e.target.value)}
            />
          </div>
          {/* 「新开」挨着关闭键放，不跟工作空间那一行混：那一行是「选」，这个是「建」 */}
          <Button size="icon" variant={adding ? "secondary" : "ghost"} className="size-8" aria-label="新开" onClick={() => setAdding((v) => !v)}>
            <Plus className="size-4" />
          </Button>
          <Button size="icon" variant="ghost" className="size-8" aria-label="关闭" onClick={close}>
            <X className="size-4" />
          </Button>
        </div>

        {adding && (
          <div className="flex items-center gap-1.5 px-2 pb-1.5 text-[12px]">
            <span className="text-muted-foreground">新开</span>
            <Button
              size="sm"
              variant="outline"
              className="h-7 text-[12px]"
              onClick={() => {
                // tab 建在正在筛的那个工作空间里（没筛就是当前那个）
                addTab(ws ? { workspaceId: ws } : undefined);
                close();
              }}
            >
              新 Tab
            </Button>
            <Button size="sm" variant="outline" className="h-7 text-[12px]" onClick={() => set({ switcher: false, dialog: { kind: "newWorkspace" } })}>
              新工作空间
            </Button>
          </div>
        )}

        {/*
          工作空间那一行：「我要去另一个项目」在手机上最短的路。点一个 = 切过去 + 只看它的 pane ——
          切过去之后多半还要在它里面挑一个，两件事本来就是一起要的。横滑，不换行（换行会吃掉好几行列表）。
        */}
        {spaceWorkspaces(layout).length > 1 && (
          <div className="flex items-center gap-1 overflow-x-auto px-2 pb-2 [scrollbar-width:none] [&::-webkit-scrollbar]:hidden">
            <Chip on={!ws} onClick={() => setWs("")}>
              全部
            </Chip>
            {/* 当前工作空间排第一个（紧跟「全部」）：一排横滑，按原顺序排的话它常在屏幕外面，最常点的反而要先划过去找 */}
            {spaceWorkspaces(layout)
              .map((w, wi) => ({ w, wi }))
              .sort((a, b) => +(b.w.id === layout.active) - +(a.w.id === layout.active))
              .map(({ w, wi }) => (
                <Chip
                  key={w.id}
                  on={ws === w.id}
                  onClick={() => {
                    setWs(w.id);
                    selectWorkspace(w.id);
                  }}
                >
                  <WsDot w={w} sessions={sessions} unread={unread} hideIdle />
                  <span className="max-w-28 truncate">{applyWorkspaceLabel(w, wi, w.name)}</span>
                  {w.id === layout.active && <span className="text-[10px] text-brand">当前</span>}
                </Chip>
              ))}
          </div>
        )}
      </div>

      <div className="min-h-0 overflow-x-hidden overflow-y-auto overscroll-contain p-1.5">
        {rows.length === 0 && <p className="px-1 py-6 text-center text-[12px] text-muted-foreground">没有匹配的 Pane</p>}
        <div className="flex flex-col gap-0.5">
          {rows.map((r) => {
            const s = r.session;
            const chip = CHIP[rankOf(r, unread)];
            const title = r.pane.sub
              ? `子 Agent · ${r.pane.sub.title || ""}`.replace(/ · $/, "")
              : s
                ? s.title || basename(s.cwd)
                : r.pane.term
                  ? `终端 · ${basename(r.pane.term.cwd ?? "")}`
                  : r.pane.file
                    ? `文件 · ${basename(r.pane.file.path)}`
                    : "新会话";
            // 第二行：在哪（工作空间 · tab）+ 路径。手机上按工作空间认项目，路径兜底分开同名的
            // An unnamed tab is named after its first agent's title: on that agent's row the tab part only repeats the
            // title, so it is left out (same as the sidebar's agent list)
            const where = [r.ws, r.tab === title ? "" : r.tab, r.at].filter(Boolean).join(" · ");
            return (
              <button
                key={r.pane.id}
                type="button"
                className={cn(
                  // 一行至少 44px：手机上手指的下限；整块都是热区
                  // The selected row uses the same fill as the sidebar's active workspace / tab (bg-accent), so the two read
                  // as one highlight (asked for: a darker foreground overlay here looked like a different thing)
                  "flex min-h-11 w-full items-center gap-2.5 rounded-lg px-2 py-1.5 text-left leading-tight outline-none",
                  "hover:bg-accent/60 focus-visible:ring-2 focus-visible:ring-ring/40",
                  r.pane.id === focusedPane && "bg-accent [&_.title]:font-medium",
                )}
                {...tap(r.pane.id)}
              >
                {/* 不画打头的 agent 图标：几乎全是同一个 C，占一列宽却分不出谁是谁；终端 / 子 agent 标题里已经写着 */}
                <span className="min-w-0 flex-1">
                  <span className="flex items-center gap-1.5">
                    <span className="title truncate text-[12px]">{title}</span>
                    {chip && <span className={cn("shrink-0 rounded-sm border px-1 py-px text-[10px]", chip.cls)}>{chip.text}</span>}
                    <span className="ml-auto shrink-0 text-[10px] text-muted-foreground tabular-nums" title={s ? new Date(s.updatedAt).toLocaleString() : ""}>
                      {ago(s?.updatedAt, now)}
                    </span>
                  </span>
                  <span className="mt-0.5 flex items-center gap-1.5 text-[11px] text-muted-foreground">
                    {/* Both halves can shrink: a long tab title (often a whole sentence) used to be shrink-0 and pushed the row
                              past the screen edge on a phone. The path gives way first (shrink-[4]), then the title */}
                    {where && <span className="min-w-0 truncate">{where}</span>}
                    {(s || r.pane.term) && <span className="min-w-0 shrink-[4] truncate font-mono">{shortCwd(s?.cwd ?? r.pane.term?.cwd ?? "")}</span>}
                  </span>
                </span>
                <StatusDot status={dotStatus(s)} unread={!!(s && unread[s.id])} />
              </button>
            );
          })}
        </div>
      </div>
    </>
  );
}

function Chip({ on, onClick, children }: { on: boolean; onClick: () => void; children: React.ReactNode }) {
  return (
    <button
      type="button"
      onClick={onClick}
      className={cn(
        // No outline: a row of bordered pills on top of the bordered search box read as a grid of boxes. Only the selected
        // one is filled, with the same fill as the selected row below and the sidebar's active workspace (bg-accent)
        "flex h-7 shrink-0 items-center gap-1.5 rounded-md px-2.5 text-[12px]",
        on ? "bg-accent font-medium text-foreground" : "text-muted-foreground hover:bg-accent/60",
      )}
    >
      {children}
    </button>
  );
}
