import { ChevronUp, Folder, FolderGit2, History, Loader2, Play, SquareTerminal } from "lucide-react";
import { useEffect, useMemo, useRef, useState } from "react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Popover, PopoverAnchor, PopoverContent } from "@/components/ui/popover";
import { basename, createInPane, openTerminal } from "@/lib/actions";
import { api } from "@/lib/api";
import { get, set, useStore } from "@/lib/store";
import type { PaneLeaf } from "@/lib/layout";
import { cn } from "@/lib/utils";
import { AgentIcon } from "./AgentIcon";

function tilde(p: string, home: string) {
  return home && p.startsWith(home) ? "~" + p.slice(home.length) : p;
}

export function DirPicker({ value, onChange, onEnter, autoFocus }: { value: string; onChange: (v: string) => void; onEnter?: () => void; autoFocus?: boolean }) {
  const home = useStore((s) => s.home);
  const sessions = useStore((s) => s.sessions);
  const [open, setOpen] = useState(false);
  const [list, setList] = useState<{ path: string; parent: string; dirs: string[]; git: boolean } | null>(null);
  const [err, setErr] = useState("");
  const recent = useMemo(() => {
    const seen = new Set<string>();
    return Object.values(sessions)
      .sort((a, b) => b.updatedAt - a.updatedAt)
      .map((s) => s.cwd)
      .filter((c) => (seen.has(c) ? false : (seen.add(c), true)))
      .slice(0, 8);
  }, [sessions]);
  const browse = (p: string) => {
    api
      .dirs(p)
      .then((r) => {
        setList(r);
        setErr("");
      })
      .catch((e) => setErr(e.message));
  };
  useEffect(() => {
    if (!open) return;
    const p = value.endsWith("/") ? value : value.slice(0, value.lastIndexOf("/") + 1) || value;
    browse(value && !value.endsWith("/") ? value : p || home);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open]);
  const shown = value.startsWith("~") && home ? home + value.slice(1) : value;
  const filter = list && shown.startsWith(list.path + "/") ? shown.slice(list.path.length + 1).toLowerCase() : "";
  return (
    <Popover open={open} onOpenChange={setOpen}>
      <PopoverAnchor asChild>
        <div className="relative">
          <Folder className="absolute left-2.5 top-1/2 size-4 -translate-y-1/2 text-muted-foreground" />
          <Input
            autoFocus={autoFocus}
            className="pl-8 font-mono text-[13px]"
            value={value}
            placeholder="~/project"
            onFocus={() => setOpen(true)}
            onChange={(e) => {
              onChange(e.target.value);
              const v = e.target.value.startsWith("~") && home ? home + e.target.value.slice(1) : e.target.value;
              const dir = v.endsWith("/") ? v.slice(0, -1) || "/" : v.slice(0, v.lastIndexOf("/")) || "/";
              if (!list || list.path !== dir) browse(dir);
            }}
            onKeyDown={(e) => {
              if (e.key === "Enter" && !e.nativeEvent.isComposing) {
                setOpen(false);
                onEnter?.();
              }
              if (e.key === "Escape") setOpen(false);
            }}
          />
        </div>
      </PopoverAnchor>
      <PopoverContent align="start" className="w-[var(--radix-popover-trigger-width)] min-w-80 p-1" onOpenAutoFocus={(e) => e.preventDefault()}>
        {recent.length > 0 && !filter && (
          <div className="mb-1 border-b pb-1">
            <div className="px-2 py-1 text-[11px] text-muted-foreground">最近</div>
            {recent.map((r) => (
              <button key={r} className="flex w-full items-center gap-2 rounded px-2 py-1 text-left font-mono text-[12px] hover:bg-accent" onClick={() => (onChange(tilde(r, home)), setOpen(false))}>
                <History className="size-3.5 text-muted-foreground" />
                <span className="truncate">{tilde(r, home)}</span>
              </button>
            ))}
          </div>
        )}
        {err && <div className="px-2 py-1 text-[12px] text-bad">{err}</div>}
        {list && (
          <div className="max-h-72 overflow-auto">
            <button className="flex w-full items-center gap-2 rounded px-2 py-1 text-left font-mono text-[12px] text-muted-foreground hover:bg-accent" onClick={() => (onChange(tilde(list.parent, home) + "/"), browse(list.parent))}>
              <ChevronUp className="size-3.5" />
              {tilde(list.path, home)}
              {list.git && <FolderGit2 className="size-3.5 text-brand" />}
            </button>
            {list.dirs
              .filter((d) => !filter || d.toLowerCase().includes(filter))
              .slice(0, 200)
              .map((d) => {
                const full = (list.path === "/" ? "" : list.path) + "/" + d;
                return (
                  <button
                    key={d}
                    className="flex w-full items-center gap-2 rounded px-2 py-1 text-left font-mono text-[12px] hover:bg-accent"
                    onClick={() => {
                      onChange(tilde(full, home));
                      browse(full);
                    }}
                    onDoubleClick={() => {
                      onChange(tilde(full, home));
                      setOpen(false);
                      onEnter?.();
                    }}
                  >
                    <Folder className="size-3.5 text-muted-foreground" />
                    {d}
                  </button>
                );
              })}
          </div>
        )}
      </PopoverContent>
    </Popover>
  );
}

