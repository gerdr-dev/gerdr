import { Plus, Search, X } from "lucide-react";
import { useEffect, useMemo, useRef, useState } from "react";
import { Button } from "@/components/ui/button";
import { addTab, basename, focusPane, selectWorkspace, tabName } from "@/lib/actions";
import { allLeaves, leaves, type PaneLeaf } from "@/lib/layout";
import { isCoarse } from "@/lib/phone";
import { applyWorkspaceLabel } from "@/lib/plugins";
import { set, useStore } from "@/lib/store";
import type { SessionSummary } from "@/lib/types";
import { cn } from "@/lib/utils";
import { StatusDot } from "./AgentIcon";
import { compareSessions, sessionRank } from "@/lib/order";
import { WsDot } from "./Chrome";

/**
 * 面板一览：**手机上换 pane 的那条路**（照 herdr-web 的 PaneSwitcher）。
 *
 * 手机上一次只看得见一个 pane（多 pane 平铺读不了），tab 栏也收掉了，于是「另一个工作空间里那个在等我的 agent」
 * 得有地方一眼找到、点一下就过去：这里把**所有工作空间的所有 pane** 拍平成一张表，按「多想让你看一眼」排。
 *
 * 它是**索引，不是第二个管理界面**：只有「去哪个」和「新开一个」，关 / 改名 / 分屏还在 pane 标题栏的菜单里。
 */
const LS_SORT = "gerdr.panesSort";

/** tab 带序号、分屏的 pane 带「第几个」：同一个 tab 里两个空 pane、或两个同名 tab，只有这两样分得开。 */
type Row = { pane: PaneLeaf; session?: SessionSummary; ws: string; wsId: string; tab: string; i: number };

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

