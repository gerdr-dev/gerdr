import { Loader2 } from "lucide-react";
import { useEffect, useState } from "react";
import { toast } from "sonner";
import { AgentIcon } from "@/components/AgentIcon";
import { CommandDialog, CommandEmpty, CommandGroup, CommandInput, CommandItem, CommandList } from "@/components/ui/command";
import { continueInPane, resumeInPane } from "@/lib/actions";
import { api } from "@/lib/api";
import { isOpen } from "@/lib/overviewState";
import { set, useStore } from "@/lib/store";
import { t } from "@/lib/i18n";

// /resume (like Claude Code): switch this pane to an earlier conversation in the same directory.
// claude in stream-json mode doesn't take /resume (it's the interactive picker), so gerdr does it, from one list sorted by time:
//   - gerdr's other sessions in this directory, whatever agent had them (closed ones too; picking one resumes it). That
//     includes conversations a /clear left behind: the hub splits them off into sessions of their own, under the title they
//     had (hub/clear.go). One of another agent is carried on by this pane's agent (continueInPane, hub/handoff.go), the
//     same as on the new-session page: which agent answers is the pane's, the history is everyone's;
//   - the agent's own sessions gerdr hasn't seen (run in a terminal): picking one imports it.
// Where a conversation came from isn't what you search by; when you last had it is.
// The session switched away from isn't deleted; it's still in the sessions list.

type Native = { sessionId: string; cwd: string; title?: string; updatedAt?: string };
type Entry = { key: string; agent: string; search: string; title: string; at: number; open?: boolean; pick: () => void };

export function ResumeDialog() {
  const d = useStore((s) => s.dialog);
  const open = d?.kind === "resume";
  const cur = useStore((s) => (open ? s.sessions[d.sessionId] : undefined));
  const sessions = useStore((s) => s.sessions);
  const agents = useStore((s) => s.agents);
  const layout = useStore((s) => s.layout);
  const config = useStore((s) => s.config);
  const [native, setNative] = useState<Native[] | null>(null);
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    if (!open || !cur) return;
    setNative(null);
    api.importable(cur.agent, cur.cwd).then(setNative, () => setNative([]));
  }, [open, cur?.id]);

  if (!open || !cur) return null;
  const agentName = agents.find((a) => a.id === cur.agent)?.name ?? cur.agent;
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
  const pickMine = async (id: string) => {
    if (sessions[id]?.agent === cur.agent) {
      close();
      return resumeInPane(paneId, id);
    }
    setBusy(true);
    try {
      await continueInPane(paneId, id, cur.agent);
      close();
    } catch {
      // continueInPane said why
    } finally {
      setBusy(false);
    }
  };

  const here = Object.values(sessions).filter((s) => s.cwd === cur.cwd);
  const entries: Entry[] = [];
  for (const s of here)
    // Nothing said in it, nothing to resume: a pane opened and left unused, or what a /clear leaves behind (only the
    // /clear itself). The first real message titles a session right away (hub touchTitle), so no title means no message.
    if (s.id !== cur.id && !!s.title)
      entries.push({
        key: s.id,
        agent: s.agent,
        search: `${s.id} ${s.title ?? ""}`,
        title: s.title || t("resumeDialog.untitled"),
        at: s.updatedAt,
        open: isOpen(layout, config, s.id),
        pick: () => pickMine(s.id),
      });
  for (const n of native ?? [])
    entries.push({
      key: `native:${n.sessionId}`,
      agent: cur.agent,
      search: `${n.sessionId} ${n.title ?? ""}`,
      title: n.title || n.sessionId.slice(0, 8),
      at: n.updatedAt ? Date.parse(n.updatedAt) : 0,
      pick: () => pickNative(n),
    });
  entries.sort((a, b) => b.at - a.at);

  return (
    <CommandDialog
      open
      onOpenChange={(o) => !o && !busy && close()}
      title={t("resumeDialog.title")}
      description={t("resumeDialog.description")}
      className="max-w-3xl"
    >
      <CommandInput placeholder={t("resumeDialog.search")} />
      {/* 高度写在 style 上、且不许被压：WebKit 里弹窗（网格 + 限高）会把可滚动的这一块压到只剩一行（用户报的，导入弹窗同一个坑） */}
      <CommandList className="max-h-none shrink-0" style={{ height: "min(70vh, 720px)" }}>
        <CommandEmpty>{native ? t("resumeDialog.empty") : t("resumeDialog.reading")}</CommandEmpty>
        <CommandGroup>
          {/* 当前这个也列出来（置灰、不能选）：一眼看见「我现在在哪个」，和别的对照着挑 */}
          <CommandItem value={`${cur.id} ${cur.title ?? ""}`} disabled className="gap-2.5 opacity-60">
            <AgentIcon agent={cur.agent} className="size-4 shrink-0" />
            <span className="min-w-0 truncate">{cur.title || t("resumeDialog.untitled")}</span>
            <span className="shrink-0 rounded-sm bg-muted px-1.5 text-[11px] text-muted-foreground">{t("resumeDialog.current")}</span>
            <span className="ml-auto shrink-0 text-[11px] text-muted-foreground">{ago(cur.updatedAt)}</span>
          </CommandItem>
          {entries.map((e) => (
            // cmdk tracks the highlighted item by value: two equal values light up together, so the key keeps it unique
            <CommandItem key={e.key} value={`${e.key} ${e.search}`} className="gap-2.5" disabled={busy} onSelect={e.pick}>
              <AgentIcon agent={e.agent} className="size-4 shrink-0" />
              <span className="min-w-0 truncate">{e.title}</span>
              {/* Shown in a pane / Work view already: picking it brings that one over instead of opening it again */}
              {e.open && <span className="shrink-0 rounded-sm bg-muted px-1.5 text-[11px] text-muted-foreground">{t("newSession.opened")}</span>}
              {e.agent !== cur.agent && <span className="shrink-0 text-[11px] text-muted-foreground">{t("newSession.handoff", { agent: agentName })}</span>}
              <span className="ml-auto shrink-0 text-[11px] text-muted-foreground">{e.at ? ago(e.at) : ""}</span>
            </CommandItem>
          ))}
        </CommandGroup>
        {native === null && (
          <div className="flex items-center gap-2 px-3 py-2 text-[12px] text-muted-foreground">
            <Loader2 className="size-3.5 animate-spin" /> {t("resumeDialog.readingNative")}
          </div>
        )}
      </CommandList>
    </CommandDialog>
  );
}

function ago(at: number) {
  const s = Math.max(0, (Date.now() - at) / 1000);
  if (s < 60) return t("newSession.ago.now");
  if (s < 3600) return t("newSession.ago.min", { n: Math.floor(s / 60) });
  if (s < 86400) return t("newSession.ago.hour", { n: Math.floor(s / 3600) });
  return t("newSession.ago.day", { n: Math.floor(s / 86400) });
}
