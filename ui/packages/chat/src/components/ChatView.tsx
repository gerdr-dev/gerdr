import { useEffect, useMemo, useRef, useState, type ReactNode } from "react";
import { flushSync } from "react-dom";
import { ArrowDown, CornerDownRight } from "lucide-react";
import { Button } from "../ui/button";
import { cn } from "../utils";
import { buildRows, type Row, type RowEnv } from "./Rows";
import { toast } from "sonner";
import { useChatHost, useChatSettings } from "../host";
import { t as tr } from "../i18n";
import { useKept } from "../kept";
import { Loader2 } from "lucide-react";
import type { GMessage } from "../transcript";
import type { ChatSession } from "../types";
import { Composer, type ComposerHandle } from "./Composer";
import { SessionContext } from "./sessionContext";
import { askKey } from "../askkeys";
import { isCoarse } from "../device";
import { ChatList, type ListHandle } from "./ChatList";

/** sub：子 agent 的只读面板（值 = 它在主会话里那张卡片的 toolCallId）。没有输入框、不能回退。 */
// 顶上钉着的那句用户消息：一行，太长截掉；图片 / 文件写成 [图片] 这种占位
function PinnedPrompt({ msg, onClick }: { msg: GMessage; onClick: () => void }) {
  const text = msg.parts
    .map((p) => (p.type === "text" ? p.text : p.type === "file" ? (p.mediaType?.startsWith("image/") ? tr("chat.image") : tr("chat.file")) : ""))
    .join(" ")
    .replace(/\s+/g, " ")
    .trim();
  return (
    <div className="pointer-events-none absolute inset-x-0 top-0 z-10 px-4">
      <button
        className="pointer-events-auto mx-auto flex w-full max-w-[800px] items-center gap-2 rounded-b-md border border-t-0 bg-background/95 px-3 py-1 text-left text-[13px] text-muted-foreground shadow-sm backdrop-blur hover:text-foreground"
        onClick={onClick}
        title={tr("chat.jumpTo")}
      >
        <CornerDownRight className="size-3.5 shrink-0" />
        <span className="truncate">{text || tr("chat.emptyMessage")}</span>
      </button>
    </div>
  );
}

// 每个 pane 的滚动位置（在不在底部、滚到哪）：列表被卸载再挂回来（tab 被挤出保留数、虚拟列表重建）时照它恢复。
const scrollMem = new Map<string, { top: number; bottom: boolean }>();

/**
 * One session's conversation: the message list and the composer. paneId tells composers apart (their drafts, scroll
 * positions); focused: this one takes the keyboard; active: false while hidden (a background tab), so scroll
 * positions aren't recorded then. sub: a sub-agent's read-only view (the toolCallId of its card): no composer, no rewind.
 * children: drawn over the conversation (e.g. a host's own empty state), inside the area that takes dropped files.
 */
