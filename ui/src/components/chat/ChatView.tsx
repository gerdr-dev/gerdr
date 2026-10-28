import { useContext, useEffect, useMemo, useRef, useState } from "react";
import { TabActive } from "../Panes";
import { Virtuoso, type VirtuosoHandle } from "react-virtuoso";
import { ArrowDown } from "lucide-react";
import { Button } from "@/components/ui/button";
import { buildRows, RowView, type RowEnv } from "./Rows";
import { toast } from "sonner";
import { api } from "@/lib/api";
import { get, setConfig, subTranscript, transcript, useStore } from "@/lib/store";
import { useKept } from "@/lib/kept";
import { Loader2 } from "lucide-react";
import type { GMessage } from "@/lib/transcript";
import type { SessionSummary } from "@/lib/types";
import { Composer, type ComposerHandle } from "./Composer";
import { SessionContext } from "./sessionContext";
import { TasksBar } from "./TasksBar";

/** sub：子 agent 的只读面板（值 = 它在主会话里那张卡片的 toolCallId）。没有输入框、不能回退。 */
// 最后一条和输入框之间留一截（照 ChatGPT 桌面版的留白）：贴太近的话最后那行元信息和输入框挤在一起
const ListFooter = () => <div className="h-12" />;

// 每个 pane 的滚动位置（在不在底部、滚到哪）：列表被卸载再挂回来（tab 被挤出保留数、虚拟列表重建）时照它恢复。
const scrollMem = new Map<string, { top: number; bottom: boolean }>();

