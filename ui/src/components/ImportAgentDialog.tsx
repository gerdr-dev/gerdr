import { Loader2 } from "lucide-react";
import { useEffect, useState } from "react";
import { toast } from "sonner";
import { AgentIcon } from "@/components/AgentIcon";
import { CommandDialog, CommandEmpty, CommandGroup, CommandInput, CommandItem, CommandList } from "@/components/ui/command";
import { createInPane } from "@/lib/actions";
import { api } from "@/lib/api";
import { set, useStore } from "@/lib/store";
import { ago } from "./NewSession";

// "Import Claude Code conversations…" on a new-session pane: the agent's own conversations in that directory that gerdr
// doesn't manage yet (run in a terminal, or from before gerdr), read from the agent's records (hub.ListImportable). Picking
// one imports it into this pane. The pane's own "earlier conversations" list shows gerdr's sessions only: conversations are
// meant to live in gerdr, so the agent's native history is a one-off import, not something to scroll past every time.

type Native = { sessionId: string; cwd: string; title?: string; updatedAt?: string };

export function ImportAgentDialog() {
  const d = useStore((s) => s.dialog);
  const open = d?.kind === "importAgent";
  const agentName = useStore((s) => (open ? (s.agents.find((a) => a.id === d.agent)?.name ?? d.agent) : ""));
  const [list, setList] = useState<Native[] | null>(null);
  const [err, setErr] = useState("");
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    if (!open) return;
    setList(null);
    setErr("");
    api.importable(d.agent, d.cwd).then(setList, (e) => setErr(e.message ?? String(e)));
  }, [open, open && d.agent, open && d.cwd]);

  if (!open) return null;
  const close = () => set({ dialog: null });
  const pick = async (n: Native) => {
    setBusy(true);
    try {
      close();
      await createInPane(d.paneId, { agent: d.agent, cwd: d.cwd, import: n.sessionId, title: n.title });
    } catch (e: any) {
      toast.error(e.message ?? String(e));
    } finally {
      setBusy(false);
    }
  };

  return (
    <CommandDialog open onOpenChange={(o) => !o && close()} title={`导入 ${agentName} 对话`} description={d.cwd}>
      <CommandInput placeholder={`导入 ${agentName} 在这个目录下的对话…`} />
      <CommandList>
        {list && <CommandEmpty>这个目录下没有还没导入的 {agentName} 对话</CommandEmpty>}
        {err && <div className="px-3 py-2 text-[12px] text-bad">{err}</div>}
        {!list && !err && (
          <div className="flex items-center gap-2 px-3 py-2 text-[12px] text-muted-foreground">
            <Loader2 className="size-3.5 animate-spin" /> 读 {agentName} 的对话记录…
          </div>
        )}
        {list && list.length > 0 && (
          <CommandGroup heading={d.cwd}>
            {list.map((n) => (
              // cmdk tracks the highlighted item by value: the id keeps equal titles apart
              <CommandItem key={n.sessionId} value={`${n.sessionId} ${n.title ?? ""}`} className="gap-2.5" disabled={busy} onSelect={() => pick(n)}>
                <AgentIcon agent={d.agent} className="size-4 shrink-0" />
                <span className="min-w-0 truncate">{n.title || n.sessionId.slice(0, 8)}</span>
                {n.updatedAt && <span className="ml-auto shrink-0 text-[11px] text-muted-foreground">{ago(n.updatedAt)}</span>}
              </CommandItem>
            ))}
          </CommandGroup>
        )}
      </CommandList>
    </CommandDialog>
  );
}
