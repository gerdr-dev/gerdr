import { Download, Pencil, Plus, Trash2 } from "lucide-react";
import { useEffect, useState } from "react";
import { toast } from "sonner";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Switch } from "@/components/ui/switch";
import { api } from "@/lib/api";
import type { ExtSettings, ExtSkill, ExtSource, ExtView, MCPServer } from "@/lib/types";

// 设置 →「Skill」「MCP」两页。gerdr 自己那份在这里增删改；agent 自己的只读，能导入。

const SOURCES: { v: ExtSource; label: string }[] = [
  { v: "agent", label: "只用 agent 自己的" },
  { v: "both", label: "agent 的 + gerdr 的" },
  { v: "gerdr", label: "只用 gerdr 的" },
];

const stop = (e: React.KeyboardEvent) => e.stopPropagation();

function useExt(open: boolean) {
  const [v, setV] = useState<ExtView | null>(null);
  useEffect(() => {
    if (open) api.ext().then(setV, (e) => toast.error(e.message));
  }, [open]);
  const run = async (p: Promise<ExtView>) => {
    try {
      setV(await p);
      return true;
    } catch (e: any) {
      toast.error(e.message);
      return false;
    }
  };
  const save = (patch: Partial<ExtSettings>) => v && run(api.putExt({ ...v.settings, ...patch }));
  return { v, run, save };
}

function SourcePicker({ value, onChange }: { value: ExtSource; onChange: (v: ExtSource) => void }) {
  return (
    <Select value={value} onValueChange={(x) => onChange(x as ExtSource)}>
      <SelectTrigger className="w-48">
        <SelectValue />
      </SelectTrigger>
      <SelectContent>
        {SOURCES.map((s) => (
          <SelectItem key={s.v} value={s.v}>
            {s.label}
          </SelectItem>
        ))}
      </SelectContent>
    </Select>
  );
}

function Section({ title, hint, children, action }: { title: string; hint?: string; children: React.ReactNode; action?: React.ReactNode }) {
  return (
    <div className="min-w-0 space-y-1.5">
      <div className="flex items-end justify-between gap-2">
        <div>
          <div className="text-[13px] font-medium">{title}</div>
          {hint && <div className="text-[12px] text-muted-foreground">{hint}</div>}
        </div>
        {action}
      </div>
      <div className="divide-y rounded-md border">{children}</div>
    </div>
  );
}

const Empty = ({ children }: { children: React.ReactNode }) => <div className="p-3 text-[12px] text-muted-foreground">{children}</div>;

// ---------------- Skill ----------------

const TEMPLATE = (name: string) => `---
name: ${name}
description: 一句话说清楚什么时候该用这个 skill（agent 靠它判断）
---

# ${name}

在这里写给 agent 的说明。
`;