export function ChatView({
  session,
  paneId,
  focused,
  sub,
  active = true,
  children,
}: {
  session: ChatSession;
  paneId: string;
  focused: boolean;
  sub?: string;
  active?: boolean;
  children?: ReactNode;
}) {
  const host = useChatHost();
  const settings = useChatSettings();
  const t = host.useTranscript(session.id, sub);
  const subTask = sub ? session.tasks?.find((x) => x.toolCallId === sub) : undefined;
  const running = sub ? subTask?.status === "running" : t.running;
  const loaded = t.loaded;
  const agentName = host.agentName?.(session.agent) ?? session.agent;
  const composer = useRef<ComposerHandle>(null);
  const [dragging, setDragging] = useState(false);
  const dragDepth = useRef(0); // dragenter / dragleave 进出子元素也会触发：数层数，出到最外面才算离开

  // 焦点切到这个 pane（或者前缀动作做完）时，把光标放回输入框。
  // 触屏上不做：聚焦输入框就弹软键盘，切个面板键盘就盖住半屏。手机上只有点了输入框本身才弹（和 Terminal 一样）。
  useEffect(() => {
    if (!focused || sub || isCoarse()) return;
    const f = () => {
      if (!document.querySelector("[role=dialog]")) composer.current?.focus();
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
      if (document.querySelector("[role=dialog]")) return;
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
      const r = await host.rewind!(session.id, msg.metadata!.messageId!);
      composer.current?.setContent(r.content ?? []);
    } catch (e: any) {
      toast.error(e.message ?? String(e));
    }
  };

  const msgs = t.all;
  const virtualList = !!settings.virtualList;
  const showThinking = settings.showThinking !== false;
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
    return [buildRows(msgs, running, openGroups, new Set(liveTaskIds.split(" ").filter(Boolean)), Date.now(), out, showThinking), out.wake] as const;
  }, [msgs, running, openGroups, liveTaskIds, tick, showThinking]);
  useEffect(() => {
    if (wake === undefined) return;
    const t = setTimeout(() => setTick((x) => x + 1), Math.max(0, wake - Date.now()));
    return () => clearTimeout(t);
  }, [wake]);
  // Older messages put in front (loadOlder): firstIdx goes down by as many rows, so a row keeps its data-item-index.
  const older = t.older;
  const olderRef = useRef(t);
  olderRef.current = t;
  // 前面多了几行：拿原来的前几行去新的里找，位置差就是。不只认第一行 —— 接缝那儿的行可能变了（折叠组并进了新来的工具调用）
  const firstIdx = useRef(1_000_000);
  const prevRows = useRef<Row[]>([]);
  if (rows !== prevRows.current) {
    const prev = prevRows.current;
    if (prev.length && rows.length > prev.length && prev[0].key !== rows[0].key) {
      const at = new Map(rows.map((r, i) => [r.key, i]));
      for (let j = 0; j < Math.min(prev.length, 50); j++) {
        const i = at.get(prev[j].key);
        if (i === undefined) continue;
        if (i > j) {
          firstIdx.current -= i - j;
        }
        break;
      }
    }
    prevRows.current = rows;
  }
  // 离顶不到一屏 / 内容还不满一屏：接着拉更早的
  const nearTop = (el: HTMLElement) => {
    if (olderRef.current.older === "more" && el.scrollTop < Math.max(400, el.clientHeight)) olderRef.current.loadOlder?.();
  };
  const list = useRef<ListHandle>(null);
  const scrollKey = `${paneId}:${session.id}:${sub ?? ""}`;
  const saved = scrollMem.get(scrollKey);
  const [atBottom, setAtBottom] = useState(saved ? saved.bottom : true);
  const bottomRef = useRef(saved ? saved.bottom : true);
  // Last row seen while at the bottom: once the list has grown past it while scrolled up, the jump-to-bottom button turns
  // brand-colored so new output isn't missed. Keyed by row (not count) so loading older history on top doesn't light it.
  const lastKey = rows[rows.length - 1]?.key;
  const seenKey = useRef(lastKey);
  if (atBottom) seenKey.current = lastKey;
  const unread = !atBottom && lastKey !== seenKey.current;
  // tab 藏起来再显示：滚动位置自己恢复。WebKit（mac App 的 WKWebView）里 visibility:hidden 的滚动容器会被清零
  // （Chrome 不会）——实测切走一下再回来就在最顶上。所以只在显示着的时候记位置（藏着期间那次清零不记），显示时照记的恢复。
  const tabActive = active;
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
  // Pin "which prompt this reply answers" at the top (as Claude Code does): the nearest user message above the top row
  // of the screen, click to jump back to it. If the top row is a prompt, the one before it is pinned, so the bar is
  // always there. Also pinned at the bottom: a long reply scrolls the prompt away while the user sits at the bottom
  // reading output (user report). The list is virtual, so the top row is found from the DOM's data-index.
  const [pinned, setPinned] = useState<number | null>(null);
  const pinRaf = useRef(0);
  const pinnedRef = useRef<number | null>(null);
  const rowsRef = useRef(rows);
  rowsRef.current = rows;
  // Commit with flushSync: a plain setState from a rAF / ResizeObserver callback renders after the frame is painted,
  // so on opening a chat the list showed up one or two frames before the pinned prompt (measured: 40-80ms, user report).
  const pinNow = () => {
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
    // The nearest user message strictly above the top row. When the top row is a user message itself (it is on
    // screen), pin the one before it instead: the bar stays up as a way back to the previous prompt rather than
    // vanishing whenever a prompt scrolls into view.
    let u = -1;
    for (let i = Math.min(topIdx - 1, rs.length - 1); i >= 0; i--)
      if (rs[i].kind === "user") {
        u = i;
        break;
      }
    // The list is hidden for its first frames, until it has placed the initial scroll position; pinning then would
    // show the prompt over an empty pane ahead of the rows.
    const lst = el.querySelector<HTMLElement>("[data-chat-list]");
    const hidden = !!lst && getComputedStyle(lst).visibility === "hidden";
    const p = !hidden && u >= 0 ? u : null;
    if (p === pinnedRef.current) return;
    pinnedRef.current = p;
    flushSync(() => setPinned(p));
  };
  const updatePinned = () => {
    cancelAnimationFrame(pinRaf.current);
    pinRaf.current = requestAnimationFrame(pinNow);
  };
  useEffect(() => () => cancelAnimationFrame(pinRaf.current), []);
  // 人刚点了展开（工具调用、折叠组、思考）：这一小会儿内容变高不追到底，点的那一行留在原地，展开的东西往下长。
  // 不然贴着底时一展开就被推到最底下，刚点的那行顶出屏幕（用户报的）。收起变矮不受影响，照样贴底。
  const holdUntil = useRef(0);
  const hold = () => (holdUntil.current = performance.now() + 500);
  const holding = () => performance.now() < holdUntil.current;
  // 行是 memo 的，回调要稳定（不然每次都重渲染），但又要拿到最新的 —— 放在 ref 里转一手
  const latest = useRef({ doRewind, sessionId: session.id, openGroups, setOpenGroups });
  latest.current = { doRewind, sessionId: session.id, openGroups, setOpenGroups };
  const steerRewind = (session.base ?? session.agent) === "claude";
  const env = useMemo<RowEnv>(
    () => ({
      sessionId: session.id,
      running,
      onRewind: sub || !host.rewind ? undefined : (m) => latest.current.doRewind(m),
      steerRewind,
      onWithdraw:
        sub || !host.withdraw
          ? undefined
          : async (id, now) => {
              const c = await host.withdraw!(session.id, id, now);
              if (c?.length) composer.current?.putBack(c);
            },
      onQuickReply: (s) => host.send(latest.current.sessionId, [{ type: "text", text: s }]).catch((e) => toast.error(e.message)),
      onToggleGroup: (key) => {
        const { openGroups: g, setOpenGroups: set } = latest.current;
        hold();
        set({ ...g, [key]: !g[key] });
      },
    }),
    [session.id, running, sub, steerRewind, host],
  );
  return (
    <SessionContext.Provider value={session}>
      <div
        data-gerdr-chat=""
        className="relative flex h-full min-h-0 flex-col"
        onMouseUp={clickToType}
        onClickCapture={(e) => (e.target as HTMLElement).closest("[aria-expanded]") && hold()}
        // 文件拖到对话里任何地方都收（不只输入框）：附到输入框里。不接的话浏览器会把文件当页面打开
        onDragEnter={(e) => {
          if (sub || !e.dataTransfer.types.includes("Files")) return;
          dragDepth.current++;
          setDragging(true);
        }}
        onDragOver={(e) => {
          if (!sub && e.dataTransfer.types.includes("Files")) e.preventDefault();
        }}
        onDragLeave={() => {
          if (dragDepth.current > 0 && --dragDepth.current === 0) setDragging(false);
        }}
        onDrop={(e) => {
          dragDepth.current = 0;
          setDragging(false);
          if (sub || !e.dataTransfer.files.length) return;
          e.preventDefault();
          composer.current?.addFiles(e.dataTransfer.files);
          composer.current?.focus();
        }}
      >
        {children}
        {dragging && (
          <div className="pointer-events-none absolute inset-2 z-30 flex items-center justify-center rounded-xl border-2 border-dashed border-brand/60 bg-background/80 text-sm text-foreground">
            {tr("composer.dropFiles")}
          </div>
        )}
        <div className="relative min-h-0 flex-1">
          {!loaded && <div className="py-10 text-center text-[13px] text-muted-foreground">{tr("chat.loading")}</div>}
          {loaded && msgs.length === 0 && sub && <div className="py-16 text-center text-[13px] text-muted-foreground">{tr("chat.subagentEmpty")}</div>}
          {loaded && msgs.length === 0 && !sub && (
            <div className="py-16 text-center text-[13px] text-muted-foreground">
              <div className="mb-1 text-foreground">{agentName}</div>
              <div className="font-mono text-[12px]">{session.cwd}</div>
            </div>
          )}
          {loaded && msgs.length > 0 && (
            <ChatList
              ref={list}
              rows={rows}
              env={env}
              older={older}
              firstIdx={firstIdx.current}
              memKey={scrollKey}
              all={!virtualList}
              startTop={saved && !saved.bottom ? saved.top : undefined}
              holding={holding}
              scrollerRef={(el) => (scrollerEl.current = el)}
              onScroll={(el) => {
                if (!activeRef.current) return; // 藏着期间的滚动（WebKit 清零）不算
                scrollMem.set(scrollKey, { top: el.scrollTop, bottom: bottomRef.current });
                updatePinned();
                nearTop(el);
              }}
              onAtBottom={(b) =>
                // Can come from a layout pass while React renders: set state after
                queueMicrotask(() => {
                  if (b === bottomRef.current) return;
                  bottomRef.current = b;
                  setAtBottom(b);
                })
              }
              onLayout={() => {
                updatePinned();
                const el = scrollerEl.current;
                // Not a screenful yet (nothing to scroll, so no onScroll): keep loading older
                if (el && activeRef.current && el.scrollHeight <= el.clientHeight + 50) nearTop(el);
              }}
            />
          )}
          {pinned !== null && rows[pinned]?.kind === "user" && (
            <PinnedPrompt
              msg={(rows[pinned] as Extract<Row, { kind: "user" }>).msg}
              onClick={() => {
                list.current?.scrollToIndex({ index: pinned, align: "start", behavior: "smooth" });
              }}
            />
          )}
          {!atBottom && msgs.length > 0 && (
            <Button
              size="icon"
              variant="outline"
              className={cn(
                "absolute bottom-4 left-1/2 size-8 -translate-x-1/2 rounded-full shadow-md",
                // dark: variants too, or the outline variant's dark:border-input wins in the dark theme.
                unread && "border-brand text-brand hover:text-brand dark:border-brand",
              )}
              onClick={() => list.current?.scrollToIndex({ index: "LAST", align: "end", behavior: "smooth" })}
              title={unread ? tr("chat.toBottomNew") : tr("chat.toBottom")}
            >
              <ArrowDown className="size-4" />
            </Button>
          )}
        </div>
        {sub ? (
          <div className="flex items-center justify-center gap-2 border-t px-3 py-2 text-[12px] text-muted-foreground">
            {running && <Loader2 className="size-3.5 animate-spin" />}
            {tr("chat.subagentReadOnly")}
            {subTask && !running ? ` · ${subTask.status === "completed" ? tr("chat.subagentDone") : tr("chat.subagentEnded")}` : ""}
          </div>
        ) : (
          <div className="mx-auto w-full max-w-[800px]">
            <Composer
              ref={composer}
              session={session}
              paneId={paneId}
              running={t.running}
              history={() => olderRef.current.all}
              onEmptyEnter={() => list.current?.scrollToIndex({ index: "LAST", align: "end", behavior: "smooth" })}
            />
          </div>
        )}
      </div>
    </SessionContext.Provider>
  );
}
