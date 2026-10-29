import { useContext, useEffect, useLayoutEffect, useMemo, useRef, useState } from "react";
import { TabActive } from "../Panes";
import { Virtuoso, type VirtuosoHandle } from "react-virtuoso";
import { ArrowDown, CornerDownRight } from "lucide-react";
import { Button } from "@/components/ui/button";
import { buildRows, RowView, type Row, type RowEnv } from "./Rows";
import { toast } from "sonner";
import { api } from "@/lib/api";
import { withdrawSteer } from "@/lib/actions";
import { get, loadOlder, sendShown, subTranscript, transcript, useStore } from "@/lib/store";
import { useKept } from "@/lib/kept";
import { Loader2 } from "lucide-react";
import type { GMessage } from "@/lib/transcript";
import type { SessionSummary } from "@/lib/types";
import { Composer, type ComposerHandle } from "./Composer";
import { SessionContext } from "./sessionContext";
import { askKey } from "@/lib/askkeys";
import { isCoarse } from "@/lib/phone";

/** sub：子 agent 的只读面板（值 = 它在主会话里那张卡片的 toolCallId）。没有输入框、不能回退。 */
// 最后一条和输入框之间留一截（照 ChatGPT 桌面版的留白）：贴太近的话最后那行元信息和输入框挤在一起。
// 手机上只留一点：屏幕本来就矮，一截空白占掉两三行字（用户报的）
const ListFooter = () => <div className="h-12 max-md:h-5" />;

// 顶上一行：前面还有更早的消息（打开会话只拉了最近一段，翻到这儿自动接着拉）。高度固定，拉完了也留着空白 ——
// 顶上的东西一变高矮，下面整段内容就跟着跳。
const ListHeader = ({ context }: { context?: { older?: "more" | "loading" } }) => (
  <div className="flex h-8 items-center justify-center text-[12px] text-muted-foreground">
    {context?.older === "loading" && <Loader2 className="size-3.5 animate-spin" />}
    {context?.older === "more" && "往上翻加载更早的消息"}
  </div>
);

/**
 * 贴着底时，不让 Virtuoso 往上「补偿」。一行的顶已经滚出视野上沿、这时把它收起来（展开的长工具输出），
 * Virtuoso 当它是「视野上面的行变矮了」，scrollBy 负的那么多，想让下面的内容不动 —— 可浏览器已经把 scrollTop 夹回底部了，
 * 再往上推就离开了底部：收起之后反而不贴底、冒出「回到底部」（用户报的）。
 * 只在调用那一刻确实在底部时才吞：人往上翻时位置早已不在底部，那时的补偿照旧（不然翻的时候内容会跳）。
 */
function keepBottomOnShrink(el: HTMLElement, bottomRef: { current: boolean }) {
  if ((el as any).__keepBottom) return;
  (el as any).__keepBottom = true;
  const scrollBy = el.scrollBy.bind(el) as (o: ScrollToOptions) => void;
  el.scrollBy = ((o: ScrollToOptions) => {
    const atBottom = el.scrollHeight - el.clientHeight - el.scrollTop < 80;
    if (typeof o === "object" && (o.top ?? 0) < 0 && atBottom && bottomRef.current) return;
    scrollBy(o);
  }) as typeof el.scrollBy;
}

// 顶上钉着的那句用户消息：一行，太长截掉；图片 / 文件写成 [图片] 这种占位
function PinnedPrompt({ msg, onClick }: { msg: GMessage; onClick: () => void }) {
  const text = msg.parts
    .map((p) => (p.type === "text" ? p.text : p.type === "file" ? (p.mediaType?.startsWith("image/") ? "[图片]" : "[文件]") : ""))
    .join(" ")
    .replace(/\s+/g, " ")
    .trim();
  return (
    <div className="pointer-events-none absolute inset-x-0 top-0 z-10 px-4 pt-2">
      <button
        className="pointer-events-auto mx-auto flex w-full max-w-[800px] items-center gap-2 rounded-md border bg-background/95 px-3 py-1.5 text-left text-[13px] text-muted-foreground shadow-sm backdrop-blur hover:text-foreground"
        onClick={onClick}
        title="跳到这句"
      >
        <CornerDownRight className="size-3.5 shrink-0" />
        <span className="truncate">{text || "（空消息）"}</span>
      </button>
    </div>
  );
}

