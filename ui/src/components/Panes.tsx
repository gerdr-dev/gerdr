import {
  Bot,
  Columns2,
  Copy,
  GitBranch,
  Maximize2,
  RefreshCw,
  Minimize2,
  MoreHorizontal,
  PanelTopOpen,
  Pencil,
  Rows2,
  SquareTerminal,
  Trash2,
  X,
} from "lucide-react";
import { createContext, useContext, useEffect, useRef, useState } from "react";
import { toast } from "sonner";
import { Button } from "@/components/ui/button";
import { api } from "@/lib/api";
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuSeparator, DropdownMenuTrigger } from "@/components/ui/dropdown-menu";
import { basename, closeFocusedPane, deleteSession, focusPane, focusSession, openTerminal, reopenTerminal, splitFocused, toggleZoom } from "@/lib/actions";
import { leaves, setRatio, updateTab, type PaneLeaf, type PaneNode, type Tab } from "@/lib/layout";
import { applyPaneTitle } from "@/lib/plugins";
import { markRead, set, setLayout, useStore } from "@/lib/store";
import { usePhone } from "@/lib/phone";
import { cn } from "@/lib/utils";
import { AgentIcon, StatusDot } from "./AgentIcon";
import { ChatView } from "./chat/ChatView";
import { NewSession } from "./NewSession";
import { TerminalView } from "./Terminal";

/** 这个 tab 是不是正显示着的那个（藏着的 tab 里的 pane 不算有焦点，不抢光标、不标已读）。 */
export const TabActive = createContext(true);

export function TabView({ tab }: { tab: Tab }) {
  const all = leaves(tab.root);
  const many = all.length > 1;
  const phone = usePhone();
  // 手机上只画焦点那一个 pane：多 pane 平铺在三四百 px 宽里读不了。不改布局里的 zoom ——
  // 布局是存服务端、桌面那边共用的，手机上看一眼不该把电脑上的分屏变成放大。换 pane 走面板一览。
  if (phone) {
    const p = all.find((x) => x.id === tab.focus) ?? all[0];
    return <PaneView pane={p} tab={tab} framed={false} count={many ? { i: all.indexOf(p) + 1, n: all.length } : undefined} />;
  }
  if (tab.zoom) {
    const p = leaves(tab.root).find((x) => x.id === tab.zoom);
    if (p) return <PaneView pane={p} tab={tab} framed={false} zoomed />;
  }
  // 分屏时和 herdr 一样：每个 pane 一圈边框、中间留缝，焦点那个边框高亮。只有一个 pane 时不画框（省地方）。
  return (
    <div className={cn("h-full min-h-0", many && "p-1")}>
      <NodeView node={tab.root} tab={tab} framed={many} />
    </div>
  );
}

function NodeView({ node, tab, framed }: { node: PaneNode; tab: Tab; framed: boolean }) {
  const box = useRef<HTMLDivElement>(null);
  if (node.type === "pane") return <PaneView pane={node} tab={tab} framed={framed} />;
  const row = node.dir === "row";
  const startDrag = (e: React.PointerEvent) => {
    e.preventDefault();
    const el = box.current;
    if (!el) return;
    const r = el.getBoundingClientRect();
    const move = (ev: PointerEvent) => {
      const ratio = row ? (ev.clientX - r.left) / r.width : (ev.clientY - r.top) / r.height;
      setLayout((l) => updateTab(l, tab.id, (t) => setRatio(t, node.id, ratio)));
    };
    const up = () => {
      window.removeEventListener("pointermove", move);
      window.removeEventListener("pointerup", up);
      document.body.style.cursor = "";
    };
    document.body.style.cursor = row ? "col-resize" : "row-resize";
    window.addEventListener("pointermove", move);
    window.addEventListener("pointerup", up);
  };
  return (
    <div ref={box} className={cn("flex h-full min-h-0 w-full min-w-0", row ? "flex-row" : "flex-col")}>
      <div style={{ flexBasis: `${node.ratio * 100}%` }} className="min-h-0 min-w-0 shrink grow-0 overflow-hidden">
        <NodeView node={node.a} tab={tab} framed={framed} />
      </div>
      <div
        onPointerDown={startDrag}
        className={cn(
          // 缝本身透明（两边各有边框），悬停时亮一条提示能拖
          "shrink-0 bg-transparent bg-clip-content transition-colors hover:bg-brand/50",
          row ? "w-1.5 cursor-col-resize px-[2px]" : "h-1.5 cursor-row-resize py-[2px]",
        )}
      />
      <div style={{ flexBasis: `${(1 - node.ratio) * 100}%` }} className="min-h-0 min-w-0 shrink grow-0 overflow-hidden">
        <NodeView node={node.b} tab={tab} framed={framed} />
      </div>
    </div>
  );
}

