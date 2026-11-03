import { ArrowDownRight, Check, ChevronDown, Code2, History, MessageSquarePlus, Settings, X } from "lucide-react";
import { useCallback, useEffect, useRef, useState, useSyncExternalStore } from "react";
import { createRoot, type Root } from "react-dom/client";
import { toast } from "sonner";
import { AgentIcon, StatusDot, dotStatus } from "@/components/AgentIcon";
import { COL_EDGE, startColResize } from "@/components/SidebarChrome";
import { toggleOverview } from "@/lib/overview";
import { ChatView } from "@/components/chat/ChatView";
import { cardOf } from "@/components/chat/ToolCard";
import type { GPart } from "@gerdr/chat";
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
import { openInCode } from "@/core/sessions";
import { useDark } from "@/core/theme";
import { mountName, registerMount, useMount } from "@/lib/mounts";
import { askSecret, secretAsks } from "@/lib/workSecrets";
import { mountInfo } from "@/lib/mounts";
import { PanelButtons } from "@/components/PanelButtons";
import { panelOfKey, spaceOfKey } from "@/lib/work";
import { askWorkAgent, ensureWorkSession, isEmptyChat, newWorkChat, openWorkChat, useWorkChats, useWorkProject, useWorkSession, workAgent } from "@/lib/work";
import { t } from "@/lib/i18n";

// Work mode: the user's own Work project on the left, one agent on the right.
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
      <PanelButtons
        size="row"
        className={(on) =>
          cn(
            "flex h-6 w-7 items-center justify-center rounded-md",
            on ? "bg-background text-foreground shadow-sm" : "text-muted-foreground hover:text-foreground",
          )
        }
      />
    </div>
  );
}

// ---- the Work project (left) ----

/**
 * What sites written for the old shared left column get on load. Their menu is `w-(--gerdr-sidebar-w)` with room left
 * at its bottom for gerdr's settings row (`--gerdr-menu-bottom`), and their lib/gerdr.ts sets both from this message.
 * gerdr lays nothing over the site any more (the rail took its header and settings row), so: a fixed menu width and no
 * strip to leave free. New sites don't use either variable.
 */
const LEGACY_SIDEBAR = { type: "gerdr:sidebar", width: 224, bottom: 0 };

/**
 * Messages to the page's lib/gerdr.ts. The page used to be a frame; it is now part of this document (a mounted root),
 * where its window.parent is this window, so the bridge it was written for still works unchanged: both sides post to
 * and listen on the same window, and tell their messages apart by type. New pages call @gerdr/core instead.
 */
const toPage = (m: unknown) => window.postMessage(m, location.origin);

// Each mounted project's page, for WorkPeek to tell it what happened to the panel it asked for. They all listen on
// the one window now, and the bridge's messages name the session, so this is the same for every project.
const frameSend = new Map<string, (m: unknown) => void>();

const pathKey = (project: string) => `gerdr.workPath.${project}`;

/**
 * One project's Work UI as a React root of its own inside this page (internal/uisite/mounts.go compiles it into the
 * page's modules). Its own root, not a child of gerdr's tree: a page's render loop or crash stays in it, and it keeps
 * its router (a memory router: the address bar is gerdr's) and the path it was on (per project, in localStorage).
 * Rendered again only when its update couldn't be hot-replaced (useMount's seq).
 */
function MountRoot({ project }: { project: string }) {
  const ref = useRef<HTMLDivElement>(null);
  const { info, seq } = useMount(project);
  const entry = useRef(info?.entry);
  entry.current = info?.entry;
  const [failed, setFailed] = useState("");
  const ok = !!info?.entry;
  useEffect(() => {
    const el = ref.current;
    const url = entry.current;
    if (!el || !url) return;
    let root: Root | null = null;
    let dead = false;
    setFailed("");
    (async () => {
      await registerMount(project);
      const app = await import(/* @vite-ignore */ url);
      if (dead) return;
      let path = "/";
      try {
        path = localStorage.getItem(pathKey(project)) || "/";
      } catch {}
      root = createRoot(el);
      root.render(
        app.createApp({
          path,
          onNavigate: (p: string) => {
            try {
              localStorage.setItem(pathKey(project), p);
            } catch {}
          },
        }),
      );
    })().catch((e) => !dead && setFailed(String(e?.stack ?? e)));
    return () => {
      dead = true;
      const r = root;
      // Not during React's own render of this tree: unmounting another root synchronously from a cleanup warns
      if (r) setTimeout(() => r.unmount());
    };
  }, [project, seq, ok]);
  const error = info?.error || failed;
  return (
    <div className="relative size-full overflow-hidden bg-background" {...{ [MOUNT_ATTR]: mountName(project) }}>
      <div ref={ref} className={cn("size-full", error && "hidden")} />
      {error && <MountError project={project} error={error} />}
      {!info && (
        <div className="flex size-full items-center justify-center">
          <PixelLoader className="size-4" />
        </div>
      )}
    </div>
  );
}