export function SkillSettings({ open }: { open: boolean }) {
  const { v, run, save } = useExt(open);
  const [edit, setEdit] = useState<{ name: string; content: string; isNew: boolean } | null>(null);
  if (!v) return <Empty>加载中…</Empty>;
  const s = v.settings;
  const mine = new Set(v.skills.map((x) => x.name));
  const toggle = (name: string, on: boolean) => {
    const dis = new Set(s.disabledSkills);
    on ? dis.delete(name) : dis.add(name);
    save({ disabledSkills: [...dis] });
  };
  const openEdit = async (name: string) => {
    try {
      const r = await api.readSkill(name);
      setEdit({ name, content: r.content, isNew: false });
    } catch (e: any) {
      toast.error(e.message);
    }
  };
  const agentList = (title: string, list: ExtSkill[], hint?: string) => (
    <Section title={title} hint={hint}>
      {list.length === 0 && <Empty>没有</Empty>}
      {list.map((sk) => (
        <div key={sk.dir} className="flex items-center gap-2 px-3 py-1.5">
          <div className="min-w-0 flex-1">
            <div className="truncate text-[13px]">{sk.name}</div>
            {sk.description && <div className="truncate text-[11px] text-muted-foreground">{sk.description}</div>}
          </div>
          <Button
            size="sm"
            variant="ghost"
            className="h-7 shrink-0 text-[12px]"
            disabled={mine.has(sk.name.split(":").pop()!)}
            onClick={() => run(api.importSkill(sk.dir)).then((ok) => ok && toast.success(`已导入 ${sk.name}`))}
          >
            <Download className="size-3.5" />
            {mine.has(sk.name.split(":").pop()!) ? "已有" : "导入"}
          </Button>
        </div>
      ))}
    </Section>
  );
  return (
    <div className="min-w-0 space-y-5">
      <div className="flex items-center justify-between gap-4">
        <div>
          <div className="text-[13px]">用哪些 skill</div>
          <div className="text-[12px] text-muted-foreground">
            改完从每个会话的下一轮起生效。Gerdr Agent 没有自己的 skill 目录，「agent 自己的」对它来说就是 Claude Code 的那些（~/.claude/skills、项目的
            .claude/skills）
          </div>
        </div>
        <SourcePicker value={s.skillSource} onChange={(x) => save({ skillSource: x })} />
      </div>
      {s.skillSource === "gerdr" && (
        <div className="rounded-md bg-muted/60 p-2.5 text-[12px] text-muted-foreground">
          Claude 插件里的 skill 没法单独关，所以「只用 gerdr 的」会把 Claude 的插件整个关掉（插件带的 hook、MCP 也一起没了）。想留某个插件里的 skill，把它导入到
          gerdr。
        </div>
      )}

      <Section title="gerdr 内置" hint="不管上面选什么都会给 agent：gerdr 让它在多个会话之间协作、按你说的改这个界面">
        {v.builtin.map((sk) => (
          <div key={sk.name} className="flex items-center gap-2 px-3 py-1.5">
            <Switch checked={sk.enabled} onCheckedChange={(on) => toggle(sk.name, on)} />
            <div className="min-w-0 flex-1">
              <div className="truncate text-[13px]">{sk.name}</div>
              {sk.description && <div className="truncate text-[11px] text-muted-foreground">{sk.description}</div>}
            </div>
          </div>
        ))}
      </Section>

      {edit ? (
        <SkillEditor
          edit={edit}
          onCancel={() => setEdit(null)}
          onSave={async (name, content) => {
            if (edit.isNew && mine.has(name)) {
              toast.error(`已经有一个叫 ${name} 的 skill 了`);
              return;
            }
            if (await run(api.writeSkill(name, content))) setEdit(null);
          }}
        />
      ) : (
        <Section
          title="gerdr 的 skill"
          hint={s.skillSource === "agent" ? "现在没在用（上面选的是只用 agent 自己的）" : "每个 skill 一个目录，在 ~/.gerdr/skills 下"}
          action={
            <Button size="sm" variant="outline" className="h-7 text-[12px]" onClick={() => setEdit({ name: "", content: "", isNew: true })}>
              <Plus className="size-3.5" />
              新建
            </Button>
          }
        >
          {v.skills.length === 0 && <Empty>还没有。新建一个，或者从下面 agent 自己的里导入。</Empty>}
          {v.skills.map((sk) => (
            <div key={sk.name} className="flex items-center gap-2 px-3 py-1.5">
              <Switch checked={sk.enabled} onCheckedChange={(on) => toggle(sk.name, on)} />
              <div className="min-w-0 flex-1">
                <div className="truncate text-[13px]">{sk.name}</div>
                {sk.description && <div className="truncate text-[11px] text-muted-foreground">{sk.description}</div>}
              </div>
              <Button size="icon" variant="ghost" className="size-7" title="编辑" onClick={() => openEdit(sk.name)}>
                <Pencil className="size-3.5" />
              </Button>
              <Button
                size="icon"
                variant="ghost"
                className="size-7"
                title="删除"
                onClick={() => run(api.deleteSkill(sk.name)).then((ok) => ok && toast.success(`已删除 ${sk.name}`))}
              >
                <Trash2 className="size-3.5" />
              </Button>
            </div>
          ))}
        </Section>
      )}

      {agentList("Claude Code 自己的", v.agent.claudeSkills, "~/.claude/skills 和已启用插件里的（项目里 .claude/skills 的按会话目录另算）")}
      {agentList("Codex 自己的", v.agent.codexSkills, "~/.codex/skills、~/.agents/skills")}
    </div>
  );
}

function SkillEditor({
  edit,
  onCancel,
  onSave,
}: {
  edit: { name: string; content: string; isNew: boolean };
  onCancel: () => void;
  onSave: (name: string, content: string) => void;
}) {
  const [name, setName] = useState(edit.name);
  const [content, setContent] = useState(edit.content);
  const valid = /^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$/.test(name);
  return (
    <div className="space-y-2 rounded-md border p-3">
      <div className="flex items-center gap-2">
        <Input
          className="h-8 w-56 font-mono text-[12px]"
          placeholder="名字（字母、数字、- 和 _）"
          value={name}
          disabled={!edit.isNew}
          onChange={(e) => {
            const n = e.target.value;
            if (edit.isNew && (!content || content === TEMPLATE(name))) setContent(TEMPLATE(n));
            setName(n);
          }}
          onKeyDown={stop}
          autoFocus={edit.isNew}
        />
        <div className="flex-1 text-[12px] text-muted-foreground">SKILL.md</div>
        <Button size="sm" variant="ghost" onClick={onCancel}>
          取消
        </Button>
        <Button size="sm" disabled={!valid} onClick={() => onSave(name, content)}>
          保存
        </Button>
      </div>
      <textarea
        className="h-80 w-full resize-y rounded-md border bg-card p-2 font-mono text-[12px] leading-relaxed"
        value={content}
        placeholder={TEMPLATE("my-skill")}
        onChange={(e) => setContent(e.target.value)}
        onKeyDown={stop}
      />
    </div>
  );
}

