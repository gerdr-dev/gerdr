import { Briefcase, Check, ChevronDown, Code2, History, LayoutGrid, MessageSquarePlus, Settings } from "lucide-react";
import { useEffect, useRef, useState } from "react";
import { toast } from "sonner";
import { AgentIcon, StatusDot } from "@/components/AgentIcon";
import { COL_EDGE, SidebarEdge, SidebarFooter, SidebarHeader, startColResize } from "@/components/Chrome";
import { formatCombo } from "@/lib/keys";
import { toggleOverview } from "@/lib/overview";
import { ChatView } from "@/components/chat/ChatView";
import { PixelLoader } from "@/components/chat/PixelLoader";
import { Button } from "@/components/ui/button";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { PHONE_MAX, usePhone } from "@/lib/phone";
import { get, keyFor, set, setMode, useStore } from "@/lib/store";
import { syncWatch } from "@/lib/sync";
import { cn } from "@/lib/utils";
import { askWorkAgent, ensureWorkSession, newWorkChat, openWorkChat, useWorkChats, useWorkProject, useWorkSession, workAgent } from "@/lib/work";

// Work mode: the user's own Work project on the left, one agent on the right (Annulo's layout, ../shuttle).
//
// The left side is a whole React site of its own — <GERDR_DIR>/work/ui, served by gerdr under /work/ with
// no build step, exactly like gerdr's UI — so its menu, pages and styling are entirely the user's (or the
// Work agent's) to write. It runs in a same-origin frame: a broken page can't take gerdr down, and it
// talks to gerdr through postMessage (ask the agent, open settings) and /api/work/run (local functions).
// The Code / Work switch is gerdr's, outside the frame.

const WORK_PANE = "work"; // drafts / scroll position key prefix for the right-hand chat (it has no pane in the layout)

// The chat column's width: dragged by its left edge (asked for), kept per browser like the sidebar's
const CHAT_W = 440;
const CHAT_MIN = 320;
const CHAT_MAX = 900;
const clampChatW = (w: number) => Math.round(Math.min(CHAT_MAX, window.innerWidth * 0.7, Math.max(CHAT_MIN, w)));

function useChatW() {
  const [w, setW] = useState(() => {
    try {
      const v = Number(localStorage.getItem("gerdr.workChatW"));
      return v ? clampChatW(v) : CHAT_W;
    } catch {
      return CHAT_W;
    }
  });
  const save = (v: number) => {
    v = clampChatW(v);
    setW(v);
    try {
      localStorage.setItem("gerdr.workChatW", String(v));
    } catch {}
  };
  return [w, save] as const;
}

/** The chat column's left edge: drag to resize, double-click for the default. Same look as the sidebar's (SidebarEdge). */
function ChatEdge({ width, onWidth }: { width: number; onWidth: (w: number) => void }) {
  const start = (e: React.PointerEvent) => startColResize(e, (dx) => onWidth(width - dx));
  return (
    <div
      className={cn("absolute top-0 -left-1 bottom-0 w-2 px-[3px] pointer-coarse:-left-3 pointer-coarse:w-6 pointer-coarse:px-[11px]", COL_EDGE)}
      onPointerDown={start}
      onDoubleClick={() => onWidth(CHAT_W)}
      title="拖动调宽窄，双击还原"
      style={{ WebkitAppRegion: "no-drag" } as any}
    />
  );
}

/**
 * Code / overview / Work: gerdr's own switch, at the right of the left column's top row in all three (Chrome.tsx
 * SidebarHeader). The overview isn't a `mode` (it lays over Code: lib/overview.ts), but to the user it's a third view.
 */
