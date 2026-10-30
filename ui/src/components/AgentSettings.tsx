import { Copy, Pencil, Plus } from "lucide-react";
import { useEffect, useState } from "react";
import { toast } from "sonner";
import { AgentIcon } from "@/components/AgentIcon";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import { Dialog, DialogContent, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { ConfirmDelete } from "@/components/ConfirmDelete";
import { Input } from "@/components/ui/input";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { LLMSettings } from "@/components/LLMSettings";
import { api } from "@/lib/api";
import { MODE, defaultsOf, flatOptions, setDefaults, withoutDefaults } from "@/lib/defaults";
import { get, setConfig, useStore, type AgentDef, type LLMProvider } from "@/lib/store";
import type { AgentInfo, ExtView } from "@/lib/types";
import { cn } from "@/lib/utils";

// 设置 →「Agent」。两个概念（用户定的）：
//   - 引擎：真正干活的那个 —— Claude Code、Codex、Native（gerdr 自己直接调模型接口，用下面配的模型服务商，可以有好几家）；
//   - agent：新建会话时选的那个 = 一个引擎 + 自己的提示词、skill / MCP、新会话默认（模式 / 模型 / 思考强度）。
//     内置三个（Claude Code / Codex / Gerdr Agent）各用一个引擎、什么都不限；可以再建，比如两个都用 Native、各走一家服务商
//     （providers），或者「代码审查」只读、只开 GitHub 的 MCP。换账号（CLAUDE_CONFIG_DIR / CODEX_HOME）是「高级」里那几项；
//     外部 ACP agent（gemini --experimental-acp 这种）在「引擎」里选。
// 入口只有一个「新建」，新建和编辑都在弹窗里（用户要的：别一排按钮）。
// 存进 config.json 的 agents（后端 hub.AgentDef，热换：改了的那个正在跑的进程收掉，会话下一轮按新配置接上）；
// 新会话默认存 defaultModes / defaultConfig（lib/defaults.ts），内置的三个也能设。

const stop = (e: React.KeyboardEvent) => e.stopPropagation();
const NONE: AgentDef[] = []; // 选择器里别 `?? []`：每次一个新数组，zustand 认成变了，无限重渲染
const UNSET = "__unset"; // Select 的值不能是空串：「不指定」用它
const NO_PROVIDERS: LLMProvider[] = [];

type Kind = "claude" | "codex" | "native" | "command";
type Draft = {
  id: string;
  name: string;
  kind: Kind;
  builtin?: boolean; // 内置的三个：只能改新会话默认
  env: string;
  bin: string;
  args: string;
  command: string;
  prompt: string;
  skills: string[] | null; // null = 照全局设置
  mcp: string[] | null;
  defaults: Record<string, string>; // MODE / 配置项 id → 值
  providers: string[]; // 引擎是 native 时只用这几家服务商；空 = 全部
};

const KIND_NAME: Record<Kind, string> = { claude: "Claude Code", codex: "Codex", native: "Native", command: "外部 ACP" };

const toDraft = (d: AgentDef): Draft => ({
  id: d.id,
  name: d.name,
  kind: d.base ?? "command",
  env: Object.entries(d.env ?? {})
    .map(([k, v]) => `${k}=${v}`)
    .join("\n"),
  bin: d.bin ?? "",
  args: (d.args ?? []).join(" "),
  command: (d.command ?? []).join(" "),
  prompt: d.prompt ?? "",
  skills: d.skills ?? null,
  mcp: d.mcp ?? null,
  defaults: defaultsOf(d.id),
  // 以前存的「不写 = 全部」：打开编辑时摊成全部勾上（现在至少要勾一个，后端仍把空当全部，兼容老配置）
  providers: d.providers?.length ? d.providers : d.base === "native" ? allProviders() : [],
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
    if (!d.command.trim()) throw new Error("外部 Agent 要填启动命令");
    return { id: d.id, name: d.name.trim(), command: words(d.command), env: Object.keys(env).length ? env : undefined };
  }
  const native = d.kind === "native"; // Native 引擎没有可执行文件，启动参数 / 环境变量用不上
  if (native && !d.providers.length) throw new Error("至少勾一个服务商");
  return {
    id: d.id,
    name: d.name.trim(),
    base: d.kind,
    env: !native && Object.keys(env).length ? env : undefined,
    bin: (!native && d.bin.trim()) || undefined,
    args: !native && words(d.args).length ? words(d.args) : undefined,
    prompt: d.prompt.trim() || undefined,
    skills: d.skills ?? undefined,
    mcp: d.mcp ?? undefined,
    providers: native ? d.providers : undefined,
  };
}

const allProviders = () => (get().config.llm?.providers ?? []).map((p) => p.id);

function newId(name: string, taken: Set<string>) {
  const base =
    name
      .toLowerCase()
      .replace(/[^a-z0-9]+/g, "-")
      .replace(/^-|-$/g, "") || "agent";
  let id = base;
  for (let n = 2; taken.has(id); n++) id = `${base}-${n}`;
  return id;
}

/** 编辑默认值时的下拉框：这种 agent 上次报的模式 / 配置项，新角色还没开过会话就用底子的（后端已经替它退过一次）。 */
function optionsOf(agents: AgentInfo[], id: string, base: string) {
  return agents.find((a) => a.id === id)?.options ?? agents.find((a) => a.id === base)?.options;
}

/** 一行摘要：「默认：计划模式 · Opus 5.5」。 */
function defaultsSummary(agents: AgentInfo[], id: string, base: string, d: Record<string, string>) {
  const o = optionsOf(agents, id, base);
  const parts: string[] = [];
  if (d[MODE]) parts.push(o?.modes?.availableModes.find((m) => m.id === d[MODE])?.name ?? d[MODE]);
  for (const c of o?.config ?? []) if (d[c.id]) parts.push(flatOptions(c).find((x) => x.value === d[c.id])?.name ?? d[c.id]);
  for (const [k, v] of Object.entries(d)) if (k !== MODE && !o?.config?.some((c) => c.id === k)) parts.push(v);
  return parts.join(" · ");
}

export function AgentSettings() {
  const agents = useStore((s) => s.agents);
  const defs = useStore((s) => s.config.agents ?? NONE);
  const providers = useStore((s) => s.config.llm?.providers) ?? NO_PROVIDERS;
  // 订阅默认值：在输入框下拉里「设为新会话的默认」之后，这一页的摘要跟着变
  useStore((s) => s.config.defaultModes);
  useStore((s) => s.config.defaultConfig);
  useStore((s) => s.config.llm);
  const [edit, setEdit] = useState<Draft | null>(null);
  const [isNew, setIsNew] = useState(false);

  const taken = new Set(["claude", "codex", "native", ...defs.map((d) => d.id)]);
  const blank = (kind: Kind, name: string, env = ""): Draft => ({
    id: newId(name, taken),
    name,
    kind,
    env,
    bin: "",
    args: "",
    command: kind === "command" ? "gemini --experimental-acp" : "",
    prompt: "",
    skills: null,
    mcp: null,
    defaults: {},
    providers: [],
  });
  // 打开时的样子：改过了（和它不一样）就不许点弹窗外面关掉，免得误点把填了半天的丢了（用户要的）；关闭按钮、取消、Esc 照常
  const [orig, setOrig] = useState("");
  const open = (d: Draft, fresh: boolean) => {
    setIsNew(fresh);
    setEdit(d);
    setOrig(JSON.stringify(d));
  };
  const dirty = !!edit && JSON.stringify(edit) !== orig;
  const save = () => {
    if (!edit) return;
    try {
      // 默认值整份写：原来有、现在清掉的也要写成空，setDefaults 才会删
      const before = edit.builtin || !isNew ? defaultsOf(edit.id) : {};
      const values: Record<string, string> = {};
      for (const k of new Set([...Object.keys(before), ...Object.keys(edit.defaults)])) values[k] = edit.defaults[k] ?? "";
      if (edit.builtin) {
        setDefaults(edit.id, values);
        toast.success("已保存：之后新开的会话按这个来");
      } else {
        const d = fromDraft(edit);
        setDefaults(edit.id, values, { agents: isNew ? [...defs, d] : defs.map((x) => (x.id === d.id ? d : x)) });
        toast.success(isNew ? `加好了：新建会话时选「${d.name}」` : "已保存（正在跑的会话下一轮按新配置接上）");
      }
      setEdit(null);
    } catch (e: any) {
      toast.error(e.message);
    }
  };

  return (
    <div className="space-y-4 text-[13px]">
      <div>
        <div className="font-medium">Agent</div>
        <div className="text-[12px] text-muted-foreground">
          新建会话时选的就是它们：一个引擎，加上自己的提示词、Skill / MCP、新会话默认。同一个引擎可以建好几个。
        </div>
      </div>
      <div className="space-y-1.5">
        {agents.map((a) => {
          const def = defs.find((d) => d.id === a.id);
          const base = def?.base ?? a.id;
          const summary = defaultsSummary(agents, a.id, base, defaultsOf(a.id));
          const chips = def && def.base ? roleChips(def, providers) : [];
          return (
            <div key={a.id} className="rounded-md border px-3 py-2">
              {/* 第一行只放短的（名字、引擎、可用没有），都不换行；不可用的原因放到下面一行 —— 原来原因塞在这一行里，
                  一长就把名字和引擎挤成两三行（用户报的） */}
              <div className="flex items-center gap-2 whitespace-nowrap">
                <AgentIcon agent={a.id} className="size-4" />
                <span className="min-w-0 truncate font-medium">{a.name}</span>
                <span className="shrink-0 text-[12px] text-muted-foreground">
                  引擎 {KIND_NAME[(def ? (def.base ?? "command") : a.id) as Kind]}
                  {!def && " · 内置"}
                </span>
                {a.available ? <span className="shrink-0 text-[12px] text-ok">可用</span> : <span className="shrink-0 text-[12px] text-bad">不可用</span>}
                <span className="flex-1" />
                <Button
                  size="icon"
                  variant="ghost"
                  className="size-7"
                  title={def ? "编辑" : "新会话默认"}
                  onClick={() => open(def ? toDraft(def) : { ...blank(a.id as Kind, a.name), id: a.id, builtin: true, defaults: defaultsOf(a.id) }, false)}
                >
                  <Pencil className="size-3.5" />
                </Button>
                {def && (
                  <ConfirmDelete
                    title="删除（已有的会话留着，只是没法再接着聊）"
                    onConfirm={() => setConfig({ agents: defs.filter((x) => x.id !== def.id), ...withoutDefaults(def.id) })}
                  />
                )}
              </div>
              {!a.available && a.error && <div className="mt-1 pl-6 text-[12px] text-bad">{a.error}</div>}
              {(summary || chips.length > 0) && (
                <div className="mt-1 flex flex-wrap gap-x-3 gap-y-0.5 pl-6 text-[12px] text-muted-foreground">
                  {chips.map((c) => (
                    <span key={c}>{c}</span>
                  ))}
                  {summary && <span>新会话默认：{summary}</span>}
                </div>
              )}
            </div>
          );
        })}
      </div>

      {/* 只一个「新建」：换账号、外部 ACP agent 都在弹窗里选「引擎」/ 展开「高级」 */}
      <Button size="sm" variant="outline" onClick={() => open(blank("claude", "新角色"), true)}>
        <Plus className="size-3.5" /> 新建
      </Button>

      {/* 新建和编辑都在弹窗里（叠在设置弹窗上面）：原来展开在列表下面，列表一长就要往下找 */}
      <Dialog open={!!edit} onOpenChange={(o) => !o && setEdit(null)}>
        {/* 宽度写成内联样式：界面的 Tailwind 是浏览器里现编的，页面上头一回出现的 class（sm:max-w-2xl）样式要晚一拍才生成，
            第一次打开时先按没限宽铺满全屏、再缩回来（用户报的），第二次才正常 */}
        <DialogContent
          className="max-h-[85vh] overflow-auto text-[13px]"
          style={{ maxWidth: "min(42rem, calc(100% - 2rem))" }}
          onInteractOutside={(e) => dirty && e.preventDefault()}
        >
          <DialogHeader>
            <DialogTitle>{!edit ? "" : edit.builtin ? `${edit.name} · 新会话默认` : isNew ? "新建 Agent" : `编辑「${edit.name}」`}</DialogTitle>
          </DialogHeader>
          {edit && <Editor d={edit} onChange={setEdit} onSave={save} onCancel={() => setEdit(null)} />}
        </DialogContent>
      </Dialog>

      <Engines agents={agents} />
    </div>
  );
}

function roleChips(d: AgentDef, providers: LLMProvider[]) {
  const out: string[] = [];
  if (d.prompt) out.push("有提示词");
  if (d.skills) out.push(d.skills.length ? `Skill ${d.skills.length} 个` : "不用 Skill");
  if (d.mcp) out.push(d.mcp.length ? `MCP ${d.mcp.length} 个` : "不用 MCP");
  if (d.providers?.length) out.push(`服务商 ${d.providers.map((id) => providers.find((p) => p.id === id)?.name || `${id}（已删）`).join("、")}`);
  if (d.env && Object.keys(d.env).length) out.push("自己的环境变量");
  return out;
}

/** 「引擎」那块：Claude Code / Codex 是本机装的 CLI（账号在它们自己那边登录），Native 用这里配的模型服务商。 */
function Engines({ agents }: { agents: AgentInfo[] }) {
  const cli = (id: string, name: string, how: string) => {
    const a = agents.find((x) => x.id === id);
    return (
      <div className="flex items-center gap-2 rounded-md border px-3 py-2">
        <AgentIcon agent={id} className="size-4" />
        <span className="font-medium">{name}</span>
        <span className="text-[12px] text-muted-foreground">{how}</span>
        <span className="flex-1" />
        {a?.available ? <span className="text-[12px] text-ok">装了</span> : <span className="truncate text-[12px] text-bad">{a?.error || "没装"}</span>}
      </div>
    );
  };
  return (
    <div className="space-y-3 border-t pt-4">
      <div>
        <div className="font-medium">引擎</div>
        <div className="text-[12px] text-muted-foreground">真正干活的那个。Agent 选哪个引擎，会话就跑在哪个上面。</div>
      </div>
      <div className="space-y-1.5">
        {cli("claude", "Claude Code", "本机的 claude 命令，账号在它那边登录")}
        {cli("codex", "Codex", "本机的 codex 命令，账号在它那边登录")}
      </div>
      <LLMSettings />
    </div>
  );
}

function Editor({ d, onChange, onSave, onCancel }: { d: Draft; onChange: (d: Draft) => void; onSave: () => void; onCancel: () => void }) {
  const set = (p: Partial<Draft>) => onChange({ ...d, ...p });
  const providers = useStore((s) => s.config.llm?.providers) ?? NO_PROVIDERS;
  const dir = /^CLAUDE_CONFIG_DIR=(.+)$/m.exec(d.env)?.[1]?.trim();
  const codexHome = /^CODEX_HOME=(.+)$/m.exec(d.env)?.[1]?.trim();
  const login = d.kind === "claude" && dir ? `CLAUDE_CONFIG_DIR=${dir} claude` : d.kind === "codex" && codexHome ? `CODEX_HOME=${codexHome} codex login` : "";
  const role = d.kind !== "command" && !d.builtin;
  // 「高级」里填过东西（另一个账号）就展开着，不然收起来：大多数角色用不到
  const [adv, setAdv] = useState(!!(d.env || d.bin || d.args) || d.kind === "command");
  return (
    <div className="space-y-3">
      <div className="grid grid-cols-[88px_1fr] items-center gap-x-3 gap-y-2.5">
        <span className="text-muted-foreground">名字</span>
        {d.builtin ? (
          <span className="font-medium">{d.name}</span>
        ) : (
          <Input value={d.name} onChange={(e) => set({ name: e.target.value })} onKeyDown={stop} className="h-8" />
        )}
        {!d.builtin && (
          <>
            <span className="text-muted-foreground">引擎</span>
            <Select
              value={d.kind}
              // 换到 Native 时先把服务商全勾上（至少要勾一个，省得人再去点）
              onValueChange={(v) => set({ kind: v as Kind, ...(v === "native" && !d.providers.length ? { providers: allProviders() } : {}) })}
            >
              <SelectTrigger size="sm" className="w-56">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="claude">Claude Code</SelectItem>
                <SelectItem value="codex">Codex</SelectItem>
                {(providers.length > 0 || d.kind === "native") && <SelectItem value="native">Native</SelectItem>}
                <SelectItem value="command">外部 ACP Agent</SelectItem>
              </SelectContent>
            </Select>
          </>
        )}
        {d.kind === "native" && !d.builtin && (
          <>
            <span className="self-start pt-1 text-muted-foreground">服务商</span>
            <div className="space-y-1">
              <div className="flex flex-wrap gap-x-4 gap-y-1">
                {[
                  ...providers.map((p) => ({ id: p.id, name: p.name || p.id })),
                  ...d.providers.filter((id) => !providers.some((p) => p.id === id)).map((id) => ({ id, name: `${id}（已删）` })),
                ].map((p) => (
                  <label key={p.id} className="flex cursor-pointer items-center gap-1.5">
                    <Checkbox
                      checked={d.providers.includes(p.id)}
                      onCheckedChange={(on) => {
                        const next = on ? [...d.providers, p.id] : d.providers.filter((x) => x !== p.id);
                        // 默认模型不在新的服务商里了就清掉，免得存一个用不上的
                        const m = d.defaults.model;
                        const defaults = m && next.length && !next.some((x) => m.startsWith(x + "/")) ? (({ model: _, ...r }) => r)(d.defaults) : d.defaults;
                        set({ providers: next, defaults });
                      }}
                    />
                    {p.name}
                  </label>
                ))}
              </div>
              <div className="text-[11px] text-muted-foreground">只用勾上的这几家的模型，至少勾一个。服务商在下面「引擎 → Native」里配。</div>
            </div>
          </>
        )}
        {role && (
          <>
            <span className="self-start pt-1.5 text-muted-foreground">提示词</span>
            <div className="space-y-1">
              <textarea
                className="min-h-24 w-full rounded-md border bg-card p-2 text-[12px]"
                value={d.prompt}
                onChange={(e) => set({ prompt: e.target.value })}
                onKeyDown={stop}
                placeholder="这个角色是干什么的、要守什么规矩。比如：你是代码审查员，只读代码、不改文件，按严重程度列出问题。"
              />
              <div className="text-[11px] text-muted-foreground">追加在 Agent 自己的系统提示后面（CLAUDE.md / AGENTS.md 照样读）。</div>
            </div>
          </>
        )}
      </div>

      {role && <ExtPicker kind={d.kind as "claude" | "codex" | "native"} skills={d.skills} mcp={d.mcp} onChange={(p) => set(p)} />}

      <DefaultsEditor
        id={d.id}
        base={d.builtin ? d.id : d.kind}
        providers={d.kind === "native" ? d.providers : undefined}
        value={d.defaults}
        onChange={(v) => set({ defaults: v })}
      />

      {!d.builtin && d.kind !== "native" && (
        <div className="space-y-2.5">
          {d.kind !== "command" && (
            <button className="text-[12px] text-muted-foreground hover:text-foreground" onClick={() => setAdv(!adv)}>
              {adv ? "▾" : "▸"} 高级：换账号 / 可执行文件 / 启动参数
            </button>
          )}
          {(adv || d.kind === "command") && (
            <div className="grid grid-cols-[88px_1fr] items-center gap-x-3 gap-y-2.5">
              {d.kind === "command" ? (
                <>
                  <span className="text-muted-foreground">启动命令</span>
                  <Input
                    value={d.command}
                    onChange={(e) => set({ command: e.target.value })}
                    onKeyDown={stop}
                    className="h-8 font-mono text-[12px]"
                    placeholder="gemini --experimental-acp"
                  />
                </>
              ) : (
                <>
                  <span className="text-muted-foreground">可执行文件</span>
                  <Input
                    value={d.bin}
                    onChange={(e) => set({ bin: e.target.value })}
                    onKeyDown={stop}
                    className="h-8 font-mono text-[12px]"
                    placeholder={`留空 = 和原版一样找 ${d.kind}`}
                  />
                  <span className="text-muted-foreground">额外参数</span>
                  <Input
                    value={d.args}
                    onChange={(e) => set({ args: e.target.value })}
                    onKeyDown={stop}
                    className="h-8 font-mono text-[12px]"
                    placeholder={d.kind === "claude" ? "比如 --settings ~/my-settings.json" : "比如 -c model=o3"}
                  />
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
          )}
          {adv && d.kind === "claude" && (
            <div className="space-y-1.5 rounded-md bg-muted/60 p-2.5 text-[12px] text-muted-foreground">
              <div>
                换账号两种做法：① 用订阅账号：给一个单独的 <code className="font-mono">CLAUDE_CONFIG_DIR</code>
                （登录态和会话记录都存在那个目录，和原版互不相干）， 然后在终端里用这个目录登录一次；② 用 API key：写{" "}
                <code className="font-mono">ANTHROPIC_API_KEY=sk-…</code>（要走代理再加 <code className="font-mono">ANTHROPIC_BASE_URL</code>
                ）。不换账号就留空，和原版共用登录。
              </div>
              {login && (
                <LoginHint cmd={login} then="进去之后输入 /login 登录，完了 /exit 退出（注意：设了这个变量之后哪怕指向 ~/.claude 也要单独登录一次）。" />
              )}
            </div>
          )}
          {adv && d.kind === "codex" && (
            <div className="space-y-1.5 rounded-md bg-muted/60 p-2.5 text-[12px] text-muted-foreground">
              <div>
                换账号：给一个单独的 <code className="font-mono">CODEX_HOME</code> 并用它登录一次，或者写 <code className="font-mono">OPENAI_API_KEY=…</code>
                。不换账号就留空。
              </div>
              {login && <LoginHint cmd={login} />}
            </div>
          )}
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

type Cand = { name: string; from: string; description?: string };

/** 这种 agent 在全局设置下本来会拿到的 skill / MCP（角色只能在这里面挑）。agent 自己那份：Gerdr Agent 用的是 Claude Code 的。 */
function candidates(v: ExtView, kind: "claude" | "codex" | "native") {
  const s = v.settings;
  const own = kind === "codex" ? "Codex" : "Claude Code";
  const skills: Cand[] = [];
  const mcp: Cand[] = [];
  const add = (list: Cand[], c: Cand) => !list.some((x) => x.name === c.name) && list.push(c);
  for (const sk of v.builtin) if (sk.enabled) add(skills, { name: sk.name, from: "内置", description: sk.description });
  if (s.skillSource !== "agent") for (const sk of v.skills) if (sk.enabled) add(skills, { name: sk.name, from: "Gerdr", description: sk.description });
  if (s.skillSource !== "gerdr")
    for (const sk of kind === "codex" ? v.agent.codexSkills : v.agent.claudeSkills) add(skills, { name: sk.name, from: own, description: sk.description });
  // Native 引擎只用 Gerdr 那份 MCP（不看来源、不借 Claude Code 的）；另外两个按来源
  if (kind === "native" || s.mcpSource !== "agent") for (const m of s.mcp) if (m.enabled) add(mcp, { name: m.name, from: "Gerdr" });
  if (kind !== "native" && s.mcpSource !== "gerdr")
    for (const m of kind === "codex" ? v.agent.codexMCP : v.agent.claudeMCP) if (m.enabled) add(mcp, { name: m.name, from: own });
  return { skills, mcp };
}

function ExtPicker({
  kind,
  skills,
  mcp,
  onChange,
}: {
  kind: "claude" | "codex" | "native";
  skills: string[] | null;
  mcp: string[] | null;
  onChange: (p: { skills?: string[] | null; mcp?: string[] | null }) => void;
}) {
  const [view, setView] = useState<ExtView | null>(null);
  useEffect(() => {
    api
      .ext()
      .then(setView)
      .catch((e) => toast.error(`读 Skill / MCP 列表失败：${e.message}`));
  }, []);
  if (!view) return <div className="text-[12px] text-muted-foreground">读 Skill / MCP 列表…</div>;
  const c = candidates(view, kind);
  return (
    <div className="grid gap-3 md:grid-cols-2">
      <Pick
        label="Skill"
        items={c.skills}
        value={skills}
        onChange={(v) => onChange({ skills: v })}
        note={
          kind === "claude"
            ? "项目里的 skill 这里列不出来：限定之后它们也不给。插件里的 skill 关不了单个，一个都没勾的插件整个关掉（连它的 MCP、hook）。"
            : kind === "codex"
              ? "Codex 自带的系统 skill 不受影响。"
              : undefined
        }
      />
      <Pick
        label="MCP"
        items={c.mcp}
        value={mcp}
        onChange={(v) => onChange({ mcp: v })}
        note={kind === "claude" ? "限定之后只给勾上的：插件带的 MCP、claude.ai 账号里连的 connector 都不给。" : undefined}
        allHint={kind === "native" ? `用「设置 → Agent → MCP」里 Gerdr 的 MCP 开着的那些，${c.mcp.length} 个。` : undefined}
      />
    </div>
  );
}

/** 一组勾选：「全部（照全局设置）」或「只给勾上的」。 */
function Pick({
  label,
  items,
  value,
  onChange,
  note,
  allHint,
}: {
  label: string;
  items: Cand[];
  value: string[] | null;
  onChange: (v: string[] | null) => void;
  note?: string;
  allHint?: string; // 「全部」时的说明，不给就是「照设置里的来源」
}) {
  const limited = value !== null;
  // 存着的名字现在列表里没有了（skill 删了、MCP 改名了）：也列出来，不然勾不掉
  const gone = (value ?? []).filter((n) => !items.some((i) => i.name === n)).map((n): Cand => ({ name: n, from: "已不在" }));
  return (
    <div className="space-y-1.5 rounded-md border bg-card p-2.5">
      <div className="flex items-center gap-2">
        <span className="font-medium">{label}</span>
        <span className="flex-1" />
        <div className="flex rounded-md border text-[12px]">
          {[
            ["全部", false],
            ["只用勾选的", true],
          ].map(([t, on]) => (
            <button
              key={t as string}
              className={cn(
                "px-2 py-0.5 first:rounded-l-md last:rounded-r-md",
                limited === on ? "bg-muted text-foreground" : "text-muted-foreground hover:text-foreground",
              )}
              onClick={() => onChange(on ? (value ?? items.map((i) => i.name)) : null)}
            >
              {t as string}
            </button>
          ))}
        </div>
      </div>
      {!limited ? (
        <div className="text-[12px] text-muted-foreground">{allHint ?? `照「设置 → ${label}」里的来源，${items.length} 个。`}</div>
      ) : (
        <div className="max-h-52 space-y-0.5 overflow-auto">
          {items.length + gone.length === 0 && <div className="text-[12px] text-muted-foreground">没有可选的</div>}
          {[...items, ...gone].map((i) => (
            <label key={i.name} className="flex cursor-pointer items-start gap-2 rounded-sm px-1 py-0.5 hover:bg-accent" title={i.description}>
              <Checkbox
                className="mt-0.5"
                checked={value!.includes(i.name)}
                onCheckedChange={(on) => onChange(on ? [...value!, i.name] : value!.filter((n) => n !== i.name))}
              />
              <span className="min-w-0 flex-1 truncate text-[12px]">{i.name}</span>
              <span className="shrink-0 text-[11px] text-muted-foreground">{i.from}</span>
            </label>
          ))}
        </div>
      )}
      {limited && note && <div className="text-[11px] leading-snug text-muted-foreground">{note}</div>}
    </div>
  );
}

/** 新会话默认：模式 + agent 报过的配置项（模型、思考强度…）。选项来自上次开会话时 agent 报的那份。 */
function DefaultsEditor({
  id,
  base,
  providers,
  value,
  onChange,
}: {
  id: string;
  base: string;
  providers?: string[]; // Native 引擎的 agent：模型只列这几家的（空 = 全部）
  value: Record<string, string>;
  onChange: (v: Record<string, string>) => void;
}) {
  const agents = useStore((s) => s.agents);
  const llm = useStore((s) => s.config.llm);
  const o = optionsOf(agents, id, base);
  const rows: { key: string; label: string; options: { value: string; name: string }[] }[] = [];
  if (o?.modes?.availableModes.length) rows.push({ key: MODE, label: "模式", options: o.modes.availableModes.map((m) => ({ value: m.id, name: m.name })) });
  for (const c of o?.config ?? [])
    rows.push({ key: c.id, label: c.id === "model" ? "模型" : c.id === "effort" ? "思考强度" : c.name, options: flatOptions(c) });
  if (base === "native") {
    // Native 的模型直接按配置里的服务商列（不靠上次报的那份：那份是全部服务商的，换了勾选也不跟着变）
    const ps = (llm?.providers ?? []).filter((p) => !providers?.length || providers.includes(p.id));
    const models = ps.flatMap((p) =>
      p.models.map((m) => ({ value: `${p.id}/${m.id}`, name: `${m.name || m.id}${ps.length > 1 ? ` · ${p.name || p.id}` : ""}` })),
    );
    const row = { key: "model", label: "模型", options: models };
    const i = rows.findIndex((r) => r.key === "model");
    if (i >= 0) rows[i] = row;
    else if (models.length) rows.push(row);
  }
  const put = (k: string, v: string) => {
    const next = { ...value };
    if (v === UNSET) delete next[k];
    else next[k] = v;
    onChange(next);
  };
  return (
    <div className="space-y-1.5 rounded-md border bg-card p-2.5">
      <div className="font-medium">新会话默认</div>
      {!rows.length ? (
        <div className="text-[12px] text-muted-foreground">
          还不知道它有哪些模式和模型：先开一个这种会话，回来就能选；或者在会话输入框下面的下拉里选好，点「设为新会话的默认」。
        </div>
      ) : (
        <div className="grid grid-cols-[88px_1fr] items-center gap-x-3 gap-y-2">
          {rows.map((r) => {
            const v = value[r.key];
            const known = !v || r.options.some((x) => x.value === v);
            return (
              <div key={r.key} className="contents">
                <span className="text-muted-foreground">{r.label}</span>
                <Select value={v ?? UNSET} onValueChange={(x) => put(r.key, x)}>
                  <SelectTrigger size="sm" className="w-64">
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    <SelectItem value={UNSET}>不指定（{id === "native" && r.key === "model" ? "第一个模型" : "跟 Agent 自己的默认"}）</SelectItem>
                    {!known && <SelectItem value={v}>{v}（现在没有这个了）</SelectItem>}
                    {r.options.map((x) => (
                      <SelectItem key={x.value} value={x.value}>
                        {x.name}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              </div>
            );
          })}
        </div>
      )}
      <div className="text-[11px] text-muted-foreground">
        只管新开的会话；`gerdr session new` 不带 --mode 时也用这里的模式（全放开的那种除外，防 Agent
        借它提权）。思考强度的选项跟着模型变，选的档位新模型不认就用模型的默认。
      </div>
    </div>
  );
}

function LoginHint({ cmd, then }: { cmd: string; then?: string }) {
  return (
    <div className="space-y-1">
      <div>在终端里跑这一条{then ? `，${then}` : "。"}</div>
      <div className="flex items-start gap-1.5">
        <code className="min-w-0 flex-1 rounded-sm bg-card px-2 py-1 font-mono break-all text-foreground">{cmd}</code>
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