export function PaneSwitcher() {
  const layout = useStore((s) => s.layout);
  const sessions = useStore((s) => s.sessions);
  const unread = useStore((s) => s.unread);
  useStore((s) => s.pluginVersion);
  const close = () => set({ switcher: false });

  const [q, setQ] = useState("");
  /** 只看这个工作空间（空 = 全部）。不存盘：它是「我这会儿在找什么」，不是偏好。 */
  const [ws, setWs] = useState("");
  const [adding, setAdding] = useState(false);
  const [sort, setSort] = useState<"rank" | "group">(() => {
    try {
      return localStorage.getItem(LS_SORT) === "group" ? "group" : "rank";
    } catch {
      return "rank";
    }
  });
  const flipSort = () => {
    const v = sort === "rank" ? "group" : "rank";
    setSort(v);
    try {
      localStorage.setItem(LS_SORT, v);
    } catch {}
  };

  // 「3m」那一列的 now：最小刻度是分钟，15 秒推一次足够。状态本身是 WebSocket 实时推的，不用轮询。
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    const t = setInterval(() => setNow(Date.now()), 15000);
    return () => clearInterval(t);
  }, []);

  useEffect(() => {
    const k = (e: KeyboardEvent) => e.key === "Escape" && close();
    window.addEventListener("keydown", k);
    return () => window.removeEventListener("keydown", k);
  }, []);

  const focusedPane = useMemo(() => {
    const w = layout.workspaces.find((x) => x.id === layout.active);
    return w?.tabs.find((t) => t.id === w.activeTab)?.focus;
  }, [layout]);

  const rows = useMemo(() => {
    const out: Row[] = allLeaves(layout).map(({ ws: w, tab, pane }, i) => {
      const wi = layout.workspaces.indexOf(w);
      const sid = pane.sub?.sessionId ?? pane.sessionId;
      const ls = leaves(tab.root);
      const at = ls.length > 1 ? ` · ${ls.indexOf(pane) + 1}/${ls.length}` : "";
      return { pane, session: sid ? sessions[sid] : undefined, ws: applyWorkspaceLabel(w, wi, w.name), wsId: w.id, tab: `${w.tabs.indexOf(tab) + 1}.${tabName(tab)}${at}`, i };
    });
    const kw = q.trim().toLowerCase();
    const hit = (r: Row) =>
      (!ws || r.wsId === ws) &&
      (!kw || `${r.session?.title ?? ""} ${r.session?.agent ?? ""} ${r.session?.cwd ?? ""} ${r.ws} ${r.tab} ${r.pane.sub?.title ?? ""} ${r.pane.term ? "终端 " + r.pane.term.cwd : ""}`.toLowerCase().includes(kw));
    const list = out.filter(hit);
    if (sort === "group") return list;
    return list.sort((a, b) => rankOf(a, unread) - rankOf(b, unread) || (a.session && b.session ? compareSessions(a.session, b.session, unread) : 0) || a.i - b.i);
  }, [layout, sessions, unread, q, ws, sort]);

  // 「分组」按工作空间分组（和侧栏里看到的一样）；优先级是全局排序，分组会把它切碎，所以那边是一条平铺的表。
  const groups = useMemo(() => {
    if (sort !== "group") return [{ ws: "", items: rows }];
    const out: { ws: string; items: Row[] }[] = [];
    for (const r of rows) {
      const g = out.find((x) => x.ws === r.wsId);
      if (g) g.items.push(r);
      else out.push({ ws: r.wsId, items: [r] });
    }
    // 当前工作空间那组放最上面（和上面那排标签一个顺序）
    return out.sort((a, b) => +(b.ws === layout.active) - +(a.ws === layout.active));
  }, [rows, sort, layout.active]);

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
      {/* 点面板外面就关：手机上没有 Esc，右上角那个 × 又小 */}
      <div className="absolute inset-0 z-20 bg-background/40" onClick={close} />
      <aside className="absolute inset-x-2 top-1.5 z-20 flex max-h-[calc(100%-12px)] flex-col overflow-hidden rounded-xl border bg-popover text-popover-foreground shadow-[0_24px_60px_-16px_rgba(0,0,0,.45)] md:right-2.5 md:left-auto md:w-[480px]">
        <div className="flex shrink-0 items-center gap-1.5 border-b p-2">
          <div className="relative min-w-0 flex-1">
            <Search className="pointer-events-none absolute top-1/2 left-2 size-3.5 -translate-y-1/2 text-muted-foreground" />
            {/* 13px 和列表一个调子；只有 iOS 留 16px：小于 16px 的输入框一聚焦，iOS 会把整页放大 */}
            {/* 触屏上不自动聚焦：一开面板就顶出键盘，而这儿多半是用手指扫，不是打字 */}
            <input
              className="h-8 w-full rounded-md border bg-transparent pr-2 pl-7 text-[13px] outline-none placeholder:text-muted-foreground/70 focus-visible:ring-2 focus-visible:ring-ring/40 supports-[-webkit-touch-callout:none]:text-[16px]"
              placeholder="筛 标题 / tab / 路径"
              value={q}
              autoFocus={!isCoarse()}
              onChange={(e) => setQ(e.target.value)}
            />
          </div>
          <Button size="sm" variant={sort === "group" ? "secondary" : "ghost"} className="h-8 px-2 text-[12px]" title="按优先级 / 按工作空间分组" onClick={flipSort}>
            {sort === "group" ? "分组" : "优先级"}
          </Button>
          {/* 「新开」挨着关闭键放，不跟工作空间那一行混：那一行是「选」，这个是「建」 */}
          <Button size="icon" variant={adding ? "secondary" : "ghost"} className="size-8" aria-label="新开" onClick={() => setAdding((v) => !v)}>
            <Plus className="size-4" />
          </Button>
          <Button size="icon" variant="ghost" className="size-8" aria-label="关闭" onClick={close}>
            <X className="size-4" />
          </Button>
        </div>

        {adding && (
          <div className="flex shrink-0 items-center gap-1.5 border-b px-2 py-1.5 text-[12px]">
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
              新 tab
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
        {layout.workspaces.length > 1 && (
          <div className="flex shrink-0 items-center gap-1.5 overflow-x-auto border-b px-2 py-1.5 [scrollbar-width:none] [&::-webkit-scrollbar]:hidden">
            <Chip on={!ws} onClick={() => setWs("")}>
              全部
            </Chip>
            {/* 当前工作空间排第一个（紧跟「全部」）：一排横滑，按原顺序排的话它常在屏幕外面，最常点的反而要先划过去找 */}
            {layout.workspaces
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

        <div className="min-h-0 overflow-y-auto overscroll-contain p-1.5">
          {rows.length === 0 && <p className="px-1 py-6 text-center text-[12px] text-muted-foreground">没有匹配的 pane</p>}
          {groups.map((g) => (
            <section key={g.ws || "all"} className="mb-1">
              {g.ws && <h4 className="px-2 pt-1 pb-0.5 text-[11px] font-medium text-muted-foreground">{g.items[0]?.ws}</h4>}
              <div className="flex flex-col gap-0.5">
                {g.items.map((r) => {
                  const s = r.session;
                  const chip = CHIP[rankOf(r, unread)];
                  const title = r.pane.sub ? `子 agent · ${r.pane.sub.title || ""}`.replace(/ · $/, "") : s ? s.title || basename(s.cwd) : r.pane.term ? `终端 · ${basename(r.pane.term.cwd ?? "")}` : "新会话";
                  // 第二行：在哪（工作空间 · tab）+ 路径。手机上按工作空间认项目，路径兜底分开同名的
                  const where = sort === "group" ? r.tab : `${r.ws} · ${r.tab}`;
                  return (
                    <button
                      key={r.pane.id}
                      type="button"
                      className={cn(
                        // 一行至少 44px：手机上手指的下限；整块都是热区
                        // 选中的和侧栏一样用灰底、不用品牌色描边。但面板底（popover）比侧栏底亮，bg-accent 在这上面差得太少、
                        // 看不出来（用户报的）：用前景色叠一层，深浅两种主题都拉得开
                        "flex min-h-11 w-full items-center gap-2.5 rounded-lg px-2 py-1.5 text-left leading-tight outline-none",
                        "hover:bg-foreground/5 focus-visible:ring-2 focus-visible:ring-ring/40",
                        r.pane.id === focusedPane && "bg-foreground/12 [&_.title]:font-medium",
                      )}
                      {...tap(r.pane.id)}
                    >
                      {/* 不画打头的 agent 图标：几乎全是同一个 C，占一列宽却分不出谁是谁；终端 / 子 agent 标题里已经写着 */}
                      <span className="min-w-0 flex-1">
                        <span className="flex items-center gap-1.5">
                          <span className="title truncate text-[12px]">{title}</span>
                          {chip && <span className={cn("shrink-0 rounded-sm border px-1 py-px text-[10px]", chip.cls)}>{chip.text}</span>}
                          <span className="ml-auto shrink-0 font-mono text-[10px] text-muted-foreground" title={s ? new Date(s.updatedAt).toLocaleString() : ""}>
                            {ago(s?.updatedAt, now)}
                          </span>
                        </span>
                        <span className="mt-0.5 flex items-center gap-1.5 text-[11px] text-muted-foreground">
                          <span className="shrink-0">{where}</span>
                          {(s || r.pane.term) && <span className="min-w-0 truncate font-mono">{shortCwd(s?.cwd ?? r.pane.term?.cwd ?? "")}</span>}
                        </span>
                      </span>
                      <StatusDot status={s?.status} unread={!!(s && unread[s.id])} />
                    </button>
                  );
                })}
              </div>
            </section>
          ))}
        </div>
      </aside>
    </>
  );
}

function Chip({ on, onClick, children }: { on: boolean; onClick: () => void; children: React.ReactNode }) {
  return (
    <button
      type="button"
      onClick={onClick}
      className={cn("flex h-7 shrink-0 items-center gap-1.5 rounded-md border px-2 text-[12px]", on ? "border-transparent bg-foreground/12 font-medium text-foreground" : "text-muted-foreground hover:bg-foreground/5")}
    >
      {children}
    </button>
  );
}