type Count = { i: number; n: number };

function PaneView({ pane, tab, framed, zoomed, count }: { pane: PaneLeaf; tab: Tab; framed: boolean; zoomed?: boolean; count?: Count }) {
  if (pane.sub) return <SubPaneView pane={pane} tab={tab} framed={framed} zoomed={zoomed} count={count} />;
  if (pane.term) return <TermPaneView pane={pane} tab={tab} framed={framed} zoomed={zoomed} count={count} />;
  return <SessionPaneView pane={pane} tab={tab} framed={framed} zoomed={zoomed} count={count} />;
}

/** 手机上「这个 tab 里有几个 pane、你在第几个」：别的 pane 在屏幕上看不见，得有个地方说它们还在。点开面板一览去挑。 */
function PaneCount({ count }: { count?: Count }) {
  if (!count) return null;
  return (
    <button className="shrink-0 rounded-sm border px-1.5 py-px font-mono text-[10px] text-muted-foreground" title="这个 tab 里的 pane（点开面板一览）" onClick={() => set({ switcher: true, drawer: false })}>
      {count.i}/{count.n}
    </button>
  );
}

/** 子 agent 的只读面板：标题栏 + 只读的对话。 */
function SubPaneView({ pane, tab, framed, zoomed, count }: { pane: PaneLeaf; tab: Tab; framed: boolean; zoomed?: boolean; count?: Count }) {
  const sub = pane.sub!;
  const session = useStore((s) => s.sessions[sub.sessionId]);
  const focused = useContext(TabActive) && tab.focus === pane.id;
  const many = framed || zoomed;
  const task = session?.tasks?.find((t) => t.toolCallId === sub.toolCallId);
  const title = task?.description || sub.title || "子 agent";
  return (
    <div
      className={cn("flex h-full min-h-0 flex-col", framed && "overflow-hidden rounded-md border", framed && (focused ? "border-brand" : "border-border"))}
      onMouseDown={() => !focused && focusPane(pane.id)}
    >
      <div className={cn("flex h-8 shrink-0 items-center gap-2 border-b px-3 text-[12px]", !focused && many && "opacity-70")}>
        <Bot className="size-4 text-muted-foreground" />
        <span className="shrink-0 text-muted-foreground">{task?.subagent || "子 agent"}</span>
        <span className="min-w-0 flex-1 truncate font-medium">{title}</span>
        {task?.status === "running" && <StatusDot status="running" />}
        <PaneCount count={count} />
        {session && (
          <button className="shrink-0 truncate text-[11px] text-muted-foreground hover:text-foreground" onClick={() => focusSession(session.id)}>
            ← {session.title || basename(session.cwd)}
          </button>
        )}
        <Button variant="ghost" size="icon" className="size-6" title="关闭" onClick={() => (focusPane(pane.id), closeFocusedPane())}>
          <X className="size-3.5" />
        </Button>
      </div>
      <div className="min-h-0 flex-1">
        {session ? (
          <ChatView key={pane.id} session={session} paneId={pane.id} focused={focused} sub={sub.toolCallId} />
        ) : (
          <div className="py-16 text-center text-[13px] text-muted-foreground">这个会话已经不在了</div>
        )}
      </div>
    </div>
  );
}

