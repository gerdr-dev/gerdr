import { useCallback, useContext, useEffect, useMemo, useRef, useState } from "react";
import { TabActive } from "../Panes";
import { Virtuoso, type VirtuosoHandle } from "react-virtuoso";
import { ArrowDown } from "lucide-react";
import { Button } from "@/components/ui/button";
import { buildRows, RowView, type RowEnv } from "./Rows";
import { toast } from "sonner";
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@/components/ui/alert-dialog";
import { Checkbox } from "@/components/ui/checkbox";
import { Label } from "@/components/ui/label";
import { api } from "@/lib/api";
import { get, setConfig, subTranscript, transcript, useStore } from "@/lib/store";
import { Loader2 } from "lucide-react";
import type { GMessage } from "@/lib/transcript";
import type { SessionSummary } from "@/lib/types";
import { Composer, type ComposerHandle } from "./Composer";
import { SessionContext } from "./sessionContext";
import { TasksBar } from "./TasksBar";

/** sub：子 agent 的只读面板（值 = 它在主会话里那张卡片的 toolCallId）。没有输入框、不能回退。 */
const ListFooter = () => <div className="h-4" />;

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
  const [rewind, setRewind] = useState<{ msg: GMessage; edit: boolean; files?: string[] | null } | null>(null);
  const restoreDefault = useStore((s) => s.config.rewindFiles);
  const [restore, setRestore] = useState(restoreDefault);

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

  const openRewind = useCallback(
    async (msg: GMessage, edit: boolean) => {
      setRestore(get().config.rewindFiles);
      setRewind({ msg, edit });
      if (msg.metadata?.snapshot) {
        try {
          const r = await api.rewindPreview(session.id, msg.metadata.messageId!);
          setRewind((cur) => (cur && cur.msg === msg ? { ...cur, files: r.files } : cur));
        } catch {}
      }
    },
    [session.id],
  );

  const doRewind = async () => {
    if (!rewind) return;
    const { msg, edit } = rewind;
    setRewind(null);
    try {
      const r = await api.rewind(session.id, msg.metadata!.messageId!, restore && !!msg.metadata?.snapshot);
      if (restore !== get().config.rewindFiles) setConfig({ rewindFiles: restore });
      const text = (r.content ?? []).map((c) => (c.type === "text" ? c.text : "")).join("");
      if (edit || text) composer.current?.setText(text);
      toast.success(r.restoredFrom ? "已回退（对话和文件）" : "已回退对话");
    } catch (e: any) {
      toast.error(e.message ?? String(e));
    }
  };

  const msgs = t.messages;
  const rows = useMemo(() => buildRows(msgs, running), [msgs, running]);
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
  const latest = useRef({ openRewind, sessionId: session.id });
  latest.current = { openRewind, sessionId: session.id };
  const env = useMemo<RowEnv>(
    () => ({
      sessionId: session.id,
      running,
      onRewind: sub ? undefined : (m) => latest.current.openRewind(m, false),
      onEdit: sub ? undefined : (m) => latest.current.openRewind(m, true),
      onQuickReply: (s) => api.send(latest.current.sessionId, [{ type: "text", text: s }]).catch((e) => toast.error(e.message)),
    }),
    [session.id, running, sub],
  );
  return (
    <SessionContext.Provider value={session}>
      <div className="flex h-full min-h-0 flex-col">
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
          <div className="mx-auto w-full max-w-[860px]">
            <TasksBar session={session} />
            <Composer ref={composer} session={session} paneId={paneId} running={t.running} />
          </div>
        )}
        <AlertDialog open={!!rewind} onOpenChange={(o) => !o && setRewind(null)}>
          <AlertDialogContent>
            <AlertDialogHeader>
              <AlertDialogTitle>{rewind?.edit ? "改这句重发" : "回到这条消息之前"}</AlertDialogTitle>
              <AlertDialogDescription>
                这条消息和它之后的对话都会被丢掉，agent 也会忘掉它们。{rewind?.edit ? "原话会放回输入框，改完再发。" : ""}
              </AlertDialogDescription>
            </AlertDialogHeader>
            {rewind?.msg.metadata?.snapshot ? (
              <div className="space-y-2">
                <div className="flex items-center gap-2">
                  <Checkbox id="restore" checked={restore} onCheckedChange={(v) => setRestore(!!v)} />
                  <Label htmlFor="restore">同时把文件恢复到那时候</Label>
                </div>
                {restore && rewind.files && rewind.files.length > 0 && (
                  <div className="max-h-40 overflow-auto rounded-md border bg-muted/40 p-2 font-mono text-[11px]">
                    {rewind.files.map((f) => (
                      <div key={f} className="truncate">
                        {f}
                      </div>
                    ))}
                  </div>
                )}
                {restore && rewind.files && rewind.files.length === 0 && <div className="text-[12px] text-muted-foreground">从那以后文件没有变化。</div>}
                {restore && <div className="text-[12px] text-muted-foreground">恢复前会先给现在的状态打一个快照，恢复错了还能找回来。</div>}
              </div>
            ) : (
              <div className="text-[12px] text-muted-foreground">这条消息没有文件快照，只回退对话。</div>
            )}
            <AlertDialogFooter>
              <AlertDialogCancel>取消</AlertDialogCancel>
              <AlertDialogAction onClick={doRewind}>回退</AlertDialogAction>
            </AlertDialogFooter>
          </AlertDialogContent>
        </AlertDialog>
      </div>
    </SessionContext.Provider>
  );
}
