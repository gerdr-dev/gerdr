import { ArrowLeft, Copy, FileText, Loader2, Plus, RefreshCw, Trash2 } from "lucide-react";
import { useEffect, useMemo, useState } from "react";
import { toast } from "sonner";
import { AgentIcon } from "@/components/AgentIcon";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { api } from "@/lib/api";
import { askConfirm } from "@/lib/confirm";
import type { MemoryEntry, MemoryStore } from "@/lib/types";
import { cn } from "@/lib/utils";

// 设置 →「记忆」：三家 agent 跨会话记住的东西（Gerdr Agent 的、Claude Code 的自动记忆、Codex 从会话里总结的）。
// 记忆都是 agent 自己写的 Markdown 文件，这里给人看、改、删；改完 agent 下一轮读到的就是新的。
// 后端只认它们记忆目录里的 .md（internal/memstore）。

const stop = (e: React.KeyboardEvent) => e.stopPropagation();

const AGENTS: { id: MemoryStore["agent"] | "all"; name: string }[] = [
  { id: "all", name: "全部" },
  { id: "native", name: "Gerdr Agent" },
  { id: "claude", name: "Claude Code" },
  { id: "codex", name: "Codex" },
];

const TYPE_NAMES: Record<string, string> = { user: "偏好", feedback: "纠正", project: "项目", reference: "资料" };

function ago(ms: number) {
  const s = (Date.now() - ms) / 1000;
  if (s < 60) return "刚刚";
  if (s < 3600) return `${Math.floor(s / 60)} 分钟前`;
  if (s < 86400) return `${Math.floor(s / 3600)} 小时前`;
  if (s < 86400 * 30) return `${Math.floor(s / 86400)} 天前`;
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
      .catch((e) => toast.error(`读不到记忆：${e.message}`));
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
      toast.success("已保存（agent 下一轮就按新的来）");
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
      title: "删掉这条记忆？",
      description: `${e.name}\n${e.description ?? ""}\n\n索引里指向它的那行也会一起删。`,
      action: "删除",
      destructive: true,
    });
    if (!ok) return;
    try {
      await api.memoryDelete(e.path);
      toast.success("删了");
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
      title: "新的一条",
      content: `---\nname: ${slug}\ndescription: 一句话说这条记的是什么（agent 靠它判断要不要读全文）\ntype: ${type}\n---\n\n要记住的事。\n`,
      saved: "",
      isNew: true,
    });
  };

  if (!stores) {
    return (
      <div className="flex items-center gap-2 py-6 text-[13px] text-muted-foreground">
        <Loader2 className="size-4 animate-spin" /> 读记忆…
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
            title="返回"
            onClick={async () => (!dirty || (await askConfirm({ title: "还没保存，放弃改动？", action: "放弃", destructive: true }))) && setEdit(null)}
          >
            <ArrowLeft className="size-4" />
          </Button>
          <span className="min-w-0 truncate font-medium">{edit.title}</span>
          <span className="min-w-0 flex-1 truncate font-mono text-[11px] text-muted-foreground">{edit.path}</span>
          {entry && (
            <Button size="sm" variant="ghost" className="text-bad" onClick={() => remove(entry)}>
              <Trash2 className="size-3.5" /> 删除
            </Button>
          )}
          <Button size="sm" onClick={save} disabled={busy || (!dirty && !edit.isNew)}>
            保存
          </Button>
        </div>
        {edit.index && (
          <div className="text-[12px] text-muted-foreground">
            索引：一条记忆一行（- [标题](文件名.md) — 一句话）。agent 每轮都会读到它，只靠它决定要不要去读哪条的全文。
          </div>
        )}
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
      <div className="text-[12px] text-muted-foreground">
        agent 跨会话记住的东西：你纠正过它的、你的偏好、项目的约定。都是它自己写的 Markdown 文件，在这里看、改、删，下一轮就按新的来。
      </div>
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
              {a.name}
            </button>
          ))}
        </div>
        <Input value={q} onChange={(e) => setQ(e.target.value)} onKeyDown={stop} className="h-8 w-48 flex-1 text-[12px]" placeholder="搜记忆" />
        <Button size="icon" variant="ghost" className="size-8" title="刷新" onClick={load}>
          <RefreshCw className="size-3.5" />
        </Button>
      </div>

      {shown.length === 0 ? (
        <div className="py-6 text-center text-muted-foreground">{q ? "没搜到" : "还没有记忆"}</div>
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
                    title="复制路径"
                    onClick={() => navigator.clipboard.writeText(cur.dir).then(() => toast.success("已复制路径"))}
                  >
                    <span className="truncate">{cur.dir}</span>
                    <Copy className="size-3 shrink-0" />
                  </button>
                </div>
                {cur.agent !== "codex" && (
                  <>
                    <Button size="sm" variant="ghost" onClick={() => openFile(`${cur.dir}/MEMORY.md`, "索引 MEMORY.md", true)}>
                      <FileText className="size-3.5" /> 索引
                    </Button>
                    <Button size="sm" variant="outline" onClick={newEntry}>
                      <Plus className="size-3.5" /> 新建一条
                    </Button>
                  </>
                )}
              </div>
              {cur.entries.length === 0 ? (
                <div className="rounded-md border border-dashed px-3 py-6 text-center text-[12px] text-muted-foreground">
                  {cur.agent === "codex"
                    ? "Codex 还没有生成记忆（它的记忆是从过去的会话里自动总结的，要在 Codex 里开了记忆功能才会有）"
                    : "还没有记忆：agent 遇到该记的事（你纠正它、说了偏好）会自己写进来，也可以手动新建"}
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
                          <span className="shrink-0 rounded-sm bg-muted px-1.5 text-[11px] text-muted-foreground">{TYPE_NAMES[e.type] ?? e.type}</span>
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