/** 终端：标题栏 + xterm。标题是 shell 设的窗口标题（OSC 0/2，多数 zsh 主题会设成「用户@机器: 目录」或正在跑的命令），没设就是目录名。 */
function TermPaneView({ pane, tab, framed, zoomed, count }: { pane: PaneLeaf; tab: Tab; framed: boolean; zoomed?: boolean; count?: Count }) {
  const term = pane.term!;
  const home = useStore((s) => s.home);
  // 坏数据兜底（旧后端配新界面时写进布局的空终端）：没 id 就显示「重开」
  const cwd = term.cwd || home;
  const focused = useContext(TabActive) && tab.focus === pane.id;
  const many = framed || zoomed;
  const [osc, setOsc] = useState("");
  useStore((s) => s.pluginVersion);
  const title = applyPaneTitle(pane, tab, osc || basename(cwd));
  return (
    <div
      className={cn("flex h-full min-h-0 flex-col", framed && "overflow-hidden rounded-md border", framed && (focused ? "border-brand" : "border-border"))}
      onMouseDown={() => !focused && focusPane(pane.id)}
    >
      <div className={cn("flex h-8 shrink-0 items-center gap-2 border-b px-3 text-[12px]", !focused && many && "opacity-70")}>
        <SquareTerminal className="size-4 text-muted-foreground" />
        <span className="min-w-[3em] max-w-[60%] shrink truncate font-medium">{title}</span>
        <PaneCount count={count} />
        <span className="hidden min-w-0 flex-1 truncate font-mono text-[11px] text-muted-foreground md:block" title={cwd}>
          {cwd}
        </span>
        <div className="flex-1 md:hidden" />
        {zoomed && (
          <Button variant="ghost" size="icon" className="size-6" onClick={toggleZoom} title="还原（prefix+z）">
            <Minimize2 className="size-3.5" />
          </Button>
        )}
        <DropdownMenu>
          <DropdownMenuTrigger asChild>
            <Button variant="ghost" size="icon" className="size-6">
              <MoreHorizontal className="size-3.5" />
            </Button>
          </DropdownMenuTrigger>
          <DropdownMenuContent align="end" onCloseAutoFocus={(e) => e.preventDefault()}>
            <DropdownMenuItem onClick={() => (focusPane(pane.id), openTerminal("right"))}>
              <SquareTerminal /> 右边再开一个终端
            </DropdownMenuItem>
            <DropdownMenuItem onClick={() => (focusPane(pane.id), openTerminal("down"))}>
              <SquareTerminal /> 下面再开一个终端
            </DropdownMenuItem>
            <DropdownMenuItem onClick={() => (focusPane(pane.id), splitFocused("right"))}>
              <Columns2 /> 右边开会话
            </DropdownMenuItem>
            <DropdownMenuItem onClick={() => (focusPane(pane.id), splitFocused("down"))}>
              <Rows2 /> 下面开会话
            </DropdownMenuItem>
            {!zoomed && !count && leaves(tab.root).length > 1 && (
              <DropdownMenuItem onClick={() => (focusPane(pane.id), toggleZoom())}>
                <Maximize2 /> 放大
              </DropdownMenuItem>
            )}
            <DropdownMenuSeparator />
            <DropdownMenuItem onClick={() => (focusPane(pane.id), closeFocusedPane())}>
              <X /> 关闭终端
            </DropdownMenuItem>
          </DropdownMenuContent>
        </DropdownMenu>
      </div>
      <div className="min-h-0 flex-1">
        {term.id ? (
          <TerminalView key={term.id} paneId={pane.id} termId={term.id} cwd={cwd} focused={focused} onTitle={setOsc} />
        ) : (
          <div className="flex h-full flex-col items-center justify-center gap-3 text-[13px] text-muted-foreground">
            这个终端没开起来
            <Button size="sm" variant="outline" onClick={() => reopenTerminal(pane.id, cwd)}>
              <SquareTerminal className="size-3.5" />在 {basename(cwd)} 重开
            </Button>
          </div>
        )}
      </div>
    </div>
  );
}

