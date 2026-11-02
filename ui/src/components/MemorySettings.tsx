import { ArrowLeft, Copy, FileText, Loader2, Plus, RefreshCw, Trash2 } from "lucide-react";
import { useEffect, useMemo, useState } from "react";
import { toast } from "sonner";
import { AgentIcon } from "@/components/AgentIcon";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { api } from "@/lib/api";
import { askConfirm } from "@/lib/confirm";
import { t } from "@/lib/i18n";
import type { MessageKey } from "@/lib/messages";
import type { MemoryEntry, MemoryStore } from "@/lib/types";
import { cn } from "@/lib/utils";

// 设置 →「记忆」：三家 agent 跨会话记住的东西（Gerdr Agent 的、Claude Code 的自动记忆、Codex 从会话里总结的）。
// 记忆都是 agent 自己写的 Markdown 文件，这里给人看、改、删；改完 agent 下一轮读到的就是新的。
// 后端只认它们记忆目录里的 .md（internal/memstore）。

const stop = (e: React.KeyboardEvent) => e.stopPropagation();

const AGENTS: { id: MemoryStore["agent"] | "all"; name: string }[] = [
  { id: "all", name: "" }, // shown as t("memorySettings.all")
  { id: "native", name: "Gerdr Agent" },
  { id: "claude", name: "Claude Code" },
  { id: "codex", name: "Codex" },
];

const TYPE_NAMES: Record<string, MessageKey> = {
  user: "memorySettings.type.user",
  feedback: "memorySettings.type.feedback",
  project: "memorySettings.type.project",
  reference: "memorySettings.type.reference",
};

function ago(ms: number) {
  const s = (Date.now() - ms) / 1000;
  if (s < 60) return t("memorySettings.justNow");
  if (s < 3600) return t("memorySettings.minutesAgo", { n: Math.floor(s / 60) });
  if (s < 86400) return t("memorySettings.hoursAgo", { n: Math.floor(s / 3600) });
  if (s < 86400 * 30) return t("memorySettings.daysAgo", { n: Math.floor(s / 86400) });
  return new Date(ms).toLocaleDateString();
}

// 编辑中的那一个：一条记忆，或者某个目录的 MEMORY.md
type Editing = { path: string; title: string; content: string; saved: string; isNew?: boolean; index?: boolean };