export function ChatView({ session, paneId, focused, sub }: { session: SessionSummary; paneId: string; focused: boolean; sub?: string }) {
  useStore((s) => s.tv[session.id]); // 日志变了就重画
  const t = sub ? subTranscript(session.id, sub) : transcript(session.id);
  const subTask = sub ? session.tasks?.find((x) => x.toolCallId === sub) : undefined;
  const running = sub ? subTask?.status === "running" : t.running;
  const loaded = useStore((s) => s.tv[session.id] !== undefined);
  const agentName = useStore((s) => s.agents.find((a) => a.id === session.agent)?.name) ?? session.agent;
  const composer = useRef<ComposerHandle>(null);

  // 焦点切到这个 pane（或者前缀动作做完）时，把光标放回输入框。
  useEffect(() => {
    if (!focused || sub) return;
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
      if (e.defaultPrevented || e.isComposing || e.metaKey || e.ctrlKey || e.altKey || e.key.length !== 1) return;
      const a = document.activeElement as HTMLElement | null;
      if (a && (a.isContentEditable || /^(INPUT|TEXTAREA|SELECT)$/.test(a.tagName))) return;
      if (get().dialog || document.querySelector("[role=dialog]")) return;
      composer.current?.focus(); // 在 keydown 里换焦点，这个键的字就落到新焦点上
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [focused, sub]);

  // 点对话区任何地方（不是在选字、也不是点按钮 / 链接这些）：光标回到输入框，直接打字（用户点名的）
  const clickToType = (e: React.MouseEvent) => {
    if (sub || e.button !== 0) return;
    const t = e.target as HTMLElement;
    if (t.closest("button,a,input,textarea,select,summary,label,[role=button],[role=menuitem],[role=checkbox],[role=radio],[contenteditable=true],[data-keep-focus]")) return;
    const sel = window.getSelection();
    if (sel && !sel.isCollapsed) return; // 在选字（拖选、双击选词）—— 抢走焦点会让人复制不了
    composer.current?.focus();
  };

  // 回退 / 改写：确认在消息旁边的小气泡里做（RewindButton），这里只管真的去回退
  const doRewind = async (msg: GMessage, edit: boolean, restore: boolean) => {
    try {
      const r = await api.rewind(session.id, msg.metadata!.messageId!, restore);
      if (msg.metadata?.snapshot && restore !== get().config.rewindFiles) setConfig({ rewindFiles: restore }); // 记住这次的选择，下次默认照它
      const text = (r.content ?? []).map((c) => (c.type === "text" ? c.text : "")).join("");
      if (edit || text) composer.current?.setText(text);
      toast.success(r.restoredFrom ? "已回退（对话和文件）" : "已回退对话");
    } catch (e: any) {
      toast.error(e.message ?? String(e));
    }
  };

  const msgs = t.messages;
  const [showCleared, setShowCleared] = useKept(`cleared:${session.id}:${sub ?? ""}`, false);
  const [openGroups, setOpenGroups] = useKept<Record<string, boolean>>(`groups:${session.id}:${sub ?? ""}`, {});
  const rows = useMemo(() => buildRows(msgs, running, showCleared, openGroups), [msgs, running, showCleared, openGroups]);
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
  const lastH = useRef(0); // 上一次量到的总高（推到底之前用它判断「刚才是不是贴着底」）
  // 行是 memo 的，回调要稳定（不然每次都重渲染），但又要拿到最新的 —— 放在 ref 里转一手
  const latest = useRef({ doRewind, sessionId: session.id, openGroups, setOpenGroups });
  latest.current = { doRewind, sessionId: session.id, openGroups, setOpenGroups };
  const env = useMemo<RowEnv>(
    () => ({
      sessionId: session.id,
      running,
      onRewind: sub ? undefined : (m, restore) => latest.current.doRewind(m, false, restore),
      onEdit: sub ? undefined : (m, restore) => latest.current.doRewind(m, true, restore),
      onQuickReply: (s) => api.send(latest.current.sessionId, [{ type: "text", text: s }]).catch((e) => toast.error(e.message)),
      onShowCleared: () => setShowCleared(true),
      onToggleGroup: (key) => {
        const { openGroups: g, setOpenGroups: set } = latest.current;
        set({ ...g, [key]: !g[key] });
      },
    }),
    [session.id, running, sub],
  );
  return (
    <SessionContext.Provider value={session}>
      <div className="flex h-full min-h-0 flex-col" onMouseUp={clickToType}>
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
              computeItemKey={(_, r) => r.key}
              itemContent={(_, r) => <RowView row={r} env={env} />}
              // 在底部就跟着新内容走（直接跳，不要动画）；人往上翻了就不打扰
              followOutput={(bottom) => (bottom ? "auto" : false)}
              // 重新挂载（切回一个被卸载过的 tab）时回到上次的位置：在底部就回底部，不在就回那个高度
              {...(saved && !saved.bottom ? { initialScrollTop: saved.top } : { initialTopMostItemIndex: { index: "LAST" as const, align: "end" as const } })}
              atBottomThreshold={80}
              atBottomStateChange={(b) => {
                if (!activeRef.current) return;
                bottomRef.current = b;
                setAtBottom(b);
              }}
              scrollerRef={(el) => (scrollerEl.current = el as HTMLElement | null)}
              onScroll={(e) => {
                if (!activeRef.current) return; // 藏着期间的滚动（WebKit 清零）不算
                const el = e.currentTarget as HTMLElement;
                lastH.current = el.scrollHeight;
                const bottom = el.scrollHeight - el.clientHeight - el.scrollTop < 80;
                bottomRef.current = bottom;
                scrollMem.set(scrollKey, { top: el.scrollTop, bottom });
              }}
              // 流式输出时最后一行在长高：followOutput 要等下一拍才跟，会落后一两行、一顿一顿的。
              // 总高度一变、原来在底部，就当场推到底。
              // 但只在「上次量的时候确实贴着底」才推：人一下子把滚动条拖上去时，新进视野的行一量高度总高就变了，
              // 这时 atBottom 还没来得及变 false，只看它会把人拽回底部（踩过）。
              totalListHeightChanged={() => {
                const el = scrollerEl.current;
                if (!el) return;
                const wasAtBottom = lastH.current - el.clientHeight - el.scrollTop < 80;
                if (bottomRef.current && wasAtBottom) el.scrollTop = el.scrollHeight;
                lastH.current = el.scrollHeight;
              }}
              increaseViewportBy={{ top: 800, bottom: 800 }}
              components={{ Footer: ListFooter }}
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
            <TasksBar session={session} />
            <Composer ref={composer} session={session} paneId={paneId} running={t.running} />
          </div>
        )}
      </div>
    </SessionContext.Provider>
  );
}