// 每个 pane 的滚动位置（在不在底部、滚到哪）：列表被卸载再挂回来（tab 被挤出保留数、虚拟列表重建）时照它恢复。
const scrollMem = new Map<string, { top: number; bottom: boolean }>();

/**
 * 人最近一次在这个对话的滚动区里动手（滚轮、触摸、按键、按下 / 按住拖滚动条）的时刻：分清滚动是人滚的还是布局变了带的（见 syncBottom）。
 * 只认滚动区里面的：切 tab 本身就是一次点击 / 按键，紧跟着就是重新挂载、边挂边量 —— 全局算的话正好把这段放过去了。
 * 触控板惯性也是一串 wheel，一直在续。
 */
function lastInputOf(el: HTMLElement): number {
  const s = el as HTMLElement & { __lastInput?: number };
  if (s.__lastInput === undefined) {
    s.__lastInput = 0;
    const mark = () => (s.__lastInput = performance.now());
    for (const ev of ["wheel", "touchstart", "touchmove", "keydown", "pointerdown"]) el.addEventListener(ev, mark, { passive: true });
    el.addEventListener("pointermove", (e) => e.buttons && mark(), { passive: true });
  }
  return s.__lastInput;
}

/** A scroll the user asked for from outside the scroller (the pinned prompt): count it as input so syncBottom lets it leave the bottom. */
function markInput(el: HTMLElement | null) {
  if (!el) return;
  lastInputOf(el);
  (el as HTMLElement & { __lastInput?: number }).__lastInput = performance.now();
}

