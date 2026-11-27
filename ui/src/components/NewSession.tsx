import { ArrowRight, ChevronUp, Folder, FolderGit2, Globe, History, Laptop, Loader2, Play, Search, Server, Settings2, SquareTerminal } from "lucide-react";
import { useEffect, useMemo, useRef, useState } from "react";
import { toast } from "sonner";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Popover, PopoverAnchor, PopoverContent } from "@/components/ui/popover";
import { browserHere, continueInPane, createInPane, focusPane, focusSession, movePaneToWorkspace, openTerminal, resumeInPane, runAction } from "@/lib/actions";
import { api, ApiError } from "@/lib/api";
import { get, hostDirs, hostNoun, hostOrder, hostStart, set, useStore, type AgentDef } from "@/lib/store";
import { keyText } from "./KeyCaps";
import { allLeaves, locateSession, paneSpace, spaceWorkspaces, type PaneLeaf } from "@/lib/layout";
import { cn } from "@/lib/utils";
import { dialogOver } from "@/lib/phone";
import { workProjectOf } from "@/lib/work";
import { AgentIcon, StatusDot } from "./AgentIcon";
import { t } from "@/lib/i18n";

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
export async function ensureDir(path: string, host?: string) {
  if (!path.startsWith("/")) return;
  await api.mkdir(path, host).catch((e) => {
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
  host,
  hostHome,
}: {
  value: string;
  onChange: (v: string) => void;
  /** Gets the chosen value: picking a row and submitting happen in the same tick, before the parent's state has it. */
  onEnter?: (v: string) => void;
  autoFocus?: boolean;
  /** Browse another computer's folders (docs/dev/MULTIHOST.md); its home directory is hostHome. */
  host?: string;
  hostHome?: string;
}) {
  const localHome = useStore((s) => s.home);
  const home = host ? (hostHome ?? "") : localHome;
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
      .filter((s) => !workProjectOf(s.id) && (s.host?.id ?? "") === (host ?? ""))
      .sort((a, b) => b.updatedAt - a.updatedAt)
      .forEach((s) => add(s.cwd));
    if (!host) workspaces.forEach((w) => add(w.cwd));
    return out;
  }, [sessions, workspaces, host]);

  const browse = (p: string) => {
    api.dirs(p, host).then(setList, () => {});
  };
  // Another computer: what was listed was the other one's
  useEffect(() => setList(null), [host]);
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
      api.dirs(p, host).then(
        () => setState({ path: p, kind: "ok" }),
        (e) => setState(e instanceof ApiError && e.status === 404 ? { path: p, kind: "missing" } : { path: p, kind: "bad", msg: e.message }),
      );
    }, 200);
    return () => clearTimeout(t);
  }, [abs, host]);
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
  // A click or Enter only fills it in (so it can still be checked or changed); double-click also submits
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
              placeholder={t("newSession.dirPlaceholder")}
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
                  // With the list open, Enter only picks: the highlighted row fills the input, no row just closes the list.
                  // Submitting is the next Enter, once you can see what's in the box (it used to create straight away).
                  if (open) return hi >= 0 && rows[hi] ? take(rows[hi], false) : setFocused(false);
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
      {/* The line is always there, empty or not: the check re-runs as you type, and a line coming and going made the dialog jump */}
      <div className={cn("h-[18px] truncate text-[12px] leading-[18px]", status?.kind === "bad" ? "text-bad" : "text-warn")}>
        {status?.kind === "missing" ? t("newSession.dirMissing") : status?.kind === "bad" ? status.msg : null}
      </div>
    </div>
  );
}

const NO_DEFS: AgentDef[] = [];
const BASE_NAME: Record<string, string> = { claude: "Claude Code", codex: "Codex", native: "Native" };

/** 自建 agent 卡片下面那行小字：有提示词就显示提示词开头（一眼看出它干什么的），没有就说用哪个引擎。 */
function roleHint(d?: AgentDef) {
  if (!d?.base) return "";
  // A built-in one edited in Settings (same id as its engine): naming the engine says nothing
  return d.prompt?.trim().split("\n")[0] || (d.id === d.base ? "" : t("newSession.engine", { name: BASE_NAME[d.base] }));
}

