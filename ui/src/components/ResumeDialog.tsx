import { Loader2 } from "lucide-react";
import { useEffect, useState } from "react";
import { toast } from "sonner";
import { AgentIcon } from "@/components/AgentIcon";
import { CommandDialog, CommandEmpty, CommandGroup, CommandInput, CommandItem, CommandList } from "@/components/ui/command";
import { resumeInPane } from "@/lib/actions";
import { api } from "@/lib/api";
import { set, useStore } from "@/lib/store";

// /resume（照 Claude Code）：在这个 pane 里换到同一个目录下之前的某段对话。
// stream-json 模式下 claude 自己不认 /resume（那是交互界面里的选择器），所以这一步 gerdr 做：
//   - gerdr 里这个 agent + 目录的其它会话（关掉的也在，接着聊走 resume）；
//   - agent 自己的会话记录里、还没进 gerdr 的（在终端里跑过的那些），选中就接进来。
// 换走的那个会话不删，在「会话」列表里还找得到。

type Native = { sessionId: string; cwd: string; title?: string; updatedAt?: string };

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
  const mine = Object.values(sessions)
    // 一句话都没说过的空会话（新建了没用的 pane）没什么可恢复的，不列
    .filter((s) => s.id !== cur.id && s.agent === cur.agent && s.cwd === cur.cwd && (s.lastSeq > 0 || !!s.title))
    .sort((a, b) => b.updatedAt - a.updatedAt);

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

  return (
    <CommandDialog open onOpenChange={(o) => !o && !busy && close()} title="恢复对话" description="换到这个目录下之前的对话" className="max-w-3xl">
      <CommandInput placeholder="搜对话…" />
      {/* 高度写在 style 上、且不许被压：WebKit 里弹窗（网格 + 限高）会把可滚动的这一块压到只剩一行（用户报的，导入弹窗同一个坑） */}
      <CommandList className="max-h-none shrink-0" style={{ height: "min(70vh, 720px)" }}>
        <CommandEmpty>{native ? "这个目录下没有别的对话" : "读取中…"}</CommandEmpty>
        <CommandGroup heading="gerdr 里的">
          {/* 当前这个也列出来（置灰、不能选）：一眼看见「我现在在哪个」，和别的对照着挑 */}
          <CommandItem value={`${cur.id} ${cur.title ?? ""}`} disabled className="gap-2.5 opacity-60">
            <AgentIcon agent={cur.agent} className="size-4 shrink-0" />
            <span className="min-w-0 truncate">{cur.title || "（没有标题）"}</span>
            <span className="shrink-0 rounded-sm bg-muted px-1.5 text-[11px] text-muted-foreground">当前</span>
            <span className="ml-auto shrink-0 text-[11px] text-muted-foreground">{ago(cur.updatedAt)}</span>
          </CommandItem>
          {mine.map((s) => (
            <CommandItem
              key={s.id}
              value={`${s.id} ${s.title ?? ""}`}
              className="gap-2.5"
              onSelect={() => {
                close();
                resumeInPane(paneId, s.id);
              }}
            >
              <AgentIcon agent={s.agent} className="size-4 shrink-0" />
              <span className="min-w-0 truncate">{s.title || "（没有标题）"}</span>
              <span className="ml-auto shrink-0 text-[11px] text-muted-foreground">{ago(s.updatedAt)}</span>
            </CommandItem>
          ))}
        </CommandGroup>
        {native === null && (
          <div className="flex items-center gap-2 px-3 py-2 text-[12px] text-muted-foreground">
            <Loader2 className="size-3.5 animate-spin" /> 读 agent 自己的会话记录…
          </div>
        )}
        {native && native.length > 0 && (
          <CommandGroup heading="agent 自己记录里的（终端里跑过的）">
            {native.map((n) => (
              <CommandItem key={n.sessionId} value={`${n.sessionId} ${n.title ?? ""}`} className="gap-2.5" disabled={busy} onSelect={() => pickNative(n)}>
                <AgentIcon agent={cur.agent} className="size-4 shrink-0" />
                <span className="min-w-0 truncate">{n.title || n.sessionId.slice(0, 8)}</span>
                <span className="ml-auto shrink-0 text-[11px] text-muted-foreground">{n.updatedAt ? ago(Date.parse(n.updatedAt)) : ""}</span>
              </CommandItem>
            ))}
          </CommandGroup>
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