export function ChatView({ session, paneId, focused, sub }: { session: SessionSummary; paneId: string; focused: boolean; sub?: string }) {
  useStore((s) => s.tv[session.id]); // 日志变了就重画
  const t = sub ? subTranscript(session.id, sub) : transcript(session.id);
  const subTask = sub ? session.tasks?.find((x) => x.toolCallId === sub) : undefined;
  const running = sub ? subTask?.status === "running" : t.running;
  const loaded = useStore((s) => s.tv[session.id] !== undefined);
  const agentName = useStore((s) => s.agents.find((a) => a.id === session.agent)?.name) ?? session.agent;
  const composer = useRef<ComposerHandle>(null);

  // 焦点切到这个 pane（或者前缀动作做完）时，把光标放回输入框。
  // 触屏上不做：聚焦输入框就弹软键盘，切个面板键盘就盖住半屏。手机上只有点了输入框本身才弹（和 Terminal 一样）。
  useEffect(() => {
    if (!focused || sub || isCoarse()) return;
    const f = () => {
      if (!get().dialog && !document.querySelector("[role=dialog]")) composer.current?.focus();
    };
    f();
    window.addEventListener("gerdr:refocus", f);
    return () => window.removeEventListener("gerdr:refocus", f);
  }, [focused]);

  // 焦点不在任何输入框里（刚点过对话区、按过 Esc……）时直接敲字：字进这个 pane 的输入框，不用先去点它。
  // 只认能打出字的单个键（没带 ⌘ / Ctrl / Alt）；快捷键、方向键、翻页照旧归页面（滚对话、切 pane）。
  useEffect(() => {
    if (!focused || sub) return;
    const onKey = (e: KeyboardEvent) => {
      if (e.defaultPrevented || e.isComposing) return;
      const a = document.activeElement as HTMLElement | null;
      if (a && (a.isContentEditable || /^(INPUT|TEXTAREA|SELECT)$/.test(a.tagName))) return;
      if (get().dialog || document.querySelector("[role=dialog]")) return;
      // Ctrl+C 不管焦点在哪都算数（在对话区选着字、点过别处）：和输入框里按一样，清空 / 打断 / 连按两次退出。
      // mac 上复制是 ⌘C，不冲突；Windows / Linux 上 Ctrl+C 就是复制，选着字时让给复制
      const copying = !/Mac/.test(navigator.platform) && window.getSelection()?.isCollapsed === false;
      if (e.key === "c" && e.ctrlKey && !e.metaKey && !e.altKey && !e.shiftKey && !copying) {
        e.preventDefault();
        composer.current?.ctrlC();
        return;
      }
      if (e.metaKey || e.ctrlKey || e.altKey) return;
      // 有问题在等回答：↑↓ / 数字 / 回车先给提问卡（和输入框里一样，见 lib/askkeys）
      if (askKey(session.id, e)) {
        e.preventDefault();
        return;
      }
      if (e.key.length !== 1) return;
      composer.current?.focus(); // 在 keydown 里换焦点，这个键的字就落到新焦点上
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [focused, sub, session.id]);

  // 点对话区任何地方（不是在选字、也不是点按钮 / 链接这些）：光标回到输入框，直接打字（用户点名的）
  // 触屏不做：手指点对话区多半是想滚动 / 看内容，一点就弹键盘很烦。
  const clickToType = (e: React.MouseEvent) => {
    if (sub || e.button !== 0 || isCoarse()) return;
    const t = e.target as HTMLElement;
    if (
      t.closest(
        "button,a,input,textarea,select,summary,label,[role=button],[role=menuitem],[role=checkbox],[role=radio],[contenteditable=true],[data-keep-focus]",
      )
    )
      return;
    const sel = window.getSelection();
    if (sel && !sel.isCollapsed) return; // 在选字（拖选、双击选词）—— 抢走焦点会让人复制不了
    composer.current?.focus();
  };

  // 回退：确认在消息旁边的小气泡里做（RewindButton），这里只管真的去回退。
  // 原话总是放回输入框：想原样重发直接点发送，想改就改，不要了清掉 —— 所以不再单独留一个「改这句重发」
  const doRewind = async (msg: GMessage) => {
    try {
      const r = await api.rewind(session.id, msg.metadata!.messageId!);
      composer.current?.setContent(r.content ?? []);
    } catch (e: any) {
      toast.error(e.message ?? String(e));
    }
  };

  const msgs = t.all;
  const virtualList = useStore((s) => !!s.config.virtualList);
  const showThinking = useStore((s) => s.config.showThinking !== false);
  // 虚拟滚动开着时，打开会话先把所有行渲染一次、量出真实高度，再缩回只渲染屏幕附近（Virtuoso 记得量过的高度，行卸载了也不忘）。
  // 不先量的话，往上翻时新进渲染范围的行只能按估的高度占位，实际一挂上高度对不上，整段内容被推走一截，Virtuoso 下一帧才补回来 ——
  // 一翻一闪（用户报的；录屏逐帧比过：小步连续滚 300 下，45 处 -223 / +223 来回跳）。在补偿上打补丁试过几种（浏览器原生锚定、
  // 自己在 ResizeObserver 里抢先补），都会在别处跳得更厉害。代价是打开时一次性全量渲染，量完就释放。
  const [measured, setMeasured] = useState(false);
  const measureTimer = useRef(0);
  // 量完 = 总高度连续 300ms 不再变（totalListHeightChanged 里续），最长等 2 秒
  useEffect(() => {
    if (!virtualList || measured) return;
    const t = setTimeout(() => setMeasured(true), 2000);
    return () => clearTimeout(t);
  }, [virtualList, measured]);
  useEffect(() => () => clearTimeout(measureTimer.current), []);
  const [showCleared, setShowCleared] = useKept(`cleared:${session.id}:${sub ?? ""}`, false);
  const [openGroups, setOpenGroups] = useKept<Record<string, boolean>>(`groups:${session.id}:${sub ?? ""}`, {});
  // 还在跑的后台任务：拼成串当依赖，任务进度一直在推，不能每推一次就重排整份对话
  const liveTaskIds = (session.tasks ?? [])
    .filter((t) => t.status === "running" && t.toolCallId)
    .map((t) => t.toolCallId)
    .join(" ");
  // 跑得快的工具调用先不露（Rows 的 PEEK_DELAY）：buildRows 告诉我们什么时候有一条跑满了，到点 tick 一下重排
  const [tick, setTick] = useState(0);
  const [rows, wake] = useMemo(() => {
    const out: { wake?: number } = {};
    return [
      buildRows(msgs, running, showCleared, openGroups, new Set(liveTaskIds.split(" ").filter(Boolean)), Date.now(), out, showThinking),
      out.wake,
    ] as const;
  }, [msgs, running, showCleared, openGroups, liveTaskIds, tick, showThinking]);
  useEffect(() => {
    if (wake === undefined) return;
    const t = setTimeout(() => setTick((x) => x + 1), Math.max(0, wake - Date.now()));
    return () => clearTimeout(t);
  }, [wake]);
  // 往前补了更早的消息（loadOlder）：告诉 Virtuoso 前面多了几行（firstItemIndex 往小改），它保住眼前这行不动。
  // 不改的话它当成整份换了，滚动位置留在原来的像素高度上 —— 眼前的内容被新插进来的顶走。
  // 顶上是折起来的 /clear 之前的对话：更早的拉来也是折在里面，先不拉（点了「展开」再接着拉）
  const older = useStore((s) => (rows[0]?.kind === "cleared" ? undefined : s.older[session.id]));
  // 前面多了几行：拿原来的前几行去新的里找，位置差就是。不只认第一行 —— 接缝那儿的行可能变了（折叠组并进了新来的工具调用）
  const firstIdx = useRef(1_000_000);
  const prevRows = useRef<Row[]>([]);
  const prepended = useRef(0); // 刚往前补过：到这个时刻为止盯着眼前那一行（见下面 settle）
  if (rows !== prevRows.current) {
    const prev = prevRows.current;
    if (prev.length && rows.length > prev.length && prev[0].key !== rows[0].key) {
      const at = new Map(rows.map((r, i) => [r.key, i]));
      for (let j = 0; j < Math.min(prev.length, 50); j++) {
        const i = at.get(prev[j].key);
        if (i === undefined) continue;
        if (i > j) {
          firstIdx.current -= i - j;
          prepended.current = performance.now() + 1500;
        }
        break;
      }
    }
    prevRows.current = rows;
  }
  // 光改 firstItemIndex 不够：Virtuoso 只在插进来那一下按估的行高 scrollBy 一次，等新行挂上、量出真实高度（往往差几千像素），
  // 差的那截它不再补 —— scrollTop 不动、上面变高，眼前的内容被整段顶走（逐帧量过：补一段 scrollBy +5700，170ms 后总高又涨 6482，
  // scrollTop 没动）。所以补完这一小会儿自己盯着眼前顶上那一行：它在内容里的位置（offset）变了、scrollTop 没跟上的那截替它补上。
  // 人这时可能还在甩（惯性），不能把它钉死在屏幕上原来的位置 —— 只补「上面变高 / 变矮」的那部分。
  // 在哪儿补：ResizeObserver 里，而且这个 observer 比 Virtuoso 的晚建 —— 同一阶段按创建顺序跑，轮到它时 Virtuoso 该补的已经补完，
  // 这时的样子就是这一帧要画的。放在 rAF 里补晚了一帧（rAF 在 ResizeObserver 之前，新行是在那之后量出来的：错位先画出来一帧），
  // 而且会抢在 Virtuoso 前面，它本来会补的也被补了两遍。
  // 认行用 data-item-index（= firstItemIndex + 位置）：往前补时同一行的它不变。别用 data-index（在 rows 里的位置）配 rows ——
  // 换数据的那一两帧 DOM 上还是旧行、rows 已经是新的，一配就认错行（踩过：补反了，一下跳回几千像素之前）
  const settle = useRef<{ idx: string; o: number; st: number } | null>(null);
  const settleFix = useRef<(() => void) | null>(null);
  useLayoutEffect(() => {
    const el = scrollerEl.current;
    const lst = el?.querySelector("[data-testid=virtuoso-item-list]");
    if (!el || !lst || performance.now() > prepended.current) return;
    const offset = (it: HTMLElement) => it.getBoundingClientRect().top - el.getBoundingClientRect().top + el.scrollTop;
    const anchorOf = () => {
      const top = el.getBoundingClientRect().top;
      for (const it of el.querySelectorAll<HTMLElement>("[data-item-index]"))
        if (it.getBoundingClientRect().bottom > top + 1) return { idx: it.dataset.itemIndex!, o: offset(it), st: el.scrollTop };
      return null;
    };
    const fix = () => {
      if (performance.now() > prepended.current) return stop();
      const a = settle.current;
      const it = a && el.querySelector<HTMLElement>(`[data-item-index="${a.idx}"]`);
      if (a && it) {
        // 上面的行变高 / 变矮了 dO，scrollTop 同时动了 dSt（Virtuoso 自己补的 + 人滚的）：没补够的差 = dO - dSt
        const dO = offset(it) - a.o;
        const miss = dO - (el.scrollTop - a.st);
        if (Math.abs(dO) > 1 && Math.abs(miss) > 1) el.scrollTop += miss;
      }
      settle.current = anchorOf();
    };
    const ro = new ResizeObserver(fix);
    const stop = () => {
      ro.disconnect();
      settle.current = null;
      settleFix.current = null;
    };
    ro.observe(lst);
    settleFix.current = fix; // 人滚动（onScroll）时也过一遍：布局没变就只是记下新的位置
    if (!settle.current) settle.current = anchorOf();
    const t = setTimeout(stop, prepended.current - performance.now());
    return () => {
      clearTimeout(t);
      ro.disconnect();
    };
  }, [rows]);
  const listCtx = useMemo(() => ({ older }), [older]);
  // 离顶不到一屏 / 内容还不满一屏：接着拉更早的
  const nearTop = (el: HTMLElement) => {
    if (rowsRef.current[0]?.kind !== "cleared" && get().older[session.id] === "more" && el.scrollTop < Math.max(400, el.clientHeight)) loadOlder(session.id);
  };
  // 要重新量：行的结构变了（中间展开 / 收起一组、回退，后面的行全换了序号，Virtuoso 按序号记的高度就对不上了）。
  // 只在末尾追加 / 换掉最后一两行（流式输出、状态行）不算：那些行就在眼前，挂上就量
  const prevKeys = useRef<string[]>([]);
  useEffect(() => {
    const prev = prevKeys.current;
    const cur = rows.map((r) => r.key);
    prevKeys.current = cur;
    let k = 0;
    while (k < prev.length && k < cur.length && prev[k] === cur[k]) k++;
    if (prev.length && k < Math.min(prev.length, cur.length) - 2) setMeasured(false);
  }, [rows]);
  // 宽度变了（拖分屏、改窗口）：每一行的高度都变了，也重新量
  const widthRO = useRef<{ ro: ResizeObserver; el: HTMLElement; w: number } | null>(null);
  const watchWidth = (el: HTMLElement | null) => {
    if (widthRO.current?.el === el) return;
    widthRO.current?.ro.disconnect();
    widthRO.current = null;
    if (!el) return;
    const cur = {
      el,
      w: el.clientWidth,
      ro: new ResizeObserver(() => {
        if (Math.abs(el.clientWidth - cur.w) > 1) {
          cur.w = el.clientWidth;
          setMeasured(false);
        }
      }),
    };
    cur.ro.observe(el);
    widthRO.current = cur;
  };
  useEffect(() => () => widthRO.current?.ro.disconnect(), []);
  const list = useRef<VirtuosoHandle>(null);
  const scrollKey = `${paneId}:${session.id}:${sub ?? ""}`;
  const saved = scrollMem.get(scrollKey);
  const [atBottom, setAtBottom] = useState(saved ? saved.bottom : true);
  const bottomRef = useRef(saved ? saved.bottom : true);
  // tab 藏起来再显示：滚动位置自己恢复。WebKit（mac App 的 WKWebView）里 visibility:hidden 的滚动容器会被清零
  // （Chrome 不会）——实测切走一下再回来就在最顶上。所以只在显示着的时候记位置（藏着期间那次清零不记），显示时照记的恢复。
  const tabActive = useContext(TabActive);
  const activeRef = useRef(tabActive);
  activeRef.current = tabActive;
  useEffect(() => {
    if (!tabActive) return;
    const restore = () => {
      const m = scrollMem.get(scrollKey);
      const el = scrollerEl.current;
      if (!m || m.bottom) list.current?.scrollToIndex({ index: "LAST", align: "end" });
      else if (el && Math.abs(el.scrollTop - m.top) > 2) el.scrollTop = m.top;
    };
    restore();
    const t1 = requestAnimationFrame(restore);
    const t2 = setTimeout(restore, 120);
    return () => {
      cancelAnimationFrame(t1);
      clearTimeout(t2);
    };
  }, [tabActive]);
  const scrollerEl = useRef<HTMLElement | null>(null);
  // 顶上钉一条「这段回复是回哪句话的」（照 Claude Code）：屏幕最上面那一行往前找最近的用户消息，
  // 它已经滚出屏幕了就钉出来，点一下跳回去。贴着底也钉：原来只在往上翻时才出，回复一长、人停在底部看输出时
  // 那句话早滚没了却不显示，要往上翻一截离开底部才冒出来（用户报的）。行是虚拟列表的，只能从 DOM 上的 data-index 认出顶上是第几行。
  const [pinned, setPinned] = useState<number | null>(null);
  const pinRaf = useRef(0);
  const rowsRef = useRef(rows);
  rowsRef.current = rows;
  const updatePinned = () => {
    cancelAnimationFrame(pinRaf.current);
    pinRaf.current = requestAnimationFrame(() => {
      const el = scrollerEl.current;
      if (!el) return;
      const top = el.getBoundingClientRect().top;
      let topIdx = -1;
      for (const it of el.querySelectorAll<HTMLElement>("[data-index]")) {
        if (it.getBoundingClientRect().bottom > top + 1) {
          topIdx = Number(it.dataset.index);
          break;
        }
      }
      const rs = rowsRef.current;
      let u = -1;
      for (let i = Math.min(topIdx, rs.length - 1); i >= 0; i--)
        if (rs[i].kind === "user") {
          u = i;
          break;
        }
      setPinned(u >= 0 && u < topIdx ? u : null);
    });
  };
  useEffect(() => () => cancelAnimationFrame(pinRaf.current), []);
  const lastH = useRef(0); // 上一次量到的总高（推到底之前用它判断「刚才是不是贴着底」）
  // 人刚点了展开（工具调用、折叠组、思考）：这一小会儿内容变高不追到底，点的那一行留在原地，展开的东西往下长。
  // 不然贴着底时一展开就被推到最底下，刚点的那行顶出屏幕（用户报的）。收起变矮不受影响，照样贴底。
  const holdUntil = useRef(0);
  const hold = () => (holdUntil.current = performance.now() + 500);
  const holding = () => performance.now() < holdUntil.current;
  // 贴没贴底直接量 DOM，不信 Virtuoso 的 atBottom：贴着底时把上面一块展开的东西收起来，内容变矮、浏览器把 scrollTop
  // 往回夹，Virtuoso 拿旧的总高一算以为人往上翻了，报 false 之后就不再改回来 —— 明明在底部却冒出「回到底部」，
  // 新输出也不跟了（用户报的）。
  // 离开底部只认人：贴着底、这会儿人没动过（滚轮 / 触摸 / 按键 / 按住拖），scrollTop 却离了底 —— 那是布局变了把它带走的，推回去。
  // 不然一次性的布局变化就把「在底部」记成 false，之后再长高也不追了、还记进 scrollMem：
  // tab 不保留时切回来要重新挂、边挂边量高度，量的过程中被推离底部一截就停在半中间，下次切回来照记的位置恢复，还是半中间（用户报的）
  const syncBottom = () => {
    const el = scrollerEl.current;
    if (!el || !activeRef.current) return;
    const b = el.scrollHeight - el.clientHeight - el.scrollTop < 80;
    if (!b && bottomRef.current && performance.now() - lastInputOf(el) > 600) {
      el.scrollTop = el.scrollHeight;
      return;
    }
    bottomRef.current = b;
    setAtBottom(b);
  };
  // 行是 memo 的，回调要稳定（不然每次都重渲染），但又要拿到最新的 —— 放在 ref 里转一手
  const latest = useRef({ doRewind, sessionId: session.id, openGroups, setOpenGroups });
  latest.current = { doRewind, sessionId: session.id, openGroups, setOpenGroups };
  const steerRewind = (session.base ?? session.agent) === "claude";
  const env = useMemo<RowEnv>(
    () => ({
      sessionId: session.id,
      running,
      onRewind: sub ? undefined : (m) => latest.current.doRewind(m),
      steerRewind,
      onWithdraw: sub
        ? undefined
        : async (id, now) => {
            const c = await withdrawSteer(session.id, id, now);
            if (c?.length) composer.current?.putBack(c);
          },
      onQuickReply: (s) => sendShown(latest.current.sessionId, [{ type: "text", text: s }]).catch((e) => toast.error(e.message)),
      onShowCleared: () => setShowCleared(true),
      onToggleGroup: (key) => {
        const { openGroups: g, setOpenGroups: set } = latest.current;
        hold();
        set({ ...g, [key]: !g[key] });
      },
    }),
    [session.id, running, sub, steerRewind],
  );
  return (
    <SessionContext.Provider value={session}>
      <div
        className="flex h-full min-h-0 flex-col"
        onMouseUp={clickToType}
        onClickCapture={(e) => (e.target as HTMLElement).closest("[aria-expanded]") && hold()}
      >
        <div className="relative min-h-0 flex-1">
          {!loaded && <div className="py-10 text-center text-[13px] text-muted-foreground">加载中…</div>}
          {loaded && msgs.length === 0 && sub && <div className="py-16 text-center text-[13px] text-muted-foreground">子 agent 还没说话</div>}
          {loaded && msgs.length === 0 && !sub && (
            <div className="py-16 text-center text-[13px] text-muted-foreground">
              <div className="mb-1 text-foreground">{agentName}</div>
              <div className="font-mono text-[12px]">{session.cwd}</div>
            </div>
          )}
          {loaded && msgs.length > 0 && (
            <Virtuoso
              ref={list}
              className="h-full"
              data={rows}
              firstItemIndex={firstIdx.current}
              context={listCtx}
              computeItemKey={(_, r) => r.key}
              itemContent={(_, r) => <RowView row={r} env={env} />}
              // 行高照实记、不取整。Virtuoso 默认 Math.round(高度)：41.5 记成 42、39.8 记成 40，行一卸载就拿这些整数垫占位，
              // 每行差一点、攒起来眼前的内容整段挪一截 —— 打开会话量完缩回那一下抖（逐帧量过：卸掉 32 行，实际 1823px 记成 1833px，
              // 往下挪 10px），会话越长卸得越多抖得越厉害；滚动时边挂边卸的几像素小抖也是它
              itemSize={(el) => el.getBoundingClientRect().height}
              // 在底部就跟着新内容走（直接跳，不要动画）；人往上翻了就不打扰
              followOutput={(bottom) => (bottom && !holding() ? "auto" : false)}
              // 重新挂载（切回一个被卸载过的 tab）时回到上次的位置：在底部就回底部，不在就回那个高度
              {...(saved && !saved.bottom ? { initialScrollTop: saved.top } : { initialTopMostItemIndex: { index: "LAST" as const, align: "end" as const } })}
              atBottomThreshold={80}
              atBottomStateChange={() => {
                syncBottom();
                requestAnimationFrame(syncBottom); // 它报的那一刻高度可能还没量完，下一帧再量一次
              }}
              scrollerRef={(el) => {
                scrollerEl.current = el as HTMLElement | null;
                if (el) {
                  keepBottomOnShrink(el as HTMLElement, bottomRef);
                  lastInputOf(el as HTMLElement); // 挂上监听：挂载过程里没人动过
                }
                watchWidth(el as HTMLElement | null);
              }}
              onScroll={(e) => {
                if (!activeRef.current) return; // 藏着期间的滚动（WebKit 清零）不算
                const el = e.currentTarget as HTMLElement;
                lastH.current = el.scrollHeight;
                syncBottom();
                scrollMem.set(scrollKey, { top: el.scrollTop, bottom: bottomRef.current });
                updatePinned();
                settleFix.current?.();
                nearTop(el);
              }}
              // 流式输出时最后一行在长高：followOutput 要等下一拍才跟，会落后一两行、一顿一顿的。
              // 总高度一变、原来在底部，就当场推到底。
              // 但只在「上次量的时候确实贴着底」才推：人一下子把滚动条拖上去时，新进视野的行一量高度总高就变了，
              // 这时 atBottom 还没来得及变 false，只看它会把人拽回底部（踩过）。
              totalListHeightChanged={() => {
                const el = scrollerEl.current;
                if (!el) return;
                const wasAtBottom = lastH.current - el.clientHeight - el.scrollTop < 80;
                const grew = el.scrollHeight > lastH.current;
                if (bottomRef.current && wasAtBottom && !(grew && holding())) el.scrollTop = el.scrollHeight;
                lastH.current = el.scrollHeight;
                syncBottom();
                updatePinned(); // 行变高矮时顶上是哪行也跟着变，不一定有 onScroll
                // 内容不满一屏（翻不动，就等不到 onScroll）：接着拉。满了就等人自己往上翻，别一口气连拉
                if (activeRef.current && el.scrollHeight <= el.clientHeight + 50) nearTop(el);
                if (!measured) {
                  clearTimeout(measureTimer.current);
                  measureTimer.current = window.setTimeout(() => setMeasured(true), 300);
                }
              }}
              // 虚拟滚动是实验室开关（默认关）：关着时渲染范围放到无限大 = 所有行一次挂好、不再卸载，
              // 贴底 / 回到底部 / 顶上钉的那句话这些照旧走 Virtuoso，不用另写一套
              increaseViewportBy={virtualList && measured ? { top: 800, bottom: 800 } : { top: 1e7, bottom: 1e7 }}
              components={{ Header: ListHeader, Footer: ListFooter }}
            />
          )}
          {pinned !== null && rows[pinned]?.kind === "user" && (
            <PinnedPrompt
              msg={(rows[pinned] as Extract<Row, { kind: "user" }>).msg}
              onClick={() => {
                // The button sits outside the scroller, so the click isn't seen as user input (lastInputOf): starting from the bottom,
                // syncBottom took the smooth scroll for a layout shift and snapped it back. Count the click as input on the scroller.
                markInput(scrollerEl.current);
                list.current?.scrollToIndex({ index: pinned, align: "start", behavior: "smooth" });
              }}
            />
          )}
          {!atBottom && msgs.length > 0 && (
            <Button
              size="icon"
              variant="outline"
              className="absolute bottom-4 left-1/2 size-8 -translate-x-1/2 rounded-full shadow-md"
              onClick={() => list.current?.scrollToIndex({ index: "LAST", align: "end", behavior: "smooth" })}
              title="回到底部"
            >
              <ArrowDown className="size-4" />
            </Button>
          )}
        </div>
        {sub ? (
          <div className="flex items-center justify-center gap-2 border-t px-3 py-2 text-[12px] text-muted-foreground">
            {running && <Loader2 className="size-3.5 animate-spin" />}子 agent 的对话，只读
            {subTask && !running ? ` · ${subTask.status === "completed" ? "已完成" : "已结束"}` : ""}
          </div>
        ) : (
          <div className="mx-auto w-full max-w-[800px]">
            <Composer ref={composer} session={session} paneId={paneId} running={t.running} />
          </div>
        )}
      </div>
    </SessionContext.Provider>
  );
}