function SessionPaneView({ pane, tab, framed, zoomed, count }: { pane: PaneLeaf; tab: Tab; framed: boolean; zoomed?: boolean; count?: Count }) {
  const session = useStore((s) => (pane.sessionId ? s.sessions[pane.sessionId] : undefined));
  const unread = useStore((s) => (pane.sessionId ? !!s.unread[pane.sessionId] : false));
  useStore((s) => s.pluginVersion);
  const focused = useContext(TabActive) && tab.focus === pane.id;
  const many = framed || zoomed;

  useEffect(() => {
    if (focused && pane.sessionId && unread) markRead(pane.sessionId);
  }, [focused, pane.sessionId, unread]);

  const base = session ? session.title || basename(session.cwd) : "新会话";
  const title = applyPaneTitle(pane, tab, base);

  return (
    <div
      className={cn("flex h-full min-h-0 flex-col", framed && "overflow-hidden rounded-md border", framed && (focused ? "border-brand" : "border-border"))}
      onMouseDown={() => !focused && focusPane(pane.id)}
    >
      <div className={cn("flex h-8 shrink-0 items-center gap-2 border-b px-3 text-[12px]", !focused && many && "opacity-70")}>
        {session ? <AgentIcon agent={session.agent} className="size-4" /> : <PanelTopOpen className="size-3.5 text-muted-foreground" />}
        <span className="min-w-[3em] max-w-[60%] shrink truncate font-medium">{title}</span>
        <StatusDot status={session?.status} unread={unread} />
        {session?.pending ? <span className="shrink-0 whitespace-nowrap rounded-sm bg-bad/15 px-1.5 text-[11px] text-bad">等你</span> : null}
        <PaneCount count={count} />
        {session ? (
          <span className="hidden min-w-0 flex-1 items-center gap-2 font-mono text-[11px] text-muted-foreground md:flex">
            <span className="min-w-0 truncate" title={session.cwd}>
              {session.cwd}
            </span>
            {/* 分支不许被截：窄的时候宁可截路径（路径的末段在标题里多半已经看得出来） */}
            {session.branch && (
              <span className="flex shrink-0 items-center gap-1" title={`git 分支：${session.branch}`}>
                <GitBranch className="size-3" />
                <span className="max-w-[16em] truncate">{session.branch}</span>
              </span>
            )}
          </span>
        ) : (
          <div className="flex-1" />
        )}
        {zoomed && (
          <Button variant="ghost" size="icon" className="size-6" onClick={toggleZoom} title="还原（prefix+z）">
            <Minimize2 className="size-3.5" />
          </Button>
        )}
        <DropdownMenu>
          <DropdownMenuTrigger asChild>
            <Button variant="ghost" size="icon" className="size-6">
              <MoreHorizontal className="size-3.5" />
            </Button>
          </DropdownMenuTrigger>
          <DropdownMenuContent align="end" onCloseAutoFocus={(e) => e.preventDefault()}>
            <DropdownMenuItem onClick={() => (focusPane(pane.id), splitFocused("right"))}>
              <Columns2 /> 左右分屏
            </DropdownMenuItem>
            <DropdownMenuItem onClick={() => (focusPane(pane.id), splitFocused("down"))}>
              <Rows2 /> 上下分屏
            </DropdownMenuItem>
            {!zoomed && !count && leaves(tab.root).length > 1 && (
              <DropdownMenuItem onClick={() => (focusPane(pane.id), toggleZoom())}>
                <Maximize2 /> 放大
              </DropdownMenuItem>
            )}
            {session && (
              <>
                <DropdownMenuSeparator />
                <DropdownMenuItem onClick={() => set({ dialog: { kind: "rename", target: "session", id: session.id, value: session.title ?? "" } })}>
                  <Pencil /> 重命名会话
                </DropdownMenuItem>
                <DropdownMenuItem
                  onClick={() => {
                    navigator.clipboard.writeText(session.agentSessionId);
                    toast("已复制 agent 会话 id");
                  }}
                >
                  <Copy /> 复制 agent 会话 id
                </DropdownMenuItem>
                {["claude", "codex"].includes(session.base ?? session.agent) && (
                  // 在别处（herdr / 终端）接着聊过的会话，打开时会自动追上；这里是手动的那一下
                  <DropdownMenuItem
                    onClick={() =>
                      api
                        .reload(session.id)
                        .then(() => toast.success("已从 agent 自己的记录重新载入"))
                        .catch((e) => toast.error(e.message))
                    }
                  >
                    <RefreshCw /> 从 agent 记录重新载入
                  </DropdownMenuItem>
                )}
                <DropdownMenuItem variant="destructive" onClick={() => deleteSession(session.id).catch((e) => toast.error(e.message))}>
                  <Trash2 /> 删除会话
                </DropdownMenuItem>
              </>
            )}
            <DropdownMenuSeparator />
            <DropdownMenuItem onClick={() => (focusPane(pane.id), closeFocusedPane())}>
              <X /> 关闭 pane
            </DropdownMenuItem>
          </DropdownMenuContent>
        </DropdownMenu>
      </div>
      <div className="min-h-0 flex-1">
        {session ? (
          <ChatView key={session.id} session={session} paneId={pane.id} focused={focused} />
        ) : (
          <NewSession key={pane.id + (pane.draft?.cwd ?? "")} pane={pane} focused={focused} />
        )}
      </div>
    </div>
  );
}