// ---------------- MCP ----------------

const blankServer = (): MCPServer => ({ name: "", type: "stdio", command: "", args: [], env: {}, url: "", headers: {}, enabled: true });
const isRemote = (m: MCPServer) => m.type === "http" || m.type === "sse" || (!m.command && !!m.url);
const kv = (o?: Record<string, string>, sep = "=") =>
  Object.entries(o ?? {})
    .map(([k, v]) => `${k}${sep}${v}`)
    .join("\n");
const parseKV = (s: string, sep: string) =>
  Object.fromEntries(
    s
      .split("\n")
      .map((l) => l.trim())
      .filter(Boolean)
      .map((l) => {
        const i = l.indexOf(sep);
        return i < 0 ? [l, ""] : [l.slice(0, i).trim(), l.slice(i + sep.length).trim()];
      }),
  );

export function MCPSettings({ open }: { open: boolean }) {
  const { v, save } = useExt(open);
  const [edit, setEdit] = useState<{ index: number; server: MCPServer } | null>(null);
  if (!v) return <Empty>加载中…</Empty>;
  const s = v.settings;
  const names = new Set(s.mcp.map((m) => m.name));
  const put = (list: MCPServer[]) => save({ mcp: list });
  const describe = (m: MCPServer) => (isRemote(m) ? m.url : [m.command, ...(m.args ?? [])].join(" "));
  const agentList = (title: string, list: MCPServer[], hint?: string) => (
    <Section title={title} hint={hint}>
      {list.length === 0 && <Empty>没有</Empty>}
      {list.map((m) => (
        <div key={m.name} className="flex items-center gap-2 px-3 py-1.5">
          <div className="min-w-0 flex-1">
            <div className="truncate text-[13px]">
              {m.name}
              {!m.enabled && <span className="ml-1.5 text-[11px] text-muted-foreground">（已关）</span>}
            </div>
            <div className="truncate font-mono text-[11px] text-muted-foreground">{describe(m)}</div>
          </div>
          <Button
            size="sm"
            variant="ghost"
            className="h-7 shrink-0 text-[12px]"
            disabled={names.has(m.name)}
            onClick={() =>
              put([...s.mcp, { ...m, type: isRemote(m) ? m.type || "http" : "stdio", enabled: true }])?.then((ok) => ok && toast.success(`已导入 ${m.name}`))
            }
          >
            <Download className="size-3.5" />
            {names.has(m.name) ? "已有" : "导入"}
          </Button>
        </div>
      ))}
    </Section>
  );
  return (
    <div className="min-w-0 space-y-5">
      <div className="flex items-center justify-between gap-4">
        <div>
          <div className="text-[13px]">用哪些 MCP</div>
          <div className="text-[12px] text-muted-foreground">
            改完从每个会话的下一轮起生效。Gerdr Agent 没有自己的 MCP 配置，「agent 自己的」对它来说就是 Claude Code 配的那些（~/.claude.json、项目的
            .mcp.json）；它不支持要 OAuth 登录的服务器
          </div>
        </div>
        <SourcePicker value={s.mcpSource} onChange={(x) => save({ mcpSource: x })} />
      </div>
      {s.mcpSource === "gerdr" && (
        <div className="rounded-md bg-muted/60 p-2.5 text-[12px] text-muted-foreground">
          只用 gerdr 的：Claude 的 claude.ai 账号连接器、Codex 插件自带的 MCP 不受这里控制。Codex 不支持 SSE，SSE 的只给 Claude。
        </div>
      )}

      {edit ? (
        <MCPEditor
          server={edit.server}
          taken={(n) => names.has(n) && s.mcp[edit.index]?.name !== n}
          onCancel={() => setEdit(null)}
          onSave={async (m) => {
            const list = [...s.mcp];
            if (edit.index < 0) list.push(m);
            else list[edit.index] = m;
            if (await put(list)) setEdit(null);
          }}
        />
      ) : (
        <Section
          title="gerdr 的 MCP"
          hint={s.mcpSource === "agent" ? "现在没在用（上面选的是只用 agent 自己的）" : "存在 ~/.gerdr/ext.json"}
          action={
            <Button size="sm" variant="outline" className="h-7 text-[12px]" onClick={() => setEdit({ index: -1, server: blankServer() })}>
              <Plus className="size-3.5" />
              添加
            </Button>
          }
        >
          {s.mcp.length === 0 && <Empty>还没有。添加一个，或者从下面 agent 自己的里导入。</Empty>}
          {s.mcp.map((m, i) => (
            <div key={m.name} className="flex items-center gap-2 px-3 py-1.5">
              <Switch checked={m.enabled} onCheckedChange={(on) => put(s.mcp.map((x, j) => (j === i ? { ...x, enabled: on } : x)))} />
              <div className="min-w-0 flex-1">
                <div className="truncate text-[13px]">
                  {m.name}
                  <span className="ml-1.5 text-[11px] text-muted-foreground">{isRemote(m) ? m.type || "http" : "stdio"}</span>
                </div>
                <div className="truncate font-mono text-[11px] text-muted-foreground">{describe(m)}</div>
              </div>
              <Button size="icon" variant="ghost" className="size-7" title="编辑" onClick={() => setEdit({ index: i, server: m })}>
                <Pencil className="size-3.5" />
              </Button>
              <Button size="icon" variant="ghost" className="size-7" title="删除" onClick={() => put(s.mcp.filter((_, j) => j !== i))}>
                <Trash2 className="size-3.5" />
              </Button>
            </div>
          ))}
        </Section>
      )}

      {agentList("Claude Code 自己的", v.agent.claudeMCP, "~/.claude.json 里用户级的（项目级的按会话目录另算）")}
      {agentList("Codex 自己的", v.agent.codexMCP, "~/.codex/config.toml")}
    </div>
  );
}