const MOUNT_ATTR = "data-gerdr-mount"; // uisite.MountAttr: the page's scoped CSS matches it

/** The page doesn't build (or threw while starting): the error in its own area, with the agent a click away. */
function MountError({ project, error }: { project: string; error: string }) {
  return (
    <div className="size-full overflow-auto p-6">
      <div className="mb-3 text-[13px] text-muted-foreground">{t("workView.pageBroken")}</div>
      <pre className="font-mono text-[12px] leading-relaxed whitespace-pre-wrap text-bad">{error}</pre>
      <Button
        size="sm"
        variant="outline"
        className="mt-4"
        onClick={() =>
          askWorkAgent(t("workView.fixPagePrompt", { error: error.slice(0, 4000) }), project).then(
            () => toast.success(t("workView.handedOff")),
            (e) => toast.error(e.message),
          )
        }
      >
        {t("workView.askFix")}
      </Button>
    </div>
  );
}

/** One project's Work UI, the whole space between the rail and the chat (WorkFrames keeps one per opened project). */
function WorkFrame({ project, active, onShowChat }: { project: string; active: boolean; onShowChat?: () => void }) {
  const dark = useDark();
  useEffect(() => {
    frameSend.set(project, toPage);
    return () => void frameSend.delete(project);
  }, [project]);
  useEffect(() => toPage({ type: "gerdr:theme", dark }), [dark]);
  useEffect(() => {
    // Every mounted page posts on this window: only the one on screen is the one the user is using
    if (!active) return;
    const f = (e: MessageEvent) => {
      if (e.origin !== location.origin || e.source !== window) return;
      const m = e.data ?? {};
      if (m.type === "gerdr:ready") {
        toPage({ type: "gerdr:theme", dark: document.documentElement.classList.contains("dark") });
        toPage(LEGACY_SIDEBAR);
      } else if (m.type === "gerdr:ask" && typeof m.text === "string") {
        // With a request id the page shows its own notice: answer it with the conversation (or the error) instead of a toast
        const rid = m.id;
        askWorkAgent(m.text, project).then(
          (session) => (rid != null ? toPage({ type: "gerdr:asked", id: rid, session }) : toast.success(t("workView.handedOff"))),
          (err) => {
            const error = err.message ?? String(err);
            if (rid != null) toPage({ type: "gerdr:asked", id: rid, error });
            else toast.error(error);
          },
        );
      } else if (m.type === "gerdr:openChat" && typeof m.session === "string") {
        openWorkChat(m.session);
        onShowChat?.();
      } else if (m.type === "gerdr:showSession" && typeof m.session === "string") {
        // Over the page, not in Code: the user answers a worker (with images / files) without leaving the board.
        // Asked again for the one already shown: the page updating its line / buttons (the task moved on).
        const actions = Array.isArray(m.actions)
          ? m.actions
              .filter((a: any) => a && typeof a.id === "string" && typeof a.label === "string")
              .map((a: any) => ({
                id: a.id,
                label: a.label,
                kind: a.kind === "primary" || a.kind === "danger" ? a.kind : undefined,
                confirm: typeof a.confirm === "string" ? a.confirm : undefined,
                jump: typeof a.jump === "string" ? a.jump : undefined,
              }))
          : undefined;
        const items = Array.isArray(m.items)
          ? m.items
              .filter((x: any) => x && typeof x.text === "string")
              .map((x: any) => ({
                text: x.text,
                jump: typeof x.jump === "string" ? x.jump : undefined,
                action: typeof x.action === "string" ? x.action : undefined,
              }))
          : undefined;
        set({ workPeek: { id: m.session, project, label: typeof m.label === "string" ? m.label : undefined, actions, items } });
      } else if (m.type === "gerdr:escape") {
        // An Esc the page didn't use (its bridge forwards them): closes the panel over it, like a drawer
        if (useStore.getState().workPeek?.project === project) set({ workPeek: null });
      } else if (m.type === "gerdr:hideSession") {
        if (useStore.getState().workPeek?.project === project) set({ workPeek: null });
      } else if (m.type === "gerdr:openSession" && typeof m.session === "string") {
        // A conversation a local function started (ctx.sessions) lives in Code, not in this chat: go to its pane
        openInCode(m.session);
      } else if (m.type === "gerdr:settings") set({ dialog: { kind: "settings", tab: typeof m.tab === "string" ? m.tab : undefined } });
      else if (m.type === "gerdr:secret" && typeof m.name === "string") {
        // Typed into gerdr's own dialog: the value goes straight to secrets.json and never passes through the page
        const id = m.id;
        askSecret(m.name, { label: typeof m.label === "string" ? m.label : "", note: typeof m.note === "string" ? m.note : "" }).then(
          (saved) => toPage({ type: "gerdr:secretDone", id, saved }),
          (e) => toPage({ type: "gerdr:secretDone", id, saved: false, error: e.message }),
        );
      }
    };
    window.addEventListener("message", f);
    return () => window.removeEventListener("message", f);
  }, [project, active, onShowChat]);
  return <MountRoot project={project} />;
}