export function MemorySettings({ open }: { open: boolean }) {
  const [stores, setStores] = useState<MemoryStore[] | null>(null);
  const [agent, setAgent] = useState<(typeof AGENTS)[number]["id"]>("all");
  const [q, setQ] = useState("");
  const [sel, setSel] = useState<string | null>(null);
  const [edit, setEdit] = useState<Editing | null>(null);
  const [busy, setBusy] = useState(false);

  const load = () =>
    api
      .memory()
      .then((r) => setStores(r.stores))
      .catch((e) => toast.error(t("memorySettings.loadFailed", { error: e.message })));
  useEffect(() => {
    if (open) load();
  }, [open]);

  const shown = useMemo(() => {
    const k = q.trim().toLowerCase();
    return (stores ?? [])
      .filter((s) => agent === "all" || s.agent === agent)
      .map((s) => (k ? { ...s, entries: s.entries.filter((e) => [e.name, e.description, e.file].some((x) => x?.toLowerCase().includes(k))) } : s))
      .filter((s) => !k || s.entries.length > 0 || s.label.toLowerCase().includes(k));
  }, [stores, agent, q]);
  const cur = shown.find((s) => s.id === sel) ?? shown[0];

  const openFile = async (path: string, title: string, index?: boolean) => {
    setBusy(true);
    try {
      const r = await api.memoryRead(path);
      setEdit({ path, title, content: r.content, saved: r.content, index });
    } catch (e: any) {
      // 还没有索引：从空的开始写
      if (index) setEdit({ path, title, content: "", saved: "", index, isNew: true });
      else toast.error(e.message);
    } finally {
      setBusy(false);
    }
  };

  const save = async () => {
    if (!edit) return;
    setBusy(true);
    try {
      await api.memoryWrite(edit.path, edit.content);
      // 新建的一条：顺手在索引里加一行（agent 只看索引决定读哪条，不在索引里等于没记）
      if (edit.isNew && !edit.index && cur) {
        const idx = `${cur.dir}/MEMORY.md`;
        const old = await api.memoryRead(idx).then(
          (r) => r.content,
          () => "",
        );
        const name = /^name:\s*(.+)$/m.exec(edit.content)?.[1]?.trim() || edit.title;
        const desc = /^description:\s*(.+)$/m.exec(edit.content)?.[1]?.trim() || "";
        const file = edit.path.split("/").pop();
        await api.memoryWrite(idx, `${old.replace(/\n*$/, old ? "\n" : "")}- [${name}](${file})${desc ? ` — ${desc}` : ""}\n`);
      }
      toast.success(t("memorySettings.saved"));
      setEdit({ ...edit, saved: edit.content, isNew: false });
      load();
    } catch (e: any) {
      toast.error(e.message);
    } finally {
      setBusy(false);
    }
  };

  const remove = async (e: MemoryEntry) => {
    const ok = await askConfirm({
      title: t("memorySettings.deleteTitle"),
      description: t("memorySettings.deleteDescription", { name: e.name ?? "", description: e.description ?? "" }),
      action: t("common.delete"),
      destructive: true,
    });
    if (!ok) return;
    try {
      await api.memoryDelete(e.path);
      toast.success(t("memorySettings.deleted"));
      setEdit(null);
      load();
    } catch (err: any) {
      toast.error(err.message);
    }
  };

  const newEntry = () => {
    if (!cur) return;
    const slug = `memory-${Date.now().toString(36)}`;
    const type = cur.scope === "global" ? "user" : "project";
    setEdit({
      path: `${cur.dir}/${slug}.md`,
      title: t("memorySettings.newTitle"),
      content: `---\nname: ${slug}\ndescription: ${t("memorySettings.templateDescription")}\ntype: ${type}\n---\n\n${t("memorySettings.templateBody")}\n`,
      saved: "",
      isNew: true,
    });
  };

  if (!stores) {
    return (
      <div className="flex items-center gap-2 py-6 text-[13px] text-muted-foreground">
        <Loader2 className="size-4 animate-spin" /> {t("memorySettings.loading")}
      </div>
    );
  }

  if (edit) {
    const dirty = edit.content !== edit.saved;
    const entry = cur?.entries.find((e) => e.path === edit.path);
    return (
      <div className="space-y-2 text-[13px]">
        <div className="flex items-center gap-2">
          <Button
            size="icon"
            variant="ghost"
            className="size-7"
            title={t("common.back")}
            onClick={async () =>
              (!dirty || (await askConfirm({ title: t("memorySettings.discardTitle"), action: t("memorySettings.discard"), destructive: true }))) &&
              setEdit(null)
            }
          >
            <ArrowLeft className="size-4" />
          </Button>
          <span className="min-w-0 truncate font-medium">{edit.title}</span>
          <span className="min-w-0 flex-1 truncate font-mono text-[11px] text-muted-foreground">{edit.path}</span>
          {entry && (
            <Button size="sm" variant="ghost" className="text-bad" onClick={() => remove(entry)}>
              <Trash2 className="size-3.5" /> {t("common.delete")}
            </Button>
          )}
          <Button size="sm" onClick={save} disabled={busy || (!dirty && !edit.isNew)}>
            {t("common.save")}
          </Button>
        </div>
        {edit.index && <div className="text-[12px] text-muted-foreground">{t("memorySettings.indexHint")}</div>}
        <textarea
          className="h-[52vh] w-full resize-none rounded-md border bg-card p-3 font-mono text-[12px] leading-relaxed"
          value={edit.content}
          spellCheck={false}
          autoFocus
          onChange={(e) => setEdit({ ...edit, content: e.target.value })}
          onKeyDown={(e) => {
            stop(e);
            if ((e.metaKey || e.ctrlKey) && e.key === "s") {
              e.preventDefault();
              save();
            }
          }}
        />
      </div>
    );
  }

  return (
    <div className="space-y-3 text-[13px]">
      <div className="text-[12px] text-muted-foreground">{t("memorySettings.intro")}</div>
      <div className="flex flex-wrap items-center gap-2">
        <div className="flex rounded-md bg-muted p-0.5">
          {AGENTS.map((a) => (
            <button
              key={a.id}
              className={cn("rounded-sm px-2.5 py-1 text-[12px] text-muted-foreground", agent === a.id && "bg-background text-foreground shadow-sm")}
              onClick={() => {
                setAgent(a.id);
                setSel(null);
              }}
            >
              {a.id === "all" ? t("memorySettings.all") : a.name}
            </button>
          ))}
        </div>
        <Input
          value={q}
          onChange={(e) => setQ(e.target.value)}
          onKeyDown={stop}
          className="h-8 w-48 flex-1 text-[12px]"
          placeholder={t("memorySettings.search")}
        />
        <Button size="icon" variant="ghost" className="size-8" title={t("common.refresh")} onClick={load}>
          <RefreshCw className="size-3.5" />
        </Button>
      </div>

      {shown.length === 0 ? (
        <div className="py-6 text-center text-muted-foreground">{q ? t("memorySettings.noResults") : t("memorySettings.empty")}</div>
      ) : (
        <div className="grid gap-3 md:grid-cols-[220px_1fr]">
          {/* 左边：记忆目录（手机上横着滑） */}
          <div className="flex gap-1 overflow-x-auto md:max-h-[52vh] md:flex-col md:overflow-x-hidden md:overflow-y-auto">
            {shown.map((s) => (
              <button
                key={s.id}
                className={cn(
                  "flex min-w-0 shrink-0 items-center gap-2 rounded-md px-2 py-1.5 text-left hover:bg-muted max-md:max-w-56",
                  cur?.id === s.id && "bg-muted",
                )}
                title={s.dir}
                onClick={() => setSel(s.id)}
              >
                <AgentIcon agent={s.agent} className="size-3.5 shrink-0" />
                <span className="min-w-0 flex-1 truncate">{s.scope === "global" ? s.label : shortPath(s.label)}</span>
                <span className="shrink-0 text-[11px] text-muted-foreground tabular-nums">{s.entries.length}</span>
              </button>
            ))}
          </div>

          {/* 右边：这个目录里的记忆 */}
          {cur && (
            <div className="min-w-0 space-y-2">
              <div className="flex items-center gap-2">
                <div className="min-w-0 flex-1">
                  <div className="truncate font-medium">{cur.label}</div>
                  <button
                    className="flex max-w-full items-center gap-1 truncate font-mono text-[11px] text-muted-foreground hover:text-foreground"
                    title={t("memorySettings.copyPath")}
                    onClick={() => navigator.clipboard.writeText(cur.dir).then(() => toast.success(t("memorySettings.pathCopied")))}
                  >
                    <span className="truncate">{cur.dir}</span>
                    <Copy className="size-3 shrink-0" />
                  </button>
                </div>
                {cur.agent !== "codex" && (
                  <>
                    <Button size="sm" variant="ghost" onClick={() => openFile(`${cur.dir}/MEMORY.md`, t("memorySettings.indexTitle"), true)}>
                      <FileText className="size-3.5" /> {t("memorySettings.index")}
                    </Button>
                    <Button size="sm" variant="outline" onClick={newEntry}>
                      <Plus className="size-3.5" /> {t("memorySettings.newEntry")}
                    </Button>
                  </>
                )}
              </div>
              {cur.entries.length === 0 ? (
                <div className="rounded-md border border-dashed px-3 py-6 text-center text-[12px] text-muted-foreground">
                  {cur.agent === "codex" ? t("memorySettings.codexEmpty") : t("memorySettings.emptyHint")}
                </div>
              ) : (
                <div className="divide-y rounded-md border">
                  {cur.entries.map((e) => (
                    <button
                      key={e.path}
                      className="group/mem block w-full px-3 py-2 text-left hover:bg-muted/50"
                      onClick={() => openFile(e.path, e.name || e.file)}
                    >
                      <div className="flex items-center gap-2">
                        <span className="min-w-0 truncate font-medium">{e.name || e.file}</span>
                        {e.type && (
                          <span className="shrink-0 rounded-sm bg-muted px-1.5 text-[11px] text-muted-foreground">
                            {TYPE_NAMES[e.type] ? t(TYPE_NAMES[e.type]) : e.type}
                          </span>
                        )}
                        <span className="flex-1" />
                        <span className="shrink-0 text-[11px] text-muted-foreground">{ago(e.modTime)}</span>
                      </div>
                      {e.description && <div className="mt-0.5 line-clamp-2 text-[12px] text-muted-foreground">{e.description}</div>}
                    </button>
                  ))}
                </div>
              )}
            </div>
          )}
        </div>
      )}
    </div>
  );
}

/** 项目路径只留最后两段（侧栏窄），完整的在右边标题上 */
function shortPath(p: string) {
  const parts = p.split("/").filter(Boolean);
  return parts.length > 2 ? `…/${parts.slice(-2).join("/")}` : p;
}
