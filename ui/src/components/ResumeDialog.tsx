import { Loader2 } from "lucide-react";
import { useEffect, useState } from "react";
import { toast } from "sonner";
import { AgentIcon } from "@/components/AgentIcon";
import { CommandDialog, CommandEmpty, CommandGroup, CommandInput, CommandItem, CommandList } from "@/components/ui/command";
import { resumeInPane } from "@/lib/actions";
import { api } from "@/lib/api";
import { set, useStore } from "@/lib/store";

// /resume (like Claude Code): switch this pane to an earlier conversation in the same directory.
// claude in stream-json mode doesn't take /resume (it's the interactive picker), so gerdr does it, from one list sorted by time:
//   - gerdr's other sessions for this agent + directory (closed ones too; picking one resumes it);
//   - contexts dropped by /clear (Record.cleared): the old agent session is still on disk and resumable, but gerdr's record moved
//     on to the new id. Listed under the title they had here, so they're found the way the user remembers them;
//   - the agent's own sessions gerdr hasn't seen (run in a terminal): picking one imports it.
// There used to be a separate "agent's own records (run in a terminal)" group at the bottom: pre-clear contexts sank into it
// under their first message and nobody found them. Where a conversation came from isn't what you search by; when you last had it is.
// The session switched away from isn't deleted; it's still in the sessions list.

type Native = { sessionId: string; cwd: string; title?: string; updatedAt?: string };
type Entry = { key: string; search: string; title: string; at: number; note?: string; pick: () => void };

export function ResumeDialog() {
  const d = useStore((s) => s.dialog);
  const open = d?.kind === "resume";
  const cur = useStore((s) => (open ? s.sessions[d.sessionId] : undefined));
  const sessions = useStore((s) => s.sessions);
  const [native, setNative] = useState<Native[] | null>(null);
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    if (!open || !cur) return;
    setNative(null);
    api.importable(cur.agent, cur.cwd).then(setNative, () => setNative([]));
  }, [open, cur?.id]);

  if (!open || !cur) return null;
  const paneId = d.paneId;
  const close = () => set({ dialog: null });

  const pickNative = async (n: Native) => {
    setBusy(true);
    try {
      const s = await api.createSession({ agent: cur.agent, cwd: cur.cwd, import: n.sessionId, title: n.title });
      close();
      resumeInPane(paneId, s.id);
    } catch (e: any) {
      toast.error(e.message);
    } finally {
      setBusy(false);
    }
  };
  const pickMine = (id: string) => {
    close();
    resumeInPane(paneId, id);
  };

  const here = Object.values(sessions).filter((s) => s.agent === cur.agent && s.cwd === cur.cwd);
  const byAgentId = new Map(here.map((s) => [s.agentSessionId, s]));
  const entries: Entry[] = [];
  const notes = new Map<string, string>(); // pre-clear contexts already imported: they're gerdr sessions now, just say where they came from
  for (const s of here)
    for (const c of s.cleared ?? []) {
      const note = s.id === cur.id ? "这个会话清空前" : `「${s.title || "没有标题"}」清空前`;
      const to = byAgentId.get(c.id);
      if (to) notes.set(to.id, note);
      else
        entries.push({
          key: `cleared:${s.id}:${c.id}`,
          search: `${c.id} ${c.title ?? ""} ${s.title ?? ""}`,
          title: c.title || c.id.slice(0, 8),
          at: c.at,
          note,
          pick: () => pickNative({ sessionId: c.id, cwd: cur.cwd, title: c.title }),
        });
    }
  for (const s of here)
    // 一句话都没说过的空会话（新建了没用的 pane）没什么可恢复的，不列
    if (s.id !== cur.id && (s.lastSeq > 0 || !!s.title))
      entries.push({
        key: s.id,
        search: `${s.id} ${s.title ?? ""}`,
        title: s.title || "（没有标题）",
        at: s.updatedAt,
        note: notes.get(s.id),
        pick: () => pickMine(s.id),
      });
  for (const n of native ?? [])
    entries.push({
      key: `native:${n.sessionId}`,
      search: `${n.sessionId} ${n.title ?? ""}`,
      title: n.title || n.sessionId.slice(0, 8),
      at: n.updatedAt ? Date.parse(n.updatedAt) : 0,
      pick: () => pickNative(n),
    });
  entries.sort((a, b) => b.at - a.at);

  return (
    <CommandDialog open onOpenChange={(o) => !o && !busy && close()} title="恢复对话" description="换到这个目录下之前的对话" className="max-w-3xl">
      <CommandInput placeholder="搜对话…" />
      {/* 高度写在 style 上、且不许被压：WebKit 里弹窗（网格 + 限高）会把可滚动的这一块压到只剩一行（用户报的，导入弹窗同一个坑） */}
      <CommandList className="max-h-none shrink-0" style={{ height: "min(70vh, 720px)" }}>
        <CommandEmpty>{native ? "这个目录下没有别的对话" : "读取中…"}</CommandEmpty>
        <CommandGroup>
          {/* 当前这个也列出来（置灰、不能选）：一眼看见「我现在在哪个」，和别的对照着挑 */}
          <CommandItem value={`${cur.id} ${cur.title ?? ""}`} disabled className="gap-2.5 opacity-60">
            <AgentIcon agent={cur.agent} className="size-4 shrink-0" />
            <span className="min-w-0 truncate">{cur.title || "（没有标题）"}</span>
            <span className="shrink-0 rounded-sm bg-muted px-1.5 text-[11px] text-muted-foreground">当前</span>
            <span className="ml-auto shrink-0 text-[11px] text-muted-foreground">{ago(cur.updatedAt)}</span>
          </CommandItem>
          {entries.map((e) => (
            <CommandItem key={e.key} value={e.search} className="gap-2.5" disabled={busy} onSelect={e.pick}>
              <AgentIcon agent={cur.agent} className="size-4 shrink-0" />
              <span className="min-w-0 truncate">{e.title}</span>
              {e.note && <span className="max-w-[40%] shrink-0 truncate rounded-sm bg-muted px-1.5 text-[11px] text-muted-foreground">{e.note}</span>}
              <span className="ml-auto shrink-0 text-[11px] text-muted-foreground">{e.at ? ago(e.at) : ""}</span>
            </CommandItem>
          ))}
        </CommandGroup>
        {native === null && (
          <div className="flex items-center gap-2 px-3 py-2 text-[12px] text-muted-foreground">
            <Loader2 className="size-3.5 animate-spin" /> 读 agent 自己的会话记录…
          </div>
        )}
      </CommandList>
    </CommandDialog>
  );
}

function ago(t: number) {
  const s = Math.max(0, (Date.now() - t) / 1000);
  if (s < 60) return "刚刚";
  if (s < 3600) return `${Math.floor(s / 60)} 分钟前`;
  if (s < 86400) return `${Math.floor(s / 3600)} 小时前`;
  return `${Math.floor(s / 86400)} 天前`;
}