export function ModeSwitch() {
  const mode = useStore((s) => s.mode);
  const overview = useStore((s) => s.overview.on);
  const cur = mode === "work" ? "work" : overview ? "overview" : "agents";
  const go = (m: typeof cur) => {
    setMode(m === "work" ? "work" : "agents");
    if (m !== "work") toggleOverview(m === "overview");
  };
  // Icons, not words: the project name next to it needs the room (the sidebar is 224px by default)
  const item = (m: typeof cur, label: string, Icon: typeof Code2) => (
    <button
      title={label}
      aria-label={label}
      className={cn(
        "flex h-6 w-7 items-center justify-center rounded-md",
        cur === m ? "bg-background text-foreground shadow-sm" : "text-muted-foreground hover:text-foreground",
      )}
      onClick={() => go(m)}
    >
      <Icon className="size-3.5" />
    </button>
  );
  return (
    <div className="flex shrink-0 gap-0.5 rounded-lg bg-muted p-0.5" style={{ WebkitAppRegion: "no-drag" } as any}>
      {item("agents", "Code", Code2)}
      {item("overview", `全部 agent 总览（${formatCombo(keyFor(get().config, "overview")[0] ?? "")}）`, LayoutGrid)}
      {item("work", "Work", Briefcase)}
    </div>
  );
}

// ---- the Work project (left) ----

function useDark() {
  const [dark, setDark] = useState(() => document.documentElement.classList.contains("dark"));
  useEffect(() => {
    const o = new MutationObserver(() => setDark(document.documentElement.classList.contains("dark")));
    o.observe(document.documentElement, { attributes: true, attributeFilter: ["class"] });
    return () => o.disconnect();
  }, []);
  return dark;
}

/** One project's Work site. Keyed by project by the caller, so switching projects loads the other site. */
function WorkFrame({ project }: { project: string }) {
  const ref = useRef<HTMLIFrameElement>(null);
  const dark = useDark();
  const send = (m: unknown) => ref.current?.contentWindow?.postMessage(m, location.origin);
  useEffect(() => send({ type: "gerdr:theme", dark }), [dark]);
  // The site's menu takes the sidebar's width (--gerdr-sidebar-w, its lib/gerdr.ts); dragged by SidebarEdge in WorkView
  const sidebarW = useStore((s) => s.sidebarW);
  useEffect(() => send({ type: "gerdr:sidebar", width: sidebarW }), [sidebarW]);
  useEffect(() => {
    const f = (e: MessageEvent) => {
      if (e.origin !== location.origin || e.source !== ref.current?.contentWindow) return;
      const m = e.data ?? {};
      if (m.type === "gerdr:ready") {
        send({ type: "gerdr:theme", dark: document.documentElement.classList.contains("dark") });
        send({ type: "gerdr:sidebar", width: get().sidebarW });
      } else if (m.type === "gerdr:ask" && typeof m.text === "string")
        askWorkAgent(m.text, project).then(
          () => toast.success("交给右边的助手了"),
          (err) => toast.error(err.message ?? String(err)),
        );
      else if (m.type === "gerdr:settings") set({ dialog: { kind: "settings", tab: typeof m.tab === "string" ? m.tab : undefined } });
    };
    window.addEventListener("message", f);
    return () => window.removeEventListener("message", f);
  }, [project]);
  return <iframe ref={ref} src={`/work/${encodeURIComponent(project)}/`} title="Work" className="size-full border-0 bg-background" />;
}

// ---- the agent on the right ----

function WorkChat() {
  const project = useWorkProject();
  const id = useWorkSession();
  const session = useStore((s) => (id ? s.sessions[id] : undefined));
  const ready = useStore((s) => s.ready);
  const [err, setErr] = useState("");
  useEffect(() => {
    syncWatch();
  }, [id]);
  useEffect(() => {
    if (ready && !session) ensureWorkSession().catch((e) => setErr(e.message ?? String(e)));
  }, [ready, session]);
  if (session) return <ChatView key={session.id} session={session} paneId={`${WORK_PANE}:${project}`} focused={false} />;
  return (
    <div className="flex h-full items-center justify-center gap-2 p-6 text-center text-[12px] text-muted-foreground">
      {err ? (
        <div className="space-y-2">
          <div className="text-bad">开助手失败：{err}</div>
          <Button size="sm" variant="outline" onClick={() => (setErr(""), ensureWorkSession().catch((e) => setErr(e.message)))}>
            重试
          </Button>
        </div>
      ) : (
        <>
          <PixelLoader className="size-4" /> 正在开助手…
        </>
      )}
    </div>
  );
}