export function NewSession({ pane, focused }: { pane: PaneLeaf; focused: boolean }) {
  const agents = useStore((s) => s.agents);
  const home = useStore((s) => s.home);
  const defAgent = useStore((s) => s.config.defaultAgent);
  const [agent, setAgent] = useState(pane.draft?.agent ?? (agents.find((a) => a.id === defAgent && a.available)?.id || agents.find((a) => a.available)?.id || "claude"));
  const [cwd, setCwd] = useState(tilde(pane.draft?.cwd ?? home, home));
  const [busy, setBusy] = useState(false);
  const [old, setOld] = useState<{ sessionId: string; cwd: string; title?: string; updatedAt?: string }[] | null>(null);
  const [oldErr, setOldErr] = useState("");
  const startRef = useRef<HTMLButtonElement>(null);

  useEffect(() => {
    if (!focused) return;
    const f = () => !get().dialog && startRef.current?.focus();
    f();
    window.addEventListener("gerdr:refocus", f);
    return () => window.removeEventListener("gerdr:refocus", f);
  }, [focused]);

  const abs = cwd.startsWith("~") ? home + cwd.slice(1) : cwd;
  useEffect(() => {
    setOld(null);
    setOldErr("");
    const t = setTimeout(() => {
      api
        .importable(agent, abs)
        .then((r) => setOld(r))
        .catch((e) => setOldErr(e.message));
    }, 400);
    return () => clearTimeout(t);
  }, [agent, abs]);

  const start = async (imp?: string, title?: string) => {
    if (busy) return;
    setBusy(true);
    try {
      // 接以前的会话：标题沿用列表里那个（agent 那边改过名就是改过的名字，没改过是第一句话）
      await createInPane(pane.id, { agent, cwd: abs, import: imp, title });
    } catch {
      setBusy(false);
    }
  };

  return (
    <div className="flex h-full items-start justify-center overflow-auto p-6">
      <div className="mt-[8vh] w-full max-w-lg space-y-5">
        <div className="text-[13px] font-medium text-muted-foreground">新会话</div>
        <div className="grid grid-cols-2 gap-2">
          {agents.map((a) => (
            <button
              key={a.id}
              disabled={!a.available}
              title={a.error}
              onClick={() => setAgent(a.id)}
              className={cn(
                "flex items-center gap-2.5 rounded-lg border px-3 py-2.5 text-left transition-colors",
                agent === a.id ? "border-brand bg-brand/8" : "hover:bg-accent",
                !a.available && "cursor-not-allowed opacity-50",
              )}
            >
              <AgentIcon agent={a.id} className="size-5" />
              <div className="min-w-0">
                <div className="text-[13px] font-medium">{a.name}</div>
                {!a.available && <div className="truncate text-[11px] text-muted-foreground">没装</div>}
              </div>
            </button>
          ))}
        </div>
        <DirPicker value={cwd} onChange={setCwd} onEnter={() => start()} />
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
            接着以前的聊（{basename(abs)}）
            <button className="ml-auto text-[11px] font-normal hover:text-foreground" onClick={() => set({ dialog: { kind: "importHerdr" } })}>
              从 herdr 整个导入…
            </button>
          </div>
          {oldErr && <div className="text-[12px] text-bad">{oldErr}</div>}
          {!old && !oldErr && <div className="text-[12px] text-muted-foreground">…</div>}
          {old && old.length === 0 && <div className="text-[12px] text-muted-foreground">这个目录下没有以前的会话</div>}
          <div className="space-y-0.5">
            {old?.slice(0, 12).map((o) => (
              <button key={o.sessionId} disabled={busy} className="flex w-full items-baseline gap-2 rounded-md px-2 py-1.5 text-left text-[13px] hover:bg-accent" onClick={() => start(o.sessionId, o.title)}>
                <span className="min-w-0 flex-1 truncate">{o.title || o.sessionId}</span>
                {o.updatedAt && <span className="shrink-0 text-[11px] text-muted-foreground">{ago(o.updatedAt)}</span>}
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