function MCPEditor({
  server,
  taken,
  onCancel,
  onSave,
}: {
  server: MCPServer;
  taken: (n: string) => boolean;
  onCancel: () => void;
  onSave: (m: MCPServer) => void;
}) {
  const [m, setM] = useState<MCPServer>({ ...server, type: isRemote(server) ? server.type || "http" : "stdio" });
  const [args, setArgs] = useState((server.args ?? []).join("\n"));
  const [env, setEnv] = useState(kv(server.env));
  const [headers, setHeaders] = useState(kv(server.headers, ": "));
  const remote = m.type !== "stdio";
  const nameOk = /^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$/.test(m.name) && !taken(m.name);
  const ok = nameOk && (remote ? !!m.url : !!m.command);
  const field = "h-8 font-mono text-[12px]";
  const area = "h-20 w-full resize-y rounded-md border bg-card p-2 font-mono text-[12px]";
  return (
    <div className="space-y-2.5 rounded-md border p-3">
      <div className="flex items-center gap-2">
        <Input
          className={`${field} w-48`}
          placeholder="名字"
          value={m.name}
          onChange={(e) => setM({ ...m, name: e.target.value })}
          onKeyDown={stop}
          autoFocus
        />
        <Select value={m.type || "stdio"} onValueChange={(t) => setM({ ...m, type: t as any })}>
          <SelectTrigger className="h-8 w-32">
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value="stdio">stdio</SelectItem>
            <SelectItem value="http">http</SelectItem>
            <SelectItem value="sse">sse</SelectItem>
          </SelectContent>
        </Select>
        <div className="flex-1" />
        <Button size="sm" variant="ghost" onClick={onCancel}>
          取消
        </Button>
        <Button
          size="sm"
          disabled={!ok}
          onClick={() =>
            onSave(
              remote
                ? { name: m.name, type: m.type, url: m.url, headers: parseKV(headers, ":"), enabled: m.enabled }
                : {
                    name: m.name,
                    type: "stdio",
                    command: m.command,
                    args: args
                      .split("\n")
                      .map((x) => x.trim())
                      .filter(Boolean),
                    env: parseKV(env, "="),
                    enabled: m.enabled,
                  },
            )
          }
        >
          保存
        </Button>
      </div>
      {m.name && taken(m.name) && <div className="text-[12px] text-bad">这个名字已经有了</div>}
      {remote ? (
        <>
          <Input
            className={field}
            placeholder="https://example.com/mcp"
            value={m.url ?? ""}
            onChange={(e) => setM({ ...m, url: e.target.value })}
            onKeyDown={stop}
          />
          <div className="text-[12px] text-muted-foreground">请求头，一行一个（Authorization: Bearer xxx）</div>
          <textarea className={area} value={headers} onChange={(e) => setHeaders(e.target.value)} onKeyDown={stop} />
        </>
      ) : (
        <>
          <Input
            className={field}
            placeholder="命令，比如 npx 或 /usr/local/bin/my-mcp"
            value={m.command ?? ""}
            onChange={(e) => setM({ ...m, command: e.target.value })}
            onKeyDown={stop}
          />
          <div className="text-[12px] text-muted-foreground">参数，一行一个</div>
          <textarea className={area} value={args} onChange={(e) => setArgs(e.target.value)} onKeyDown={stop} />
          <div className="text-[12px] text-muted-foreground">环境变量，一行一个（KEY=VALUE）</div>
          <textarea className={area} value={env} onChange={(e) => setEnv(e.target.value)} onKeyDown={stop} />
        </>
      )}
    </div>
  );
}
