import { Briefcase, Check, ChevronDown, Code2, History, MessageSquarePlus, Settings } from "lucide-react";
import { useCallback, useEffect, useRef, useState } from "react";
import { toast } from "sonner";
import { AgentIcon, StatusDot } from "@/components/AgentIcon";
import { COL_EDGE, startColResize } from "@/components/SidebarChrome";
import { focusSession } from "@/lib/actions";
import { toggleOverview } from "@/lib/overview";
import { ChatView } from "@/components/chat/ChatView";
import { PixelLoader } from "@/components/chat/PixelLoader";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { api } from "@/lib/api";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { spaceList, type Layout } from "@/lib/layout";
import { usePhone } from "@/lib/phone";
import { set, setMode, useStore } from "@/lib/store";
import { syncWatch } from "@/lib/sync";
import { cn } from "@/lib/utils";
import { askWorkAgent, ensureWorkSession, isEmptyChat, newWorkChat, openWorkChat, useWorkChats, useWorkProject, useWorkSession, workAgent } from "@/lib/work";
import { t } from "@/lib/i18n";

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
      title={t("nav.dragResize")}
      style={{ WebkitAppRegion: "no-drag" } as any}
    />
  );
}

/**
 * Code / Work in the phone drawer's top row (SidebarChrome.tsx SidebarHeader); wider screens switch on the rail. The
 * active grid (the overview) isn't a view of its own here: the sidebar's 进行中 section opens it.
 */