function ago(iso: string) {
  const d = (Date.now() - new Date(iso).getTime()) / 1000;
  if (d < 3600) return `${Math.max(1, Math.round(d / 60))} 分钟前`;
  if (d < 86400) return `${Math.round(d / 3600)} 小时前`;
  return `${Math.floor(d / 86400)} 天前`;
}

function ChatHeader() {
  const chats = useWorkChats();
  const current = useWorkSession();
  const unread = useStore((s) => s.unread);
  const title = (t?: string) => (t && t !== "Work" ? t : "新对话");
  const cur = chats.find((c) => c.id === current);
  const fresh = () => newWorkChat().catch((e) => toast.error(e.message));
  const agents = useStore((s) => s.agents);
  useStore((s) => s.config.workAgents); // re-render when the default changes
  const defaultId = workAgent();
  return (
    <div className="flex h-10 shrink-0 items-center gap-1 border-b px-2" style={{ WebkitAppRegion: "drag" } as any}>
      <DropdownMenu>
        <DropdownMenuTrigger asChild>
          <Button variant="ghost" size="sm" className="h-7 min-w-0 gap-1.5 px-2 text-[12px]" style={{ WebkitAppRegion: "no-drag" } as any}>
            {cur ? <AgentIcon agent={cur.agent} className="size-3.5 shrink-0" /> : <History className="size-3.5 shrink-0 text-muted-foreground" />}
            <span className="truncate">{title(cur?.title)}</span>
            <ChevronDown className="size-3 shrink-0 text-muted-foreground" />
          </Button>
        </DropdownMenuTrigger>
        <DropdownMenuContent align="start" className="max-h-96 w-72 overflow-y-auto">
          {chats.map((c) => (
            <DropdownMenuItem key={c.id} onClick={() => openWorkChat(c.id)} className={cn(c.id === current && "bg-accent")}>
              <span className="min-w-0 flex-1 truncate">{title(c.title)}</span>
              <span className="shrink-0 text-[11px] text-muted-foreground">{ago(new Date(c.updatedAt).toISOString())}</span>
              <StatusDot status={c.status} unread={!!unread[c.id]} />
            </DropdownMenuItem>
          ))}
          {chats.length > 0 && <DropdownMenuSeparator />}
          <DropdownMenuLabel className="text-[11px] font-normal text-muted-foreground">新对话用</DropdownMenuLabel>
          {agents
            .filter((a) => a.available)
            .map((a) => (
              <DropdownMenuItem key={a.id} onClick={() => newWorkChat(a.id).catch((e) => toast.error(e.message))}>
                <AgentIcon agent={a.id} className="size-3.5" />
                <span className="flex-1">{a.name}</span>
                {a.id === defaultId && <Check className="text-muted-foreground" />}
              </DropdownMenuItem>
            ))}
          <DropdownMenuSeparator />
          <DropdownMenuItem onClick={() => set({ dialog: { kind: "settings", tab: "agents" } })}>
            <Settings /> 配置 agent（引擎、提示词、Skill、MCP、默认模型）…
          </DropdownMenuItem>
        </DropdownMenuContent>
      </DropdownMenu>
      <div className="flex-1" />
      <Button variant="ghost" size="icon" className="size-7" title="新对话" style={{ WebkitAppRegion: "no-drag" } as any} onClick={fresh}>
        <MessageSquarePlus className="size-3.5" />
      </Button>
    </div>
  );
}

/** Top bar over the Work project: gerdr's own controls (mode switch, settings), apart from the project's menu. */
function WorkBar({ narrow }: { narrow: boolean }) {
  // The first sidebarW px are the same header as the sidebar's (Chrome.tsx SidebarHeader), so the switch sits exactly where
  // it does in Code mode; the Work site's own menu below is the same width (gerdr:sidebar). Settings and the connection
  // status are in the bottom row gerdr lays over the site's menu (WorkView), as in the other two modes.
  // Narrow: the site has no side menu to line up with (useNarrow), so the header takes the whole width.
  const sidebarW = useStore((s) => s.sidebarW);
  return (
    <div className="flex h-10 shrink-0 border-b bg-sidebar" style={{ WebkitAppRegion: "drag" } as any}>
      <SidebarHeader className={narrow ? "flex-1 border-b-0" : "border-r border-b-0"} style={narrow ? undefined : { width: sidebarW }} />
    </div>
  );
}

