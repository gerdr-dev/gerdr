import { Briefcase, Check, ChevronDown, Code2, History, MessageSquarePlus, Settings } from "lucide-react";
import { useEffect, useRef, useState } from "react";
import { toast } from "sonner";
import { AgentIcon, StatusDot } from "@/components/AgentIcon";
import { SpaceSwitcher } from "@/components/Chrome";
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
import { usePhone } from "@/lib/phone";
import { set, setMode, useStore } from "@/lib/store";
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

/** Code / Work: gerdr's own switch, at the right of the top row's first 224px in both modes (sidebar header / Work top bar). */
export function ModeSwitch() {
  const mode = useStore((s) => s.mode);
  // Icons, not words: the project name next to it needs the room (the sidebar is 224px)
  const item = (m: "agents" | "work", label: string, Icon: typeof Code2) => (
    <button
      title={label}
      aria-label={label}
      className={cn(
        "flex h-6 w-7 items-center justify-center rounded-md",
        mode === m ? "bg-background text-foreground shadow-sm" : "text-muted-foreground hover:text-foreground",
      )}
      onClick={() => setMode(m)}
    >
      <Icon className="size-3.5" />
    </button>
  );
  return (
    <div className="flex shrink-0 gap-0.5 rounded-lg bg-muted p-0.5" style={{ WebkitAppRegion: "no-drag" } as any}>
      {item("agents", "Code", Code2)}
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
  useEffect(() => {
    const f = (e: MessageEvent) => {
      if (e.origin !== location.origin || e.source !== ref.current?.contentWindow) return;
      const m = e.data ?? {};
      if (m.type === "gerdr:ready") send({ type: "gerdr:theme", dark: document.documentElement.classList.contains("dark") });
      else if (m.type === "gerdr:ask" && typeof m.text === "string")
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
function WorkBar() {
  // The first 224px repeat the sidebar header (Chrome.tsx Sidebar: project, then Code / Work, then one icon button), so the
  // switch sits exactly where it does in Code mode; the Work site's own menu below is the same width.
  return (
    <div className="flex h-10 shrink-0 border-b bg-sidebar" style={{ WebkitAppRegion: "drag" } as any}>
      <div className="flex w-56 shrink-0 items-center gap-1 border-r px-2">
        <div className="native-traffic-light-gap" />
        <div className="flex min-w-16 flex-1">
          <SpaceSwitcher />
        </div>
        <ModeSwitch />
        <Button
          variant="ghost"
          size="icon"
          className="size-7 text-muted-foreground"
          title="设置"
          style={{ WebkitAppRegion: "no-drag" } as any}
          onClick={() => set({ dialog: { kind: "settings" } })}
        >
          <Settings className="size-4" />
        </Button>
      </div>
    </div>
  );
}

export function WorkView() {
  const phone = usePhone();
  const project = useWorkProject();
  const [side, setSide] = useState<"page" | "chat">("page");
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
      <div className="flex min-w-0 flex-1 flex-col">
        <WorkBar />
        <div className="min-h-0 flex-1">
          <WorkFrame key={project} project={project} />
        </div>
      </div>
      <div className="flex w-[min(440px,40%)] shrink-0 flex-col border-l">
        <ChatHeader />
        <div className="min-h-0 flex-1">
          <WorkChat />
        </div>
      </div>
    </div>
  );
}