export function ModeSwitch() {
  const mode = useStore((s) => s.mode);
  const cur = mode === "work" ? "work" : "agents";
  const go = (m: typeof cur) => {
    setMode(m);
    if (m !== "work") toggleOverview(false);
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

/**
 * What sites written for the old shared left column get on load. Their menu is `w-(--gerdr-sidebar-w)` with room left
 * at its bottom for gerdr's settings row (`--gerdr-menu-bottom`), and their lib/gerdr.ts sets both from this message.
 * gerdr lays nothing over the site any more (the rail took its header and settings row), so: a fixed menu width and no
 * strip to leave free. New sites don't use either variable.
 */
const LEGACY_SIDEBAR = { type: "gerdr:sidebar", width: 224, bottom: 0 };

/** One project's Work site, the whole space between the rail and the chat (WorkFrames keeps one per opened project). */
function WorkFrame({ project, onShowChat }: { project: string; onShowChat?: () => void }) {
  const ref = useRef<HTMLIFrameElement>(null);
  const [secretAsk, setSecretAsk] = useState<SecretAsk | null>(null);
  const dark = useDark();
  const send = (m: unknown) => ref.current?.contentWindow?.postMessage(m, location.origin);
  useEffect(() => send({ type: "gerdr:theme", dark }), [dark]);
  useEffect(() => {
    const f = (e: MessageEvent) => {
      if (e.origin !== location.origin || e.source !== ref.current?.contentWindow) return;
      const m = e.data ?? {};
      if (m.type === "gerdr:ready") {
        send({ type: "gerdr:theme", dark: document.documentElement.classList.contains("dark") });
        send(LEGACY_SIDEBAR);
      } else if (m.type === "gerdr:ask" && typeof m.text === "string") {
        // With a request id the page shows its own notice: answer it with the conversation (or the error) instead of a toast
        const rid = m.id;
        askWorkAgent(m.text, project).then(
          (session) => (rid != null ? send({ type: "gerdr:asked", id: rid, session }) : toast.success(t("workView.handedOff"))),
          (err) => {
            const error = err.message ?? String(err);
            if (rid != null) send({ type: "gerdr:asked", id: rid, error });
            else toast.error(error);
          },
        );
      } else if (m.type === "gerdr:openChat" && typeof m.session === "string") {
        openWorkChat(m.session);
        onShowChat?.();
      } else if (m.type === "gerdr:openSession" && typeof m.session === "string") {
        // A conversation a local function started (ctx.sessions) lives in Code, not in this chat: go to its pane
        toggleOverview(false);
        setMode("agents");
        focusSession(m.session);
      } else if (m.type === "gerdr:settings") set({ dialog: { kind: "settings", tab: typeof m.tab === "string" ? m.tab : undefined } });
      else if (m.type === "gerdr:secret" && typeof m.name === "string") {
        // Asked for by the page, typed into gerdr's own dialog: the value goes straight to secrets.json and never
        // passes through the page (code an agent wrote) or a model
        setSecretAsk({ id: m.id, name: m.name, label: typeof m.label === "string" ? m.label : "", note: typeof m.note === "string" ? m.note : "" });
      }
    };
    window.addEventListener("message", f);
    return () => window.removeEventListener("message", f);
  }, [project, onShowChat]);
  return (
    <>
      <iframe ref={ref} src={`/work/${encodeURIComponent(project)}/`} title="Work" className="size-full border-0 bg-background" />
      <SecretDialog
        ask={secretAsk}
        onDone={(saved, error) => {
          if (secretAsk) send({ type: "gerdr:secretDone", id: secretAsk.id, saved, error });
          setSecretAsk(null);
        }}
      />
    </>
  );
}

type SecretAsk = { id: unknown; name: string; label: string; note: string };

/** The Work page's secret() call: one password field, saved as a ctx.secrets key. */
function SecretDialog({ ask, onDone }: { ask: SecretAsk | null; onDone: (saved: boolean, error?: string) => void }) {
  const [value, setValue] = useState("");
  const [err, setErr] = useState("");
  const [busy, setBusy] = useState(false);
  useEffect(() => {
    setValue("");
    setErr("");
  }, [ask]);
  const bad = !!ask && (!ask.name || /[\s/]/.test(ask.name));
  const save = async () => {
    if (!ask || !value || bad) return;
    setBusy(true);
    try {
      await api.setWorkSecret(ask.name, value);
      onDone(true);
    } catch (e: any) {
      setErr(e.message ?? String(e));
    } finally {
      setBusy(false);
    }
  };
  return (
    <Dialog open={!!ask} onOpenChange={(o) => !o && onDone(false)}>
      <DialogContent className="sm:max-w-md">
        <DialogHeader>
          <DialogTitle>{ask?.label || t("workView.secretTitle")}</DialogTitle>
          <DialogDescription>{t("workView.secretDesc", { name: ask?.name ?? "" })}</DialogDescription>
        </DialogHeader>
        {ask?.note && <div className="whitespace-pre-wrap text-[12px] text-muted-foreground">{ask.note}</div>}
        <form
          onSubmit={(e) => {
            e.preventDefault();
            void save();
          }}
        >
          <Input type="password" autoFocus autoComplete="off" value={value} onChange={(e) => setValue(e.target.value)} disabled={bad} />
        </form>
        {(err || bad) && <div className="text-[12px] text-bad">{bad ? t("workView.secretBadName", { name: ask?.name ?? "" }) : err}</div>}
        <DialogFooter>
          <Button variant="outline" onClick={() => onDone(false)}>
            {t("common.cancel")}
          </Button>
          <Button onClick={() => void save()} disabled={!value || busy || bad}>
            {t("workView.secretSave")}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

/**
 * Every project's Work site opened in this page, the current one shown: switching projects only hides the frame, so
 * coming back finds the site as it was (an iframe can't be re-parented or remounted without reloading — reported: the
 * project's Work page lost its state on every switch). A project's frame is first loaded when it's seen in Work mode
 * (in Code mode this view is only kept mounted, nobody looks at it), and dropped when the project is closed. Appended in
 * the order they're opened and never reordered: React moving an iframe in the DOM reloads it too.
 */
function WorkFrames({ onShowChat }: { onShowChat?: () => void }) {
  const project = useWorkProject();
  const shown = useStore((s) => s.mode === "work");
  const spaces = useStore((s) => s.layout.spaces);
  const workspaces = useStore((s) => s.layout.workspaces);
  const [kept, setKept] = useState<string[]>([]);
  const live = new Set(spaceList({ spaces, workspaces } as Layout).map((s) => s.id));
  const list = kept.filter((p) => live.has(p));
  if (shown && !list.includes(project)) list.push(project);
  const key = list.join("\n");
  useEffect(() => {
    setKept((k) => (k.join("\n") === key ? k : key.split("\n").filter(Boolean)));
  }, [key]);
  return (
    <div className="relative size-full">
      {list.map((p) => (
        <div key={p} className={cn("absolute inset-0", p !== project && "pointer-events-none invisible")} aria-hidden={p !== project}>
          <WorkFrame project={p} onShowChat={onShowChat} />
        </div>
      ))}
    </div>
  );
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
          <div className="text-bad">{t("workView.openFailed", { error: err })}</div>
          <Button size="sm" variant="outline" onClick={() => (setErr(""), ensureWorkSession().catch((e) => setErr(e.message)))}>
            {t("common.retry")}
          </Button>
        </div>
      ) : (
        <>
          <PixelLoader className="size-4" /> {t("workView.opening")}
        </>
      )}
    </div>
  );
}

function ago(iso: string) {
  const d = (Date.now() - new Date(iso).getTime()) / 1000;
  if (d < 3600) return t("nav.minutesAgo", { n: Math.max(1, Math.round(d / 60)) });
  if (d < 86400) return t("nav.hoursAgo", { n: Math.round(d / 3600) });
  return t("nav.daysAgo", { n: Math.floor(d / 86400) });
}

/**
 * The chat column's header. Two separate jobs, kept apart so neither looks like the other: the title is the history
 * (switch to an earlier conversation), the split button on the right starts a new one — its main half with the
 * agent it names, its arrow with another (which then becomes the default for this project's next ones).
 */
function ChatHeader() {
  const all = useWorkChats();
  const current = useWorkSession();
  const unread = useStore((s) => s.unread);
  const title = (s?: string) => (s && s !== "Work" ? s : t("workView.newChat"));
  const cur = all.find((c) => c.id === current);
  // Untouched conversations would all read "新对话" with nothing to tell them apart; the current one stays so it's marked
  const chats = all.filter((c) => c.id === current || !isEmptyChat(c));
  const agents = useStore((s) => s.agents);
  useStore((s) => s.config.workAgents); // re-render when the default changes
  const defaultId = workAgent();
  const defaultName = agents.find((a) => a.id === defaultId)?.name ?? defaultId;
  // Kept the current, still-empty chat (newWorkChat): say so, or the button looks like it did nothing
  const fresh = (agent?: string) =>
    newWorkChat(agent).then(
      (id) => id === current && toast(t("workView.alreadyNew")),
      (e) => toast.error(e.message),
    );
  const noDrag = { WebkitAppRegion: "no-drag" } as any;
  return (
    <div className="flex h-10 shrink-0 items-center gap-1 border-b px-2" style={{ WebkitAppRegion: "drag" } as any}>
      <DropdownMenu>
        <DropdownMenuTrigger asChild>
          <Button variant="ghost" size="sm" className="h-7 min-w-0 gap-1.5 px-2 text-[12px]" title={t("workView.history")} style={noDrag}>
            {cur ? <AgentIcon agent={cur.agent} className="size-3.5 shrink-0" /> : <History className="size-3.5 shrink-0 text-muted-foreground" />}
            <span className="truncate">{title(cur?.title)}</span>
            <ChevronDown className="size-3 shrink-0 text-muted-foreground" />
          </Button>
        </DropdownMenuTrigger>
        <DropdownMenuContent align="start" className="max-h-96 w-72 overflow-y-auto">
          <DropdownMenuLabel className="text-[11px] font-normal text-muted-foreground">{t("workView.history")}</DropdownMenuLabel>
          {chats.length === 0 && <DropdownMenuItem disabled>{t("workView.noChats")}</DropdownMenuItem>}
          {chats.map((c) => (
            <DropdownMenuItem key={c.id} onClick={() => openWorkChat(c.id)}>
              <AgentIcon agent={c.agent} className="size-3.5 shrink-0" />
              <span className="min-w-0 flex-1 truncate">{title(c.title)}</span>
              <span className="shrink-0 text-[11px] text-muted-foreground">{ago(new Date(c.updatedAt).toISOString())}</span>
              {/* the current one is the chat below, its status is in plain sight: the slot marks it instead */}
              {c.id === current ? <Check className="size-3.5 shrink-0 text-muted-foreground" /> : <StatusDot status={c.status} unread={!!unread[c.id]} />}
            </DropdownMenuItem>
          ))}
        </DropdownMenuContent>
      </DropdownMenu>
      <div className="flex-1" />
      <div className="flex items-center" style={noDrag}>
        {/* Touch: bigger targets (the header is h-10, so up to 36px fits) */}
        <Button
          variant="ghost"
          size="icon"
          className="size-7 rounded-r-none pointer-coarse:size-9"
          title={t("workView.newChatWith", { name: defaultName })}
          aria-label={t("workView.newChat")}
          onClick={() => fresh()}
        >
          <MessageSquarePlus className="size-3.5 pointer-coarse:size-4" />
        </Button>
        <DropdownMenu>
          <DropdownMenuTrigger asChild>
            <Button
              variant="ghost"
              size="icon"
              className="h-7 w-4 rounded-l-none text-muted-foreground pointer-coarse:h-9 pointer-coarse:w-6"
              title={t("workView.newChatOtherAgent")}
            >
              <ChevronDown className="size-3" />
            </Button>
          </DropdownMenuTrigger>
          <DropdownMenuContent align="end" className="w-64">
            <DropdownMenuLabel className="text-[11px] font-normal text-muted-foreground">{t("workView.startNewChat")}</DropdownMenuLabel>
            {agents.map((a) => (
              <DropdownMenuItem key={a.id} disabled={!a.available} title={a.available ? undefined : a.error} onClick={() => fresh(a.id)}>
                <AgentIcon agent={a.id} className="size-3.5" />
                <span className="flex-1">{a.name}</span>
                {!a.available ? (
                  <span className="text-[11px] text-muted-foreground">{t("workView.unavailable")}</span>
                ) : (
                  a.id === defaultId && <span className="text-[11px] text-muted-foreground">{t("common.default")}</span>
                )}
              </DropdownMenuItem>
            ))}
            <DropdownMenuSeparator />
            <DropdownMenuItem onClick={() => set({ dialog: { kind: "settings", tab: "agents" } })}>
              <Settings /> {t("workView.configureAgents")}
            </DropdownMenuItem>
          </DropdownMenuContent>
        </DropdownMenu>
      </div>
    </div>
  );
}

export function WorkView() {
  const phone = usePhone();
  const [chatW, setChatW] = useChatW();
  const [side, setSide] = useState<"page" | "chat">("page");
  const showChat = useCallback(() => setSide("chat"), []);
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
                  {v === "page" ? t("workView.page") : t("workView.assistant")}
                </button>
              ))}
            </div>
          </div>
          <div className="size-9" />
        </div>
        {/* both stay mounted: switching tabs keeps the page's state and the chat's scroll */}
        <div className={cn("min-h-0 flex-1", side !== "page" && "hidden")}>
          <WorkFrames onShowChat={showChat} />
        </div>
        {/* The same header as on wider screens: without it a phone has no history, no new chat and no agent picker */}
        <div className={cn("flex min-h-0 flex-1 flex-col", side !== "chat" && "hidden")}>
          <ChatHeader />
          <div className="min-h-0 flex-1">
            <WorkChat />
          </div>
        </div>
      </div>
    );
  }
  return (
    // The rail (Rail.tsx) is left of this: the site gets everything up to the chat, nothing of gerdr's laid over it
    <div className="flex h-full bg-background">
      <div className="min-w-0 flex-1">
        <WorkFrames />
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