/**
 * Whether the page column is narrower than the site's phone breakpoint (Tailwind's md, like gerdr's own PHONE_MAX):
 * a landscape phone or a small window with a wide chat. The site then drops its side menu for its phone layout (the
 * templates' layout.tsx), so gerdr's header and settings row, drawn sidebarW wide to sit on that menu, would stick out
 * over the page instead. It's the frame's width that counts, not the window's: the site's breakpoints see the frame.
 */
function useNarrow(ref: React.RefObject<HTMLDivElement | null>) {
  const [narrow, setNarrow] = useState(false);
  useEffect(() => {
    const el = ref.current;
    if (!el) return;
    const o = new ResizeObserver(() => setNarrow(el.clientWidth < PHONE_MAX));
    o.observe(el);
    return () => o.disconnect();
  }, []);
  return narrow;
}

export function WorkView() {
  const phone = usePhone();
  const sidebarW = useStore((s) => s.sidebarW);
  const project = useWorkProject();
  const [chatW, setChatW] = useChatW();
  const [side, setSide] = useState<"page" | "chat">("page");
  const col = useRef<HTMLDivElement>(null);
  const narrow = useNarrow(col);
  if (phone) {
    return (
      <div className="flex h-full flex-col bg-background">
        <div className="flex h-11 shrink-0 items-center gap-1 border-b bg-sidebar px-1.5">
          <Button variant="ghost" size="icon" className="size-9" aria-label="Code" onClick={() => setMode("agents")}>
            <Code2 className="size-4" />
          </Button>
          <div className="flex flex-1 justify-center">
            <div className="flex gap-0.5 rounded-lg bg-muted p-0.5">
              {(["page", "chat"] as const).map((v) => (
                <button
                  key={v}
                  className={cn("h-7 rounded-md px-3 text-[12px]", side === v ? "bg-background shadow-sm" : "text-muted-foreground")}
                  onClick={() => setSide(v)}
                >
                  {v === "page" ? "页面" : "助手"}
                </button>
              ))}
            </div>
          </div>
          <div className="size-9" />
        </div>
        {/* both stay mounted: switching tabs keeps the page's state and the chat's scroll */}
        <div className={cn("min-h-0 flex-1", side !== "page" && "hidden")}>
          <WorkFrame key={project} project={project} />
        </div>
        <div className={cn("min-h-0 flex-1", side !== "chat" && "hidden")}>
          <WorkChat />
        </div>
      </div>
    );
  }
  return (
    <div className="flex h-full bg-background">
      <div ref={col} className="relative flex min-w-0 flex-1 flex-col">
        <WorkBar narrow={narrow} />
        <div className="min-h-0 flex-1">
          <WorkFrame key={project} project={project} />
        </div>
        {/* Narrow: no side menu in the site to lay the settings row over or to resize, so the row goes under the page (useNarrow) */}
        {narrow ? (
          <SidebarFooter />
        ) : (
          // The sidebar's resize handle, over the top bar and the site's menu edge alike (the site lays its menu out at
          // the same width): it's ours, so it works whatever the site does, and captures the pointer so the frame can't take it
          <div className="pointer-events-none absolute inset-y-0 left-0 [&>*]:pointer-events-auto" style={{ width: sidebarW }}>
            <SidebarEdge />
            {/* Settings at the bottom of the left column, like the Code sidebar (asked for). The site keeps that strip free
              (--gerdr-menu-bottom in its index.css, used by its layout.tsx) */}
            <div className="absolute inset-x-0 bottom-0 border-r">
              <SidebarFooter />
            </div>
          </div>
        )}
      </div>
      {/* max-w: a window made smaller after dragging still leaves the Work site some room */}
      <div className="relative flex max-w-[70%] shrink-0 flex-col border-l" style={{ width: chatW }}>
        <ChatEdge width={chatW} onWidth={setChatW} />
        <ChatHeader />
        <div className="min-h-0 flex-1">
          <WorkChat />
        </div>
      </div>
    </div>
  );
}
