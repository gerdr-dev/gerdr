import { Copy, Pencil, Plus, Trash2 } from "lucide-react";
import { useState } from "react";
import { toast } from "sonner";
import { AgentIcon } from "@/components/AgentIcon";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { setConfig, useStore, type AgentDef } from "@/lib/store";

// 设置 →「Agent」。除了内置的 Claude Code / Codex，可以再加几个：
//   - 变体：同一个 claude / codex，换一套环境变量 / 参数 —— 最常见的是「另一个账号」（Claude 2）；
//   - 外部 ACP agent：自己会说 ACP 的程序（gemini --experimental-acp 这种）。
// 存进 config.json 的 agents，服务端热换（改了的那个，正在跑的进程收掉，会话下一轮按新配置接上）。

const stop = (e: React.KeyboardEvent) => e.stopPropagation();
const NONE: AgentDef[] = []; // 选择器里别 `?? []`：每次一个新数组，zustand 认成变了，无限重渲染

type Kind = "claude" | "codex" | "command";
type Draft = { id: string; name: string; kind: Kind; env: string; bin: string; args: string; command: string };

const toDraft = (d: AgentDef): Draft => ({
  id: d.id,
  name: d.name,
  kind: d.base === "claude" || d.base === "codex" ? d.base : "command",
  env: Object.entries(d.env ?? {})
    .map(([k, v]) => `${k}=${v}`)
    .join("\n"),
  bin: d.bin ?? "",
  args: (d.args ?? []).join(" "),
  command: (d.command ?? []).join(" "),
});

function fromDraft(d: Draft): AgentDef {
  const env: Record<string, string> = {};
  for (const line of d.env.split("\n")) {
    const t = line.trim();
    if (!t || t.startsWith("#")) continue;
    const i = t.indexOf("=");
    if (i <= 0) throw new Error(`环境变量要写成 KEY=VALUE：${t}`);
    env[t.slice(0, i).trim()] = t.slice(i + 1).trim();
  }
  const words = (s: string) => s.split(/\s+/).filter(Boolean);
  if (!d.name.trim()) throw new Error("起个名字");
  if (d.kind === "command") {
    if (!d.command.trim()) throw new Error("外部 agent 要填启动命令");
    return { id: d.id, name: d.name.trim(), command: words(d.command), env: Object.keys(env).length ? env : undefined };
  }
  return {
    id: d.id,
    name: d.name.trim(),
    base: d.kind,
    env: Object.keys(env).length ? env : undefined,
    bin: d.bin.trim() || undefined,
    args: words(d.args).length ? words(d.args) : undefined,
  };
}

function newId(name: string, taken: Set<string>) {
  const base = name.toLowerCase().replace(/[^a-z0-9]+/g, "-").replace(/^-|-$/g, "") || "agent";
  let id = base;
  for (let n = 2; taken.has(id); n++) id = `${base}-${n}`;
  return id;
}

export function AgentSettings() {
  const agents = useStore((s) => s.agents);
  const defs = useStore((s) => s.config.agents ?? NONE);
  const [edit, setEdit] = useState<Draft | null>(null);
  const [isNew, setIsNew] = useState(false);

  const taken = new Set(["claude", "codex", ...defs.map((d) => d.id)]);
  const add = (kind: Kind) => {
    const n = defs.filter((d) => (d.base ?? "command") === kind).length + 2;
    const name = kind === "claude" ? `Claude ${n}` : kind === "codex" ? `Codex ${n}` : "Gemini";
    const id = newId(name, taken);
    setIsNew(true);
    setEdit({
      id,
      name,
      kind,
      env: kind === "claude" ? `CLAUDE_CONFIG_DIR=~/.${id}` : kind === "codex" ? `CODEX_HOME=~/.${id}` : "",
      bin: "",
      args: "",
      command: kind === "command" ? "gemini --experimental-acp" : "",
    });
  };
  const save = () => {
    if (!edit) return;
    try {
      const d = fromDraft(edit);
      setConfig({ agents: isNew ? [...defs, d] : defs.map((x) => (x.id === d.id ? d : x)) });
      toast.success(isNew ? `加好了：新建会话时选「${d.name}」` : "已保存（正在跑的会话下一轮按新配置接上）");
      setEdit(null);
    } catch (e: any) {
      toast.error(e.message);
    }
  };

  return (
    <div className="space-y-4 text-[13px]">
      <div className="space-y-1.5">
        {agents.map((a) => {
          const def = defs.find((d) => d.id === a.id);
          return (
            <div key={a.id} className="flex items-center gap-2 rounded-md border px-3 py-2">
              <AgentIcon agent={a.id} className="size-4" />
              <span className="font-medium">{a.name}</span>
              <span className="text-[12px] text-muted-foreground">
                {!def ? "内置" : def.base === "claude" ? "Claude Code 变体" : def.base === "codex" ? "Codex 变体" : "外部 ACP"}
              </span>
              {a.available ? <span className="text-[12px] text-ok">可用</span> : <span className="truncate text-[12px] text-bad">{a.error || "不可用"}</span>}
              <span className="flex-1" />
              {def && (
                <>
                  <Button
                    size="icon"
                    variant="ghost"
                    className="size-7"
                    title="编辑"
                    onClick={() => {
                      setIsNew(false);
                      setEdit(toDraft(def));
                    }}
                  >
                    <Pencil className="size-3.5" />
                  </Button>
                  <Button
                    size="icon"
                    variant="ghost"
                    className="size-7"
                    title="删除（已有的会话留着，只是没法再接着聊）"
                    onClick={() => setConfig({ agents: defs.filter((x) => x.id !== def.id) })}
                  >
                    <Trash2 className="size-3.5" />
                  </Button>
                </>
              )}
            </div>
          );
        })}
      </div>

      {!edit && (
        <div className="flex flex-wrap gap-2">
          <Button size="sm" variant="outline" onClick={() => add("claude")}>
            <Plus className="size-3.5" /> 加一个 Claude（另一个账号…）
          </Button>
          <Button size="sm" variant="outline" onClick={() => add("codex")}>
            <Plus className="size-3.5" /> 加一个 Codex
          </Button>
          <Button size="sm" variant="ghost" onClick={() => add("command")}>
            <Plus className="size-3.5" /> 外部 ACP agent
          </Button>
        </div>
      )}

      {edit && <Editor d={edit} onChange={setEdit} onSave={save} onCancel={() => setEdit(null)} />}
    </div>
  );
}