export function NewSession({ pane, focused }: { pane: PaneLeaf; focused: boolean }) {
  const agents = useStore((s) => s.agents);
  const hosts = useStore((s) => s.hosts);
  // Which computer it starts on: "" = this one (docs/dev/MULTIHOST.md)
  const [host, setHost] = useState(pane.draft?.host ?? "");
  const [hostHomes, setHostHomes] = useState<Record<string, string>>({});
  const localHome = useStore((s) => s.home);
  const home = host ? (hostHomes[host] ?? "") : localHome;
  useEffect(() => {
    if (!host || hostHomes[host] !== undefined) return;
    api.dirs("", host).then(
      (d) => {
        setHostHomes((m) => ({ ...m, [host]: d.home }));
        setCwd((c) => (c === "" || c === "~" || c === tilde(localHome, localHome) ? "~" : c));
      },
      (e) => toast.error(t("newSession.hostUnreachable", { error: e.message ?? String(e) })),
    );
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [host]);
  const defAgent = useStore((s) => s.config.defaultAgent);
  const defs = useStore((s) => s.config.agents) ?? NO_DEFS;
  const [agent, setAgent] = useState(
    pane.draft?.agent ?? (agents.find((a) => a.id === defAgent && a.available)?.id || agents.find((a) => a.available)?.id || "claude"),
  );
  const [cwd, setCwd] = useState(tilde(pane.draft?.cwd ?? home, home));
  const [busy, setBusy] = useState(false);
  const layout = useStore((s) => s.layout);
  const startRef = useRef<HTMLButtonElement>(null);

  useEffect(() => {
    if (!focused) return;
    const f = () => !get().dialog && startRef.current?.focus();
    f();
    window.addEventListener("gerdr:refocus", f);
    return () => window.removeEventListener("gerdr:refocus", f);
  }, [focused]);

  const abs = cwd.startsWith("~") ? home + cwd.slice(1) : cwd;
  // The open workspaces of this pane's project, as quick picks over the directory input: the input starts with the
  // current workspace's directory, and these switch it in one tap (typing a path on a phone is slow). Only this
  // computer's: another one's folders aren't these.
  // A panel hosting Code gives its own instead (core useCodeDirs): where work goes there isn't the workspaces, which
  // such a panel may not even show (the repositories in its settings). Code mode keeps the workspaces.
  const panelDirs = useStore((s) => hostDirs(s));
  const wsDirs = useMemo(() => {
    // A panel's path may be written "~/…" (the Superagent panel's settings take what the person typed): expanded, or
    // the chip never matched the folder it picked and showed as not chosen
    if (panelDirs)
      return panelDirs.map((d) => {
        const cwd = trimSlash(expand(d.path, home));
        return { id: d.path, cwd, name: d.name || cwd.split("/").pop() || d.path, color: d.color };
      });
    const seen = new Set<string>();
    return spaceWorkspaces(layout, paneSpace(layout, pane.id))
      .filter((w) => w.cwd && !seen.has(w.cwd) && (seen.add(w.cwd), true))
      .map((w) => ({ id: w.id, cwd: w.cwd!, name: w.name, color: undefined as string | undefined }));
  }, [layout, pane.id, panelDirs, home]);
  // Whatever this pane becomes goes into the workspace whose folder is picked: the chips above name workspaces, and
  // picking one while the empty pane sat in another left the new session stuck in the wrong group (user report). The
  // pane's own workspace wins when it has that folder too; another computer's folders aren't these workspaces.
  const intoWorkspace = (dir: string) => {
    if (host) return;
    const d = trimSlash(dir);
    const l = get().layout;
    const own = allLeaves(l).find((x) => x.pane.id === pane.id)?.ws;
    if (!own || own.cwd === d) return;
    const to = spaceWorkspaces(l, paneSpace(l, pane.id)).find((w) => w.cwd === d);
    if (to) movePaneToWorkspace(pane.id, to.id);
  };
  // The picked one may sit past the row's right edge: bring it into view (scrollLeft, not scrollIntoView, which would
  // also scroll the page)
  const wsRow = useRef<HTMLDivElement>(null);
  useEffect(() => {
    const row = wsRow.current;
    const on = row?.querySelector<HTMLElement>("[data-on]");
    if (!row || !on) return;
    const l = on.offsetLeft - row.offsetLeft;
    if (l < row.scrollLeft || l + on.offsetWidth > row.scrollLeft + row.clientWidth) row.scrollLeft = l - 4;
  }, [abs, wsDirs.length]);
  // Earlier conversations here = gerdr's own sessions in this directory, whatever agent had them: a click carries one on
  // with its own agent, and the row's button hands it to the agent picked above (hub/handoff.go), so running out of one
  // agent's quota doesn't strand a conversation. Ones a pane already shows are listed too, marked, and clicking one with the
  // same agent jumps to its pane instead of opening it a second time. Not the agent's native history: that's a one-off
  // import (ImportAgentDialog), and as conversations move into gerdr it would only empty out. Work agent conversations
  // belong to their Work view, and never-used sessions (an empty pane) have nothing to resume.
  const sessions = useStore((s) => s.sessions);
  // Untitled = nothing said in it yet (a fresh pane, or a tab right after /clear): nothing to pick up, see ResumeDialog
  const old = Object.values(sessions)
    .filter((s) => s.cwd === abs && !!s.title && !workProjectOf(s.id) && (s.host?.id ?? "") === host)
    // A running session bumps updatedAt with every streamed chunk, so two running ones kept swapping places: order those by
    // when their turn started instead, which holds still for the whole turn.
    .sort((a, b) => (b.turn?.startedAt ?? b.updatedAt) - (a.turn?.startedAt ?? a.updatedAt))
    .map((s) => ({ s, open: !!locateSession(layout, s.id) }));
  // The list shows the latest dozen; the search goes through every one of them (titles; every word has to match)
  const [q, setQ] = useState("");
  const words = q.trim().toLowerCase().split(/\s+/).filter(Boolean);
  const shown = words.length ? old.filter(({ s }) => words.every((w) => `${s.title} ${s.id}`.toLowerCase().includes(w))) : old.slice(0, 12);
  const nameOf = (id: string) => agents.find((a) => a.id === id)?.name ?? id;
  const agentName = nameOf(agent);

  // An earlier conversation carries on with its own agent, whatever card is picked above: clicking a Claude conversation
  // while Gemini was picked used to fork it into Gemini, and that was rarely what the click meant.
  const pick = (id: string, open: boolean) => {
    if (open) return focusSession(id);
    intoWorkspace(abs);
    resumeInPane(pane.id, id);
  };
  // Handing it to the picked agent (hub/handoff.go; the original stays) is the row's own button, so one agent running
  // out of quota still doesn't strand a conversation.
  const handoff = async (id: string) => {
    if (busy) return;
    setBusy(true);
    try {
      intoWorkspace(abs);
      await continueInPane(pane.id, id, agent);
    } catch {
      setBusy(false);
    }
  };

  // as：双击某个 agent 卡片直接开，这时 setAgent 还没生效，得显式传进来
  // dir: a path the picker just chose, before this render's cwd has it
  const start = async (imp?: string, title?: string, as = agent, dir?: string) => {
    if (busy || (host && !home)) return;
    setBusy(true);
    try {
      // 接以前的会话：标题沿用列表里那个（agent 那边改过名就是改过的名字，没改过是第一句话）
      const cwd = dir !== undefined ? expand(dir, home) : abs;
      try {
        await ensureDir(cwd, host || undefined);
      } catch (e: any) {
        toast.error(e.message ?? String(e)); // createInPane reports its own failures; this one would go unseen
        throw e;
      }
      // A panel hosting Code starts it as its own (createInPane, core useCodeStart): no workspace of Code's for it
      if (imp || host || !hostStart()) intoWorkspace(cwd);
      await createInPane(pane.id, { agent: as, cwd, import: imp, title, host: host || undefined });
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
      if (get().dialog || dialogOver(startRef.current)) return;
      e.preventDefault();
      focusPane(pane.id);
      // Same as ⌘W, so a panel hosting Code closes it its own way (actions/hosted.ts: the page goes and the task seen
      // before comes back). Calling closeFocusedPane here only removed the tab, and the panel put an empty page back
      runAction("closePane");
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [focused, pane.id]);

  // A panel hosting Code (Superagent) opens this page with ⌘T, not by quitting an agent with Ctrl+C, and closes it with
  // closePane (⌘W, actions/hosted.ts): name that key there. Code keeps the shell-like Ctrl+D (the person, 2026-11-26)
  const hosted = useStore((s) => !!hostOrder(s));
  useStore((s) => s.config); // keyText reads the keys from it: follow a rebinding
  const closeKey = hosted ? keyText("closePane") : "";
  const noun = useStore((s) => hostNoun(s)); // the panel's own word (core useCodeNoun): Superagent's 新任务
  const title = !hosted
    ? t("newSession.title")
    : noun
      ? closeKey
        ? t("newSession.namedKey", { name: noun, key: closeKey })
        : noun
      : closeKey
        ? t("newSession.titleKey", { key: closeKey })
        : t("newSession.titlePlain");

  return (
    <div className="flex h-full items-start justify-center overflow-auto p-6">
      <div className="mt-[8vh] w-full max-w-lg space-y-5">
        <div className="flex items-center text-[13px] font-medium text-muted-foreground">
          {title}
          {/* 角色、各 agent 新会话的默认模式 / 模型都在设置 → Agent 里，从这儿直接过去 */}
          <button
            className="ml-auto flex items-center gap-1 text-[11px] font-normal hover:text-foreground"
            onClick={() => set({ dialog: { kind: "settings", tab: "agents" } })}
          >
            <Settings2 className="size-3.5" />
            {t("newSession.agentSettings")}
          </button>
        </div>
        {hosts.length > 0 && (
          // Where it runs: this computer or one it drives. The agents below are this computer's list; the other one
          // needs the same agent installed.
          <div className="flex flex-wrap gap-1.5">
            {[{ id: "", name: t("newSession.thisComputer"), online: true, error: undefined as string | undefined }, ...hosts].map((h) => (
              <button
                key={h.id || "local"}
                title={h.error}
                onClick={() => setHost(h.id)}
                className={cn(
                  "flex items-center gap-1.5 rounded-md border px-2.5 py-1 text-[12px] transition-colors",
                  host === h.id ? "border-brand bg-brand/8" : "hover:bg-accent",
                )}
              >
                {h.id ? <Server className="size-3.5" /> : <Laptop className="size-3.5" />}
                {h.name}
                {h.id && <span className={cn("size-1.5 rounded-full", h.online ? "bg-ok" : "bg-muted-foreground/40")} />}
              </button>
            ))}
          </div>
        )}
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
                  <div className="truncate text-[11px] text-muted-foreground">
                    {a.available ? roleHint(defs.find((d) => d.id === a.id)) : t("newSession.notInstalled")}
                  </div>
                )}
              </div>
            </button>
          ))}
        </div>
        <div className="space-y-2">
          {!host && wsDirs.length > (panelDirs ? 0 : 1) && (
            // One row that scrolls sideways: wrapped, a dozen workspaces ate half a phone screen. Both scrollbar hiders, as
            // Android's WebView ignores scrollbar-width (OverviewPhone).
            <div
              ref={wsRow}
              // Wider than a phone there is room: the chips wrap, up to three rows (then that box scrolls), instead of
              // hiding most of them past the right edge of one row
              className="-mx-1 flex gap-1.5 px-1 [scrollbar-width:none] max-md:overflow-x-auto md:max-h-[6.25rem] md:flex-wrap md:overflow-y-auto md:py-px [&::-webkit-scrollbar]:hidden"
            >
              {wsDirs.map((w) => (
                <button
                  key={w.id}
                  data-on={w.cwd === trimSlash(abs) || undefined}
                  title={tilde(w.cwd, home)}
                  onClick={() => setCwd(tilde(w.cwd, home))}
                  // Same as double-clicking an agent card: pick it and start right away
                  onDoubleClick={() => start(undefined, undefined, agent, w.cwd)}
                  className={cn(
                    "flex max-w-48 shrink-0 items-center gap-1.5 rounded-md border px-2.5 py-1 text-[12px] transition-colors",
                    w.cwd === trimSlash(abs) ? "border-brand bg-brand/8" : "hover:bg-accent",
                  )}
                >
                  {/* A panel's colour for the repository (core useCodeDirs `color`): the person's own data, not a theme colour */}
                  <Folder className={cn("size-3.5 shrink-0", !w.color && "text-muted-foreground")} style={w.color ? { color: w.color } : undefined} />
                  <span className="truncate">{w.name}</span>
                </button>
              ))}
            </div>
          )}
          <DirPicker value={cwd} onChange={setCwd} onEnter={(v) => start(undefined, undefined, agent, v)} host={host || undefined} hostHome={home} />
        </div>
        <div className="flex gap-2">
          {/* Another computer's home isn't known until it answered: "~/x" would land in the wrong place */}
          <Button
            ref={startRef}
            className="flex-1"
            disabled={busy || (!!host && !home)}
            onClick={() => start()}
            onKeyDown={(e) => e.key === "Enter" && start()}
          >
            {busy ? <Loader2 className="size-4 animate-spin" /> : <Play className="size-4" />}
            {t("newSession.start")}
          </Button>
          {/* 临时要敲几条命令：这个 pane 直接变成这个目录下的终端 (on the computer picked above) */}
          <Button
            variant="outline"
            disabled={busy || (!!host && !home)}
            title={keyText("splitTerminal") ? t("newSession.terminalHereKey", { key: keyText("splitTerminal") }) : t("newSession.terminalHere")}
            onClick={() => {
              intoWorkspace(abs);
              void openTerminal({ pane: pane.id, cwd: abs, host: host || undefined });
            }}
          >
            <SquareTerminal className="size-4" />
            {t("newSession.terminal")}
          </Button>
          {/* or this pane becomes the project's browser (components/browser/) */}
          <Button
            variant="outline"
            disabled={busy}
            title={t("newSession.browserHere")}
            onClick={() => {
              intoWorkspace(abs);
              void browserHere(pane.id);
            }}
          >
            <Globe className="size-4" />
            {t("newSession.browser")}
          </Button>
        </div>
        <div>
          <div className="mb-1.5 flex items-center gap-1.5 text-[12px] text-muted-foreground">
            <History className="size-3.5" />
            {t("newSession.continue")}
            <button
              className="ml-auto text-[11px] font-normal hover:text-foreground"
              onClick={() => set({ dialog: { kind: "importAgent", agent, cwd: abs, paneId: pane.id, host: host || undefined } })}
            >
              {t("newSession.import", { agent: agentName })}
            </button>
          </div>
          {old.length === 0 && <div className="text-[12px] text-muted-foreground">{t("newSession.noOld")}</div>}
          {old.length > 12 && (
            <div className="relative mb-1.5">
              <Search className="pointer-events-none absolute top-1/2 left-2.5 size-3.5 -translate-y-1/2 text-muted-foreground" />
              <Input value={q} onChange={(e) => setQ(e.target.value)} placeholder={t("resumeDialog.search")} className="h-8 pl-8 text-[13px]" />
            </div>
          )}
          {words.length > 0 && shown.length === 0 && <div className="text-[12px] text-muted-foreground">{t("newSession.noMatch")}</div>}
          {/* -mx-2 cancels the rows' padding, so their icons line up with the header's and everything above */}
          <div className="-mx-2 space-y-0.5">
            {shown.map(({ s: o, open }) => (
              <div key={o.id} className="group flex items-center rounded-md hover:bg-accent">
                <button
                  disabled={busy}
                  className="flex min-w-0 flex-1 items-center gap-2 py-1.5 pl-2 pr-1 text-left text-[13px]"
                  title={open ? t("newSession.alreadyOpenTitle") : undefined}
                  onClick={() => pick(o.id, open)}
                >
                  <AgentIcon agent={o.agent} className="size-3.5 shrink-0" />
                  <span className="min-w-0 flex-1 truncate">{o.title || o.id}</span>
                  {open && (
                    <span className="flex shrink-0 items-center gap-1 text-[11px] text-muted-foreground">
                      <StatusDot status={o.status} />
                      {t("newSession.opened")}
                    </span>
                  )}
                  <span className="shrink-0 text-[11px] text-muted-foreground">{ago(o.updatedAt)}</span>
                </button>
                {/* Another agent's conversation: hand it to the picked one. Shown on hover, always on touch screens */}
                {o.agent !== agent && (
                  <button
                    disabled={busy}
                    title={t("newSession.handoffTitle", { from: nameOf(o.agent), agent: agentName })}
                    onClick={() => void handoff(o.id)}
                    className="mr-1 hidden shrink-0 items-center gap-0.5 rounded-sm px-1 py-1 text-muted-foreground group-hover:flex hover:bg-background hover:text-foreground [@media(hover:none)]:flex"
                  >
                    <ArrowRight className="size-3" />
                    <AgentIcon agent={agent} className="size-3.5" />
                  </button>
                )}
              </div>
            ))}
          </div>
        </div>
      </div>
    </div>
  );
}

export function ago(at: string | number) {
  const ms = Date.now() - (typeof at === "number" ? at : Date.parse(at));
  const m = Math.floor(ms / 60000);
  if (m < 1) return t("newSession.ago.now");
  if (m < 60) return t("newSession.ago.min", { n: m });
  const h = Math.floor(m / 60);
  if (h < 24) return t("newSession.ago.hour", { n: h });
  const d = Math.floor(h / 24);
  return t("newSession.ago.day", { n: d });
}