/** The secret a page asked for (lib/workSecrets.ts): one password field, saved as a ctx.secrets key. */
function SecretPrompt() {
  const ask = useSyncExternalStore(secretAsks.subscribe, secretAsks.current);
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
      ask.done(true);
    } catch (e: any) {
      setErr(e.message ?? String(e));
    } finally {
      setBusy(false);
    }
  };
  return (
    <Dialog open={!!ask} onOpenChange={(o) => !o && ask?.done(false)}>
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
          <Button variant="outline" onClick={() => ask?.done(false)}>
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
 * Every project's Work UI opened in this page, the current one shown: switching projects only hides it, so coming back
 * finds the page as it was. A project's UI is first rendered when it's seen in Work mode (in Code mode this view is only
 * kept mounted, nobody looks at it), and dropped when the project is closed.
 */
function WorkFrames({ onShowChat }: { onShowChat?: () => void }) {
  const project = useWorkProject();
  const shown = useStore((s) => s.mode === "work");
  const spaces = useStore((s) => s.layout.spaces);
  const workspaces = useStore((s) => s.layout.workspaces);
  const [kept, setKept] = useState<string[]>([]);
  const live = new Set(spaceList({ spaces, workspaces } as Layout).map((s) => s.id));
  const list = kept.filter((p) => live.has(spaceOfKey(p)) && (!panelOfKey(p) || mountInfo(p)));
  if (shown && !list.includes(project)) list.push(project);
  const key = list.join("\n");
  useEffect(() => {
    setKept((k) => (k.join("\n") === key ? k : key.split("\n").filter(Boolean)));
  }, [key]);
  return (
    <div className="relative size-full">
      {list.map((p) => (
        <div key={p} className={cn("absolute inset-0", p !== project && "pointer-events-none invisible")} aria-hidden={p !== project}>
          <WorkFrame project={p} active={p === project && shown} onShowChat={onShowChat} />
        </div>
      ))}
      <WorkPeek />
      <SecretPrompt />
    </div>
  );
}

/**
 * The conversation the page asked to show (showSession): a panel over the right of the site with the whole chat, so
 * answering a worker takes images and files like anywhere else. Its own pane id: the draft is separate from the one in
 * the session's real pane.
 */
function WorkPeek() {
  const peek = useStore((s) => s.workPeek);
  const id = peek?.id;
  const session = useStore((s) => (s.workPeek ? s.sessions[s.workPeek.id] : undefined));
  const ready = useStore((s) => s.ready);
  const [armed, setArmed] = useState("");
  // A button with jump scrolls the chat to that card (ToolCard ref) instead of telling the page
  const [focus, setFocus] = useState<{ match: (p: GPart) => boolean; seq: number }>();
  const project = peek?.project;
  useEffect(() => {
    syncWatch();
    setArmed("");
    // However it goes away (the close button, ⌘W in runAction, the page showing another one): tell the page
    if (id && project) return () => frameSend.get(project)?.({ type: "gerdr:sessionHidden", session: id });
  }, [id, project]);
  if (!peek || !id) return null;
  const tell = (m: Record<string, unknown>) => frameSend.get(peek.project)?.({ ...m, session: id });
  const close = () => set({ workPeek: null });
  return (
    <>
      {/* A drawer: a click anywhere outside it closes it. */}
      <div className="absolute inset-0 z-20 bg-black/20" onClick={close} aria-hidden />
      <PeekEsc onEsc={close} />
      <aside className="absolute inset-y-0 right-0 z-30 flex w-[min(640px,100%)] flex-col border-l bg-background shadow-xl" aria-label={session?.title || id}>
        <div className="flex h-10 shrink-0 items-center gap-2 border-b px-3">
          {session && <StatusDot status={dotStatus(session)} />}
          <span className="min-w-0 flex-1 truncate text-[13px] font-medium">{session?.title || id}</span>
          <Button
            variant="ghost"
            size="icon"
            className="size-7"
            title={t("workView.peekInCode")}
            onClick={() => {
              close();
              openInCode(id);
            }}
          >
            <Code2 className="size-4" />
          </Button>
          <Button variant="ghost" size="icon" className="size-7" title={t("common.close")} onClick={close}>
            <X className="size-4" />
          </Button>
        </div>
        {(peek.label || peek.actions?.length || peek.items?.length) && (
          <div className="shrink-0 space-y-1 border-b bg-muted/40 px-3 py-1.5">
            <div className="flex flex-wrap items-center gap-2">
              {/* Wraps (up to 4 lines): a short line about the conversation; what's waiting goes in items below */}
              {peek.label && (
                <span className="line-clamp-4 min-w-0 flex-1 text-[12px] whitespace-pre-wrap text-muted-foreground" title={peek.label}>
                  {peek.label}
                </span>
              )}
              <div className="ml-auto flex max-w-full flex-wrap justify-end gap-1.5">
                {peek.actions?.map((a) => (
                  <Button
                    key={a.id}
                    size="sm"
                    variant={a.kind === "primary" ? "default" : "ghost"}
                    title={a.label}
                    // A page may name what a button does in full ("决定 t21：…"): capped, the end fades (index.css truncate)
                    className={cn("h-7 max-w-[20rem] min-w-0 text-[12px]", (a.kind === "danger" || armed === a.id) && "text-bad hover:text-bad")}
                    onClick={() => {
                      if (a.jump) {
                        const ref = a.jump;
                        return setFocus((f) => ({ match: (p) => cardOf(p)?.ref === ref, seq: (f?.seq ?? 0) + 1 }));
                      }
                      // A confirm text: first click arms it (the label becomes the question), the second does it
                      if (a.confirm && armed !== a.id) return setArmed(a.id);
                      setArmed("");
                      tell({ type: "gerdr:sessionAction", action: a.id });
                    }}
                  >
                    <span className="min-w-0 truncate">{armed === a.id ? a.confirm : a.label}</span>
                  </Button>
                ))}
              </div>
            </div>
            {/* One line per thing waiting on the user: text that fades at the end, a jump icon; the whole row is the target */}
            {peek.items?.map((it, n) => (
              <button
                key={n}
                title={it.text}
                onClick={() => {
                  if (it.jump) {
                    const ref = it.jump;
                    return setFocus((f) => ({ match: (p) => cardOf(p)?.ref === ref, seq: (f?.seq ?? 0) + 1 }));
                  }
                  if (it.action) tell({ type: "gerdr:sessionAction", action: it.action });
                }}
                className="flex w-full items-center gap-2 rounded-md border-l-2 border-l-warn bg-warn/5 py-1.5 pr-1.5 pl-2 text-left text-[12px] hover:bg-warn/10"
              >
                <span className="min-w-0 flex-1 truncate">{it.text}</span>
                <ArrowDownRight className="size-3.5 shrink-0 text-warn" />
              </button>
            ))}
          </div>
        )}
        <div className="min-h-0 flex-1">
          {session ? (
            <ChatView key={id} session={session} paneId={`work-peek:${id}`} focused focus={focus} />
          ) : (
            <div className="flex h-full items-center justify-center text-[12px] text-muted-foreground">
              {ready ? t("workView.peekGone") : <PixelLoader className="size-4" />}
            </div>
          )}
        </div>
      </aside>
    </>
  );
}

/**
 * Esc closes the panel, unless something in it took the key first: the composer stopping a running agent, its @ menu,
 * a focused image, a dialog (they preventDefault / stopPropagation). Esc pressed in the page comes as gerdr:escape.
 */
function PeekEsc({ onEsc }: { onEsc: () => void }) {
  useEffect(() => {
    const f = (e: KeyboardEvent) => {
      if (e.key !== "Escape" || e.defaultPrevented || useStore.getState().dialog) return;
      onEsc();
    };
    window.addEventListener("keydown", f);
    return () => window.removeEventListener("keydown", f);
  }, [onEsc]);
  return null;
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
  // A page that draws its own chats (ui.json "chat": false) gets the whole width; the Work agent is still one call away
  const project = useWorkProject();
  const { info } = useMount(project);
  const ownChat = info?.config?.chat === false;
  const [chatW, setChatW] = useChatW();
  const [side, setSide] = useState<"page" | "chat">("page");
  const showChat = useCallback(() => setSide("chat"), []);
  if (ownChat && !phone)
    return (
      <div className="h-full bg-background">
        <WorkFrames />
      </div>
    );
  if (phone) {
    return (
      <div className="flex h-full flex-col bg-background">
        <div className="flex h-11 shrink-0 items-center gap-1 border-b bg-sidebar px-1.5">
          <Button variant="ghost" size="icon" className="size-9" aria-label="Code" onClick={() => setMode("agents")}>
            <Code2 className="size-4" />
          </Button>
          <div className={cn("flex flex-1 justify-center", ownChat && "invisible")}>
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
        <div className={cn("min-h-0 flex-1", side !== "page" && !ownChat && "hidden")}>
          <WorkFrames onShowChat={showChat} />
        </div>
        {/* The same header as on wider screens: without it a phone has no history, no new chat and no agent picker */}
        <div className={cn("flex min-h-0 flex-1 flex-col", (side !== "chat" || ownChat) && "hidden")}>
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