function Editor({ d, onChange, onSave, onCancel }: { d: Draft; onChange: (d: Draft) => void; onSave: () => void; onCancel: () => void }) {
  const set = (p: Partial<Draft>) => onChange({ ...d, ...p });
  const dir = /^CLAUDE_CONFIG_DIR=(.+)$/m.exec(d.env)?.[1]?.trim();
  const codexHome = /^CODEX_HOME=(.+)$/m.exec(d.env)?.[1]?.trim();
  const login = d.kind === "claude" && dir ? `CLAUDE_CONFIG_DIR=${dir} claude` : d.kind === "codex" && codexHome ? `CODEX_HOME=${codexHome} codex login` : "";
  return (
    <div className="space-y-3 rounded-lg border bg-muted/30 p-3">
      <div className="grid grid-cols-[88px_1fr] items-center gap-x-3 gap-y-2.5">
        <span className="text-muted-foreground">名字</span>
        <Input value={d.name} onChange={(e) => set({ name: e.target.value })} onKeyDown={stop} className="h-8" />
        <span className="text-muted-foreground">基于</span>
        <Select value={d.kind} onValueChange={(v) => set({ kind: v as Kind })}>
          <SelectTrigger size="sm" className="w-56">
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value="claude">Claude Code</SelectItem>
            <SelectItem value="codex">Codex</SelectItem>
            <SelectItem value="command">外部 ACP agent</SelectItem>
          </SelectContent>
        </Select>
        {d.kind === "command" ? (
          <>
            <span className="text-muted-foreground">启动命令</span>
            <Input value={d.command} onChange={(e) => set({ command: e.target.value })} onKeyDown={stop} className="h-8 font-mono text-[12px]" placeholder="gemini --experimental-acp" />
          </>
        ) : (
          <>
            <span className="text-muted-foreground">可执行文件</span>
            <Input value={d.bin} onChange={(e) => set({ bin: e.target.value })} onKeyDown={stop} className="h-8 font-mono text-[12px]" placeholder={`留空 = 和原版一样找 ${d.kind}`} />
            <span className="text-muted-foreground">额外参数</span>
            <Input value={d.args} onChange={(e) => set({ args: e.target.value })} onKeyDown={stop} className="h-8 font-mono text-[12px]" placeholder={d.kind === "claude" ? "比如 --settings ~/my-settings.json" : "比如 -c model=o3"} />
          </>
        )}
        <span className="self-start pt-1.5 text-muted-foreground">环境变量</span>
        <textarea
          className="h-24 w-full rounded-md border bg-card p-2 font-mono text-[12px]"
          value={d.env}
          spellCheck={false}
          onChange={(e) => set({ env: e.target.value })}
          onKeyDown={stop}
          placeholder="KEY=VALUE，一行一个"
        />
      </div>
      {d.kind === "claude" && (
        <div className="space-y-1.5 rounded-md bg-muted/60 p-2.5 text-[12px] text-muted-foreground">
          <div>
            换账号两种做法：① 用订阅账号：给一个单独的 <code className="font-mono">CLAUDE_CONFIG_DIR</code>（登录态和会话记录都存在那个目录，和原版互不相干），
            然后在终端里用这个目录登录一次；② 用 API key：写 <code className="font-mono">ANTHROPIC_API_KEY=sk-…</code>（要走代理再加 <code className="font-mono">ANTHROPIC_BASE_URL</code>）。
          </div>
          {login && <LoginHint cmd={login} then="进去之后输入 /login 登录，完了 /exit 退出（注意：设了这个变量之后哪怕指向 ~/.claude 也要单独登录一次）。" />}
        </div>
      )}
      {d.kind === "codex" && (
        <div className="space-y-1.5 rounded-md bg-muted/60 p-2.5 text-[12px] text-muted-foreground">
          <div>
            换账号：给一个单独的 <code className="font-mono">CODEX_HOME</code> 并用它登录一次，或者写 <code className="font-mono">OPENAI_API_KEY=…</code>。
          </div>
          {login && <LoginHint cmd={login} />}
        </div>
      )}
      <div className="flex justify-end gap-2">
        <Button size="sm" variant="ghost" onClick={onCancel}>
          取消
        </Button>
        <Button size="sm" onClick={onSave}>
          保存
        </Button>
      </div>
    </div>
  );
}

function LoginHint({ cmd, then }: { cmd: string; then?: string }) {
  return (
    <div className="space-y-1">
      <div>在终端里跑这一条{then ? `，${then}` : "。"}</div>
      <div className="flex items-start gap-1.5">
        <code className="min-w-0 flex-1 rounded bg-card px-2 py-1 font-mono break-all text-foreground">{cmd}</code>
        <Button
          size="icon"
          variant="ghost"
          className="size-7 shrink-0"
          title="复制"
          onClick={() => navigator.clipboard.writeText(cmd).then(() => toast.success("已复制"))}
        >
          <Copy className="size-3.5" />
        </Button>
      </div>
    </div>
  );
}
