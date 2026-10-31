import { ChevronUp, Folder, FolderGit2, History, Loader2, Play, Settings2, SquareTerminal } from "lucide-react";
import { useEffect, useMemo, useRef, useState } from "react";
import { toast } from "sonner";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Popover, PopoverAnchor, PopoverContent } from "@/components/ui/popover";
import { closeFocusedPane, createInPane, focusPane, focusSession, openTerminal, resumeInPane } from "@/lib/actions";
import { api, ApiError } from "@/lib/api";
import { get, set, useStore, type AgentDef } from "@/lib/store";
import { locateSession, type PaneLeaf } from "@/lib/layout";
import { cn } from "@/lib/utils";
import { workProjectOf } from "@/lib/work";
import { AgentIcon, StatusDot } from "./AgentIcon";

function tilde(p: string, home: string) {
  return home && p.startsWith(home) ? "~" + p.slice(home.length) : p;
}

const expand = (v: string, home: string) => (v.startsWith("~") && home ? home + v.slice(1) : v);
const trimSlash = (p: string) => p.replace(/\/+$/, "") || "/";
/** A bare word (no leading / or ~) isn't a path: it searches the directories already used. */
const isQuery = (v: string) => !!v && !v.startsWith("/") && !v.startsWith("~");

/**
 * Creates the directory (and its parents) if it isn't there: starting a session or a workspace in a path that doesn't
 * exist yet just makes it, the picker says so beforehand. A backend from before /api/fs/mkdir answers 404: carry on,
 * opening the session then fails with its own message.
 */
export async function ensureDir(path: string) {
  if (!path.startsWith("/")) return;
  await api.mkdir(path).catch((e) => {
    if (!(e instanceof ApiError && e.status === 404)) throw e;
  });
}

type Row = { path: string; label: string; kind: "recent" | "dir" | "up"; git?: boolean };

/**
 * The working-directory input used everywhere a path is typed (new session, new workspace). Suggests as you type:
 * the directories already used (sessions' and workspaces' cwds, newest first) — typing a bare name like "gerdr" searches
 * them — and the subfolders of what's typed so far. ↑↓ pick, Tab completes into the input, Enter takes it.
 * A path that doesn't exist is said so under the input: it gets created on submit (ensureDir).
 */
export function DirPicker({
  value,
  onChange,
  onEnter,
  autoFocus,
}: {
  value: string;
  onChange: (v: string) => void;
  /** Gets the chosen value: picking a row and submitting happen in the same tick, before the parent's state has it. */
  onEnter?: (v: string) => void;
  autoFocus?: boolean;
}) {
  const home = useStore((s) => s.home);
  const sessions = useStore((s) => s.sessions);
  const workspaces = useStore((s) => s.layout.workspaces);
  const [focused, setFocused] = useState(false);
  const [list, setList] = useState<{ path: string; parent: string; dirs: string[]; git: boolean } | null>(null);
  const [hi, setHi] = useState(-1);
  // What the typed path is: ok / missing (created on submit) / bad (a file, no permission: the server's message)
  const [state, setState] = useState<{ path: string; kind: "ok" | "missing" | "bad"; msg?: string } | null>(null);
  const listRef = useRef<HTMLDivElement>(null);

  // Every directory used before, newest first: sessions' cwds (not Work agents', those live in gerdr's own dir), then
  // workspaces' that have no session yet.
  const known = useMemo(() => {
    const seen = new Set<string>();
    const out: string[] = [];
    const add = (c?: string) => c && !seen.has(c) && (seen.add(c), out.push(c));
    Object.values(sessions)
      .filter((s) => !workProjectOf(s.id))
      .sort((a, b) => b.updatedAt - a.updatedAt)
      .forEach((s) => add(s.cwd));
    workspaces.forEach((w) => add(w.cwd));
    return out;
  }, [sessions, workspaces]);

  const browse = (p: string) => {
    api.dirs(p).then(setList, () => {});
  };
  // List the folder the typed path is in (or the path itself when it ends with /)
  const abs = expand(value, home);
  const dirOf = abs.endsWith("/") ? trimSlash(abs) : abs.slice(0, abs.lastIndexOf("/")) || "/";
  useEffect(() => {
    if (!focused || isQuery(value)) return;
    if (!list || list.path !== dirOf) browse(value ? dirOf : home);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [focused, dirOf]);

  // Does the typed path exist? Checked a moment after typing stops.
  useEffect(() => {
    if (!value.trim() || isQuery(value)) return setState(null);
    const p = trimSlash(abs);
    const t = setTimeout(() => {
      api.dirs(p).then(
        () => setState({ path: p, kind: "ok" }),
        (e) => setState(e instanceof ApiError && e.status === 404 ? { path: p, kind: "missing" } : { path: p, kind: "bad", msg: e.message }),
      );
    }, 200);
    return () => clearTimeout(t);
  }, [abs]);
  const status = state && state.path === trimSlash(abs) ? state : null;

  const rows: Row[] = useMemo(() => {
    if (isQuery(value)) {
      const q = value.toLowerCase();
      const base = (p: string) => p.slice(p.lastIndexOf("/") + 1).toLowerCase();
      // Folder-name matches first, then anywhere in the path
      return [...known.filter((p) => base(p).includes(q)), ...known.filter((p) => !base(p).includes(q) && p.toLowerCase().includes(q))]
        .slice(0, 50)
        .map((p) => ({ path: p, label: tilde(p, home), kind: "recent" as const }));
    }
    const out: Row[] = [];
    const filter =
      list && abs.startsWith(list.path === "/" ? "/" : list.path + "/") ? abs.slice(list.path === "/" ? 1 : list.path.length + 1).toLowerCase() : "";
    if (!filter && (!value || value === "~/" || value === "~" || value === "/")) {
      known.slice(0, 8).forEach((p) => out.push({ path: p, label: tilde(p, home), kind: "recent" }));
    }
    if (list && list.path === dirOf) {
      if (!filter) out.push({ path: list.parent, label: tilde(list.path, home), kind: "up", git: list.git });
      list.dirs
        .filter((d) => !filter || d.toLowerCase().includes(filter))
        .sort((a, b) => (filter ? +!a.toLowerCase().startsWith(filter) - +!b.toLowerCase().startsWith(filter) : 0))
        .slice(0, 200)
        .forEach((d) => {
          const full = (list.path === "/" ? "" : list.path) + "/" + d;
          out.push({ path: full, label: d, kind: "dir" });
        });
    }
    return out;
  }, [value, known, list, abs, dirOf, home]);

  // A bare word picks its first match on Enter; while typing a path nothing is picked until ↑↓ (Enter takes what's typed)
  useEffect(() => setHi(isQuery(value) && rows.length ? 0 : -1), [value, rows.length]);
  useEffect(() => {
    if (hi >= 0) listRef.current?.querySelector(`[data-row="${hi}"]`)?.scrollIntoView({ block: "nearest" });
  }, [hi]);

  // Into the input, ready to go deeper; up = the parent folder
  const complete = (r: Row) => {
    onChange(tilde(r.path, home) + (r.path === "/" ? "" : "/"));
    if (r.kind !== "recent") browse(r.path);
  };
  // A click only fills it in (so it can still be checked or changed); Enter / double-click also submit
  const take = (r: Row, submit: boolean) => {
    const v = tilde(r.path, home);
    onChange(v);
    setFocused(false);
    if (submit) onEnter?.(v);
  };
  const open = focused && rows.length > 0;
  return (
    <div className="space-y-1">
      <Popover open={open}>
        <PopoverAnchor asChild>
          <div className="relative">
            <Folder className="absolute left-2.5 top-1/2 size-4 -translate-y-1/2 text-muted-foreground" />
            <Input
              autoFocus={autoFocus}
              className={cn("pl-8 font-mono text-[13px]", status?.kind === "missing" && "border-warn", status?.kind === "bad" && "border-bad")}
              value={value}
              placeholder="~/project，或输入名字搜用过的目录"
              onFocus={() => setFocused(true)}
              onBlur={() => setFocused(false)}
              onChange={(e) => {
                onChange(e.target.value);
                setFocused(true);
              }}
              onKeyDown={(e) => {
                if (e.nativeEvent.isComposing) return;
                if (e.key === "ArrowDown" || e.key === "ArrowUp") {
                  if (!rows.length) return;
                  e.preventDefault();
                  setFocused(true);
                  const d = e.key === "ArrowDown" ? 1 : -1;
                  setHi((h) => (h < 0 ? (d > 0 ? 0 : rows.length - 1) : (h + d + rows.length) % rows.length));
                } else if (e.key === "Tab" && open && !e.shiftKey) {
                  const r = rows[hi >= 0 ? hi : rows.findIndex((x) => x.kind !== "up")];
                  if (r) {
                    e.preventDefault();
                    complete(r);
                  }
                } else if (e.key === "Enter") {
                  e.preventDefault();
                  if (open && hi >= 0 && rows[hi]) return take(rows[hi], true);
                  if (isQuery(value)) return; // a name with no match isn't a path
                  setFocused(false);
                  onEnter?.(value);
                } else if (e.key === "Escape" && open) {
                  e.preventDefault();
                  e.stopPropagation(); // close the list, not the dialog around it
                  setFocused(false);
                }
              }}
            />
          </div>
        </PopoverAnchor>
        <PopoverContent
          align="start"
          className="w-[var(--radix-popover-trigger-width)] min-w-80 p-1"
          onOpenAutoFocus={(e) => e.preventDefault()}
          // Clicking a row mustn't blur the input (that closes the list before the click lands)
          onMouseDown={(e) => e.preventDefault()}
        >
          <div ref={listRef} className="max-h-72 overflow-auto">
            {rows.map((r, i) => {
              const Icon = r.kind === "recent" ? History : r.kind === "up" ? ChevronUp : Folder;
              return (
                <button
                  key={r.kind + r.path}
                  data-row={i}
                  className={cn(
                    "flex w-full items-center gap-2 rounded-sm px-2 py-1 text-left font-mono text-[12px] hover:bg-accent",
                    i === hi && "bg-accent",
                    r.kind === "up" && "text-muted-foreground",
                  )}
                  onMouseEnter={() => setHi(i)}
                  onClick={() => (r.kind === "recent" ? take(r, false) : complete(r))}
                  onDoubleClick={() => r.kind === "dir" && take(r, true)}
                >
                  <Icon className="size-3.5 shrink-0 text-muted-foreground" />
                  <span className="truncate">{r.label}</span>
                  {r.git && <FolderGit2 className="size-3.5 shrink-0 text-brand" />}
                </button>
              );
            })}
          </div>
        </PopoverContent>
      </Popover>
      {status?.kind === "missing" && <div className="truncate text-[12px] text-warn">这个目录还不存在，会自动创建</div>}
      {status?.kind === "bad" && <div className="truncate text-[12px] text-bad">{status.msg}</div>}
    </div>
  );
}

const NO_DEFS: AgentDef[] = [];
const BASE_NAME: Record<string, string> = { claude: "Claude Code", codex: "Codex", native: "Native" };

/** 自建 agent 卡片下面那行小字：有提示词就显示提示词开头（一眼看出它干什么的），没有就说用哪个引擎。 */
function roleHint(d?: AgentDef) {
  if (!d?.base) return "";
  return d.prompt?.trim().split("\n")[0] || `引擎 ${BASE_NAME[d.base]}`;
}

export function NewSession({ pane, focused }: { pane: PaneLeaf; focused: boolean }) {
  const agents = useStore((s) => s.agents);
  const home = useStore((s) => s.home);
  const defAgent = useStore((s) => s.config.defaultAgent);
  const defs = useStore((s) => s.config.agents) ?? NO_DEFS;
  const [agent, setAgent] = useState(
    pane.draft?.agent ?? (agents.find((a) => a.id === defAgent && a.available)?.id || agents.find((a) => a.available)?.id || "claude"),
  );
  const [cwd, setCwd] = useState(tilde(pane.draft?.cwd ?? home, home));
  const [busy, setBusy] = useState(false);
  const startRef = useRef<HTMLButtonElement>(null);

  useEffect(() => {
    if (!focused) return;
    const f = () => !get().dialog && startRef.current?.focus();
    f();
    window.addEventListener("gerdr:refocus", f);
    return () => window.removeEventListener("gerdr:refocus", f);
  }, [focused]);

  const abs = cwd.startsWith("~") ? home + cwd.slice(1) : cwd;
  // Earlier conversations here = gerdr's own sessions for this agent and directory. Ones a pane already shows are listed too,
  // marked, and clicking one jumps to its pane instead of opening it a second time. Not the agent's native history: that's a
  // one-off import (ImportAgentDialog), and as conversations move into gerdr it would only empty out. Work agent conversations
  // belong to their Work view, and never-used sessions (an empty pane) have nothing to resume.
  const sessions = useStore((s) => s.sessions);
  const layout = useStore((s) => s.layout);
  // Untitled = nothing said in it yet (a fresh pane, or a tab right after /clear): nothing to pick up, see ResumeDialog
  const old = Object.values(sessions)
    .filter((s) => s.agent === agent && s.cwd === abs && !!s.title && !workProjectOf(s.id))
    // A running session bumps updatedAt with every streamed chunk, so two running ones kept swapping places: order those by
    // when their turn started instead, which holds still for the whole turn.
    .sort((a, b) => (b.turn?.startedAt ?? b.updatedAt) - (a.turn?.startedAt ?? a.updatedAt))
    .map((s) => ({ s, open: !!locateSession(layout, s.id) }));
  const agentName = agents.find((a) => a.id === agent)?.name ?? agent;

  // as：双击某个 agent 卡片直接开，这时 setAgent 还没生效，得显式传进来
  // dir: a path the picker just chose, before this render's cwd has it
  const start = async (imp?: string, title?: string, as = agent, dir?: string) => {
    if (busy) return;
    setBusy(true);
    try {
      // 接以前的会话：标题沿用列表里那个（agent 那边改过名就是改过的名字，没改过是第一句话）
      const cwd = dir !== undefined ? expand(dir, home) : abs;
      try {
        await ensureDir(cwd);
      } catch (e: any) {
        toast.error(e.message ?? String(e)); // createInPane reports its own failures; this one would go unseen
        throw e;
      }
      await createInPane(pane.id, { agent: as, cwd, import: imp, title });
    } catch {
      setBusy(false);
    }
  };

  // Ctrl+D 关掉这个空 pane（像在空 shell 里按 ^D 退出）：两次 Ctrl+C 退出 agent 之后落到这一屏，再按它才关 pane。
  // 挂在 window 上、只看这个 pane 是不是焦点：原来挂在这一屏的 div 上，关了别的 pane、点过别处之后
  // 光标不在这一屏里面（落到了 body），按了没反应（用户报的）。
  // 光标在输入框里时不管：mac 上那是「删掉后面一个字」。
  useEffect(() => {
    if (!focused) return;
    const onKey = (e: KeyboardEvent) => {
      if (e.defaultPrevented || e.key !== "d" || !e.ctrlKey || e.metaKey || e.altKey || e.shiftKey) return;
      const a = document.activeElement as HTMLElement | null;
      if (a && (a.isContentEditable || /^(INPUT|TEXTAREA|SELECT)$/.test(a.tagName))) return;
      if (get().dialog || document.querySelector("[role=dialog]")) return;
      e.preventDefault();
      focusPane(pane.id);
      closeFocusedPane();
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [focused, pane.id]);

  return (
    <div className="flex h-full items-start justify-center overflow-auto p-6">
      <div className="mt-[8vh] w-full max-w-lg space-y-5">
        <div className="flex items-center text-[13px] font-medium text-muted-foreground">
          新会话（Ctrl+D 关掉）
          {/* 角色、各 agent 新会话的默认模式 / 模型都在设置 → Agent 里，从这儿直接过去 */}
          <button
            className="ml-auto flex items-center gap-1 text-[11px] font-normal hover:text-foreground"
            onClick={() => set({ dialog: { kind: "settings", tab: "agents" } })}
          >
            <Settings2 className="size-3.5" />
            Agent 设置
          </button>
        </div>
        <div className="grid grid-cols-2 gap-2">
          {agents.map((a) => (
            <button
              key={a.id}
              disabled={!a.available}
              title={a.error}
              onClick={() => setAgent(a.id)}
              onDoubleClick={() => start(undefined, undefined, a.id)}
              className={cn(
                "flex items-center gap-2.5 rounded-lg border px-3 py-2.5 text-left transition-colors",
                agent === a.id ? "border-brand bg-brand/8" : "hover:bg-accent",
                !a.available && "cursor-not-allowed opacity-50",
              )}
            >
              <AgentIcon agent={a.id} className="size-5" />
              <div className="min-w-0">
                <div className="text-[13px] font-medium">{a.name}</div>
                {(!a.available || roleHint(defs.find((d) => d.id === a.id))) && (
                  <div className="truncate text-[11px] text-muted-foreground">{a.available ? roleHint(defs.find((d) => d.id === a.id)) : "没装"}</div>
                )}
              </div>
            </button>
          ))}
        </div>
        <DirPicker value={cwd} onChange={setCwd} onEnter={(v) => start(undefined, undefined, agent, v)} />
        <div className="flex gap-2">
          <Button ref={startRef} className="flex-1" disabled={busy} onClick={() => start()} onKeyDown={(e) => e.key === "Enter" && start()}>
            {busy ? <Loader2 className="size-4 animate-spin" /> : <Play className="size-4" />}
            开始
          </Button>
          {/* 临时要敲几条命令：这个 pane 直接变成这个目录下的终端 */}
          <Button variant="outline" disabled={busy} title="在这个目录开终端（prefix+t 在旁边开）" onClick={() => openTerminal({ pane: pane.id, cwd: abs })}>
            <SquareTerminal className="size-4" />
            终端
          </Button>
        </div>
        <div>
          <div className="mb-1.5 flex items-center gap-1.5 text-[12px] text-muted-foreground">
            <History className="size-3.5" />
            接着以前的聊
            <button
              className="ml-auto text-[11px] font-normal hover:text-foreground"
              onClick={() => set({ dialog: { kind: "importAgent", agent, cwd: abs, paneId: pane.id } })}
            >
              导入 {agentName} 对话…
            </button>
          </div>
          {old.length === 0 && <div className="text-[12px] text-muted-foreground">这个目录下还没有以前的会话</div>}
          <div className="space-y-0.5">
            {old.slice(0, 12).map(({ s: o, open }) => (
              <button
                key={o.id}
                disabled={busy}
                className="flex w-full items-center gap-2 rounded-md px-2 py-1.5 text-left text-[13px] hover:bg-accent"
                title={open ? "已经开着：点了跳过去" : undefined}
                onClick={() => (open ? focusSession(o.id) : resumeInPane(pane.id, o.id))}
              >
                <span className="min-w-0 flex-1 truncate">{o.title || o.id}</span>
                {open && (
                  <span className="flex shrink-0 items-center gap-1 text-[11px] text-muted-foreground">
                    <StatusDot status={o.status} />
                    已打开
                  </span>
                )}
                <span className="shrink-0 text-[11px] text-muted-foreground">{ago(o.updatedAt)}</span>
              </button>
            ))}
          </div>
        </div>
      </div>
    </div>
  );
}

export function ago(t: string | number) {
  const ms = Date.now() - (typeof t === "number" ? t : Date.parse(t));
  const m = Math.floor(ms / 60000);
  if (m < 1) return "刚刚";
  if (m < 60) return `${m} 分钟前`;
  const h = Math.floor(m / 60);
  if (h < 24) return `${h} 小时前`;
  const d = Math.floor(h / 24);
  return `${d} 天前`;
}
