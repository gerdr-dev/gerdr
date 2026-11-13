import { Check, Copy, Pencil, Plus } from "lucide-react";
import { useEffect, useState } from "react";
import { toast } from "sonner";
import { AgentIcon, SPRITE } from "@/components/AgentIcon";
import { AgentGlyph, autoGlyph } from "@/components/AgentGlyph";
import { IconPicker } from "@/components/IconPicker";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import { Dialog, DialogContent, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { ConfirmDelete } from "@/components/ConfirmDelete";
import { Input } from "@/components/ui/input";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { LLMSettings } from "@/components/LLMSettings";
import { api } from "@/lib/api";
import { MODE, defaultsOf, flatOptions, setDefaults, withoutDefaults } from "@/lib/defaults";
import { t } from "@/lib/i18n";
import { get, setConfig, useStore, type AgentDef, type LLMProvider } from "@/lib/store";
import type { AgentInfo, ExtView } from "@/lib/types";
import { cn } from "@/lib/utils";
import { useCopied } from "@/lib/useCopied";

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
  icon: string; // a GLYPHS / LINES key, or "sprite:<key>" (an add-on's sprite); "" = default (vendor logo / automatic)
  color: string; // a PALETTE key; "" = the icon's own colours
};

const kindName = (k: Kind) => (k === "command" ? t("agentSettings.kind.command") : { claude: "Claude Code", codex: "Codex", native: "Native" }[k]);

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
  icon: d.icon ?? "",
  color: d.color ?? "",
});

function fromDraft(d: Draft): AgentDef {
  const env: Record<string, string> = {};
  for (const line of d.env.split("\n")) {
    const ln = line.trim();
    if (!ln || ln.startsWith("#")) continue;
    const i = ln.indexOf("=");
    if (i <= 0) throw new Error(t("agentSettings.err.envFormat", { line: ln }));
    env[ln.slice(0, i).trim()] = ln.slice(i + 1).trim();
  }
  const words = (s: string) => s.split(/\s+/).filter(Boolean);
  if (!d.name.trim()) throw new Error(t("agentSettings.err.nameRequired"));
  if (d.kind === "command") {
    if (!d.command.trim()) throw new Error(t("agentSettings.err.commandRequired"));
    return { id: d.id, name: d.name.trim(), command: words(d.command), env: Object.keys(env).length ? env : undefined, ...looks(d) };
  }
  const native = d.kind === "native"; // Native 引擎没有可执行文件，启动参数 / 环境变量用不上
  if (native && !d.providers.length) throw new Error(t("agentSettings.err.providerRequired"));
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
    ...looks(d),
  };
}

/**
 * Icon and colour as saved; a colour only means something on a built-in icon (not on the Claude Code / Codex logo, nor
 * on a sprite, which draws its own).
 */
const looks = (d: Draft) => ({ icon: d.icon || undefined, color: (hasGlyph(d) && d.color) || undefined });
const hasGlyph = (d: Draft) => (d.icon ? !d.icon.startsWith(SPRITE) : d.kind !== "claude" && d.kind !== "codex");

/**
 * The part of a draft the backend bakes into the agent's process (hub.sameProcess): changing it makes the backend
 * close that process on save, which cuts off every turn running on it. The name and the new-session defaults don't.
 */
const processKey = ({ name: _n, defaults: _d, icon: _i, color: _c, ...rest }: Draft) => JSON.stringify(rest);

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
    icon: "",
    color: "",
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
        toast.success(t("agentSettings.savedBuiltin"));
      } else {
        const d = fromDraft(edit);
        setDefaults(edit.id, values, { agents: isNew ? [...defs, d] : defs.map((x) => (x.id === d.id ? d : x)) });
        const restarted = !isNew && processKey(edit) !== processKey(JSON.parse(orig));
        toast.success(isNew ? t("agentSettings.added", { name: d.name }) : restarted ? t("agentSettings.savedRestarted") : t("common.saved"));
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
        <div className="text-[12px] text-muted-foreground">{t("agentSettings.intro")}</div>
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
                {/* The engine label gives way first, the name only past 60% (on phones it got cut to "Claude…") */}
                <span className="max-w-[60%] shrink-0 truncate font-medium">{a.name}</span>
                <span className="min-w-0 flex-1 truncate text-[12px] text-muted-foreground">
                  {t("agentSettings.engineOf", { name: kindName((def ? (def.base ?? "command") : a.id) as Kind) })}
                  {!def && t("agentSettings.builtinSuffix")}
                </span>
                <span className={cn("shrink-0 text-[12px]", a.available ? "text-ok" : "text-bad")}>
                  {a.available ? t("agentSettings.available") : t("agentSettings.unavailable")}
                </span>
                <div className="flex shrink-0 items-center gap-2">
                  <Button
                    size="icon"
                    variant="ghost"
                    className="size-7"
                    title={def ? t("common.edit") : t("agentSettings.newSessionDefaults")}
                    onClick={() => open(def ? toDraft(def) : { ...blank(a.id as Kind, a.name), id: a.id, builtin: true, defaults: defaultsOf(a.id) }, false)}
                  >
                    <Pencil className="size-3.5" />
                  </Button>
                  {def && (
                    <ConfirmDelete
                      title={t("agentSettings.deleteTitle")}
                      onConfirm={() => setConfig({ agents: defs.filter((x) => x.id !== def.id), ...withoutDefaults(def.id) })}
                    />
                  )}
                </div>
              </div>
              {!a.available && a.error && <div className="mt-1 pl-6 text-[12px] text-bad">{a.error}</div>}
              {(summary || chips.length > 0) && (
                <div className="mt-1 flex flex-wrap gap-x-3 gap-y-0.5 pl-6 text-[12px] text-muted-foreground">
                  {chips.map((c) => (
                    <span key={c}>{c}</span>
                  ))}
                  {summary && <span>{t("agentSettings.defaultsSummary", { summary })}</span>}
                </div>
              )}
            </div>
          );
        })}
      </div>

      {/* 只一个「新建」：换账号、外部 ACP agent 都在弹窗里选「引擎」/ 展开「高级」 */}
      <Button size="sm" variant="outline" onClick={() => open(blank("claude", t("agentSettings.newRoleName")), true)}>
        <Plus className="size-3.5" /> {t("agentSettings.new")}
      </Button>

      {/* 新建和编辑都在弹窗里（叠在设置弹窗上面）：原来展开在列表下面，列表一长就要往下找 */}
      <Dialog open={!!edit} onOpenChange={(o) => !o && setEdit(null)}>
        {/* 宽度写成内联样式：界面的 Tailwind 是浏览器里现编的，页面上头一回出现的 class（sm:max-w-2xl）样式要晚一拍才生成，
            第一次打开时先按没限宽铺满全屏、再缩回来（用户报的），第二次才正常 */}
        <DialogContent
          // Three parts: title and buttons stay put, only the form between them scrolls (Editor)
          className="flex max-h-[85vh] flex-col gap-0 overflow-hidden p-0 text-[13px]"
          style={{ maxWidth: "min(42rem, calc(100% - 2rem))" }}
          onInteractOutside={(e) => dirty && e.preventDefault()}
        >
          {/* Room for the close button on the right; left-aligned on phones too, a centered long title ran under the X */}
          {/* Same title as the Settings dialog under it (SettingsDialog.tsx): size, padding, the phone's 15px */}
          <DialogHeader className="min-w-0 shrink-0 border-b px-6 pt-5 pr-12 pb-4 text-left max-md:px-4 max-md:pt-3 max-md:pb-3">
            <DialogTitle className="truncate leading-normal max-md:text-[15px]">
              {!edit
                ? ""
                : edit.builtin
                  ? t("agentSettings.titleBuiltin", { name: edit.name })
                  : isNew
                    ? t("agentSettings.titleNew")
                    : t("agentSettings.titleEdit", { name: edit.name })}
            </DialogTitle>
          </DialogHeader>
          {edit && (
            <Editor
              d={edit}
              restarts={!isNew && !edit.builtin && processKey(edit) !== processKey(JSON.parse(orig))}
              onChange={setEdit}
              onSave={save}
              onCancel={() => setEdit(null)}
            />
          )}
        </DialogContent>
      </Dialog>

      <Engines agents={agents} />
    </div>
  );
}

function roleChips(d: AgentDef, providers: LLMProvider[]) {
  const out: string[] = [];
  if (d.prompt) out.push(t("agentSettings.chip.prompt"));
  if (d.skills)
    out.push(
      d.skills.length
        ? t(d.skills.length === 1 ? "agentSettings.chip.skillsOne" : "agentSettings.chip.skills", { n: d.skills.length })
        : t("agentSettings.chip.noSkills"),
    );
  if (d.mcp)
    out.push(
      d.mcp.length ? t(d.mcp.length === 1 ? "agentSettings.chip.mcpOne" : "agentSettings.chip.mcp", { n: d.mcp.length }) : t("agentSettings.chip.noMcp"),
    );
  if (d.providers?.length)
    out.push(
      t("agentSettings.chip.providers", {
        list: d.providers.map((id) => providers.find((p) => p.id === id)?.name || t("agentSettings.deletedProvider", { id })).join(t("agentSettings.listSep")),
      }),
    );
  if (d.env && Object.keys(d.env).length) out.push(t("agentSettings.chip.env"));
  return out;
}

/** 「引擎」那块：Claude Code / Codex 是本机装的 CLI（账号在它们自己那边登录），Native 用这里配的模型服务商。 */
function Engines({ agents }: { agents: AgentInfo[] }) {
  const cli = (id: string, name: string, how: string) => {
    const a = agents.find((x) => x.id === id);
    return (
      // On phones the one-line description goes to its own row: squeezed into the first row it wrapped the name and
      // pushed the status into one character per line
      <div className="flex flex-wrap items-center gap-x-2 gap-y-0.5 rounded-md border px-3 py-2">
        <AgentIcon agent={id} className="size-4" />
        <span className="shrink-0 font-medium whitespace-nowrap">{name}</span>
        <span className="min-w-0 flex-1 text-[12px] text-muted-foreground max-md:order-last max-md:basis-full max-md:pl-6">{how}</span>
        {a?.available ? (
          <span className="ml-auto shrink-0 text-[12px] text-ok">{t("agentSettings.installed")}</span>
        ) : (
          <span className="ml-auto min-w-0 truncate text-[12px] text-bad">{a?.error || t("agentSettings.notInstalled")}</span>
        )}
      </div>
    );
  };
  return (
    <div className="space-y-3 border-t pt-4">
      <div>
        <div className="font-medium">{t("agentSettings.engines")}</div>
        <div className="text-[12px] text-muted-foreground">{t("agentSettings.enginesHint")}</div>
      </div>
      <div className="space-y-1.5">
        {cli("claude", "Claude Code", t("agentSettings.claudeHow"))}
        {cli("codex", "Codex", t("agentSettings.codexHow"))}
      </div>
      <LLMSettings />
    </div>
  );
}

function Editor({
  d,
  restarts,
  onChange,
  onSave,
  onCancel,
}: {
  d: Draft;
  restarts: boolean;
  onChange: (d: Draft) => void;
  onSave: () => void;
  onCancel: () => void;
}) {
  const set = (p: Partial<Draft>) => onChange({ ...d, ...p });
  // Sessions on this agent with a turn in flight: saving a restarting change cuts them off
  const busy = useStore((s) => Object.values(s.sessions).filter((x) => x.agent === d.id && (x.status === "running" || x.status === "waiting")).length);
  const providers = useStore((s) => s.config.llm?.providers) ?? NO_PROVIDERS;
  const dir = /^CLAUDE_CONFIG_DIR=(.+)$/m.exec(d.env)?.[1]?.trim();
  const codexHome = /^CODEX_HOME=(.+)$/m.exec(d.env)?.[1]?.trim();
  const login = d.kind === "claude" && dir ? `CLAUDE_CONFIG_DIR=${dir} claude` : d.kind === "codex" && codexHome ? `CODEX_HOME=${codexHome} codex login` : "";
  const role = d.kind !== "command" && !d.builtin;
  // 「高级」里填过东西（另一个账号）就展开着，不然收起来：大多数角色用不到
  const [adv, setAdv] = useState(!!(d.env || d.bin || d.args) || d.kind === "command");
  return (
    <div className="flex min-h-0 flex-1 flex-col">
      {/* the scrolling part, its scrollbar at the dialog's edge */}
      <div className="min-h-0 flex-1 space-y-3 overflow-auto px-6 py-4 max-md:px-4">
        {/* Phones: one column, each label pulled up against the field under it (a label column left the fields too narrow) */}
        <div className="grid grid-cols-[88px_minmax(0,1fr)] items-center gap-x-3 max-md:grid-cols-1 gap-y-2.5 max-md:[&>span.text-muted-foreground]:-mb-1.5 max-md:[&>span.text-muted-foreground]:pt-0">
          <span className="text-muted-foreground">{t("agentSettings.name")}</span>
          {d.builtin ? (
            <span className="font-medium">{d.name}</span>
          ) : (
            <Input value={d.name} onChange={(e) => set({ name: e.target.value })} onKeyDown={stop} className="h-8" />
          )}
          {!d.builtin && (
            <>
              <span className="self-start pt-1.5 text-muted-foreground">{t("agentSettings.icon")}</span>
              <RoleIconPicker d={d} onChange={set} />
            </>
          )}
          {!d.builtin && (
            <>
              <span className="text-muted-foreground">{t("agentSettings.engine")}</span>
              <Select
                value={d.kind}
                // 换到 Native 时先把服务商全勾上（至少要勾一个，省得人再去点）
                onValueChange={(v) => set({ kind: v as Kind, ...(v === "native" && !d.providers.length ? { providers: allProviders() } : {}) })}
              >
                <SelectTrigger size="sm" className="w-56 max-w-full max-md:w-full">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="claude">Claude Code</SelectItem>
                  <SelectItem value="codex">Codex</SelectItem>
                  {(providers.length > 0 || d.kind === "native") && <SelectItem value="native">Native</SelectItem>}
                  <SelectItem value="command">{t("agentSettings.externalAcp")}</SelectItem>
                </SelectContent>
              </Select>
            </>
          )}
          {d.kind === "native" && !d.builtin && (
            <>
              <span className="self-start pt-1 text-muted-foreground">{t("agentSettings.providers")}</span>
              <div className="space-y-1">
                <div className="flex flex-wrap gap-x-4 gap-y-1">
                  {[
                    ...providers.map((p) => ({ id: p.id, name: p.name || p.id })),
                    ...d.providers.filter((id) => !providers.some((p) => p.id === id)).map((id) => ({ id, name: t("agentSettings.deletedProvider", { id }) })),
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
                <div className="text-[11px] text-muted-foreground">{t("agentSettings.providersHint")}</div>
              </div>
            </>
          )}
          {role && (
            <>
              <span className="self-start pt-1.5 text-muted-foreground">{t("agentSettings.prompt")}</span>
              <div className="space-y-1">
                <textarea
                  className="min-h-24 w-full rounded-md border bg-card p-2 text-[12px]"
                  value={d.prompt}
                  onChange={(e) => set({ prompt: e.target.value })}
                  onKeyDown={stop}
                  placeholder={t("agentSettings.promptPlaceholder")}
                />
                <div className="text-[11px] text-muted-foreground">{t("agentSettings.promptHint")}</div>
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
                {adv ? "▾" : "▸"} {t("agentSettings.advanced")}
              </button>
            )}
            {(adv || d.kind === "command") && (
              <div className="grid grid-cols-[88px_minmax(0,1fr)] items-center gap-x-3 max-md:grid-cols-1 gap-y-2.5 max-md:[&>span.text-muted-foreground]:-mb-1.5 max-md:[&>span.text-muted-foreground]:pt-0">
                {d.kind === "command" ? (
                  <>
                    <span className="text-muted-foreground">{t("agentSettings.command")}</span>
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
                    <span className="text-muted-foreground">{t("agentSettings.bin")}</span>
                    <Input
                      value={d.bin}
                      onChange={(e) => set({ bin: e.target.value })}
                      onKeyDown={stop}
                      className="h-8 font-mono text-[12px]"
                      placeholder={t("agentSettings.binPlaceholder", { kind: d.kind })}
                    />
                    <span className="text-muted-foreground">{t("agentSettings.args")}</span>
                    <Input
                      value={d.args}
                      onChange={(e) => set({ args: e.target.value })}
                      onKeyDown={stop}
                      className="h-8 font-mono text-[12px]"
                      placeholder={t("agentSettings.argsPlaceholder", { example: d.kind === "claude" ? "--settings ~/my-settings.json" : "-c model=o3" })}
                    />
                  </>
                )}
                <span className="self-start pt-1.5 text-muted-foreground">{t("agentSettings.env")}</span>
                <textarea
                  className="h-24 w-full rounded-md border bg-card p-2 font-mono text-[12px]"
                  value={d.env}
                  spellCheck={false}
                  onChange={(e) => set({ env: e.target.value })}
                  onKeyDown={stop}
                  placeholder={t("agentSettings.envPlaceholder")}
                />
              </div>
            )}
            {adv && d.kind === "claude" && (
              <div className="space-y-1.5 rounded-md bg-muted/60 p-2.5 text-[12px] text-muted-foreground">
                <div>
                  {t("agentSettings.claudeAccount1")}
                  <code className="font-mono">CLAUDE_CONFIG_DIR</code>
                  {t("agentSettings.claudeAccount2")}
                  <code className="font-mono">ANTHROPIC_API_KEY=sk-…</code>
                  {t("agentSettings.claudeAccount3")}
                  <code className="font-mono">ANTHROPIC_BASE_URL</code>
                  {t("agentSettings.claudeAccount4")}
                </div>
                {login && <LoginHint cmd={login} then={t("agentSettings.claudeLoginThen")} />}
              </div>
            )}
            {adv && d.kind === "codex" && (
              <div className="space-y-1.5 rounded-md bg-muted/60 p-2.5 text-[12px] text-muted-foreground">
                <div>
                  {t("agentSettings.codexAccount1")}
                  <code className="font-mono">CODEX_HOME</code>
                  {t("agentSettings.codexAccount2")}
                  <code className="font-mono">OPENAI_API_KEY=…</code>
                  {t("agentSettings.codexAccount3")}
                </div>
                {login && <LoginHint cmd={login} />}
              </div>
            )}
          </div>
        )}
      </div>

      {/* Outside the scrolling part, so Save is always in reach on a long form; flush with the dialog's bottom edge */}
      <div className="flex shrink-0 items-center justify-end gap-2 border-t px-6 py-3 max-md:px-4">
        {restarts && (
          <div className={cn("mr-auto text-[12px]", busy ? "text-warn" : "text-muted-foreground")}>
            {busy ? t(busy === 1 ? "agentSettings.restartBusyOne" : "agentSettings.restartBusy", { n: busy }) : t("agentSettings.restartIdle")}
          </div>
        )}
        <Button size="sm" variant="ghost" onClick={onCancel}>
          {t("common.cancel")}
        </Button>
        <Button size="sm" onClick={onSave}>
          {t("common.save")}
        </Button>
      </div>
    </div>
  );
}

/**
 * The role's icon (IconPicker): "默认" is the vendor logo for Claude Code / Codex, an automatic glyph otherwise; no
 * colour for the vendor logos and sprites (those draw their own).
 */
function RoleIconPicker({ d, onChange }: { d: Draft; onChange: (p: Partial<Draft>) => void }) {
  const vendor = d.kind === "claude" || d.kind === "codex";
  return (
    <IconPicker
      icon={d.icon}
      color={d.color}
      onChange={onChange}
      colorable={hasGlyph(d)}
      def={{
        title: vendor ? t("agentSettings.iconDefaultVendor", { name: kindName(d.kind) }) : t("agentSettings.iconDefaultAuto"),
        node: vendor ? <AgentIcon agent={d.kind} className="size-5" /> : <AgentGlyph name={autoGlyph(d.id)} color={d.color} className="size-5" />,
        glyph: autoGlyph(d.id),
      }}
    />
  );
}

/** global: given with "全部" (the global source includes it); every Cand can be picked. */
export type Cand = { name: string; from: string; description?: string; global: boolean };

/**
 * What an agent on this engine can pick (`skills` / `mcp`) and what it gets with "全部" (global settings).
 * A pick isn't bound by the global source (backend ext.GerdrSkillsOn…): every switched-on one is offered, gerdr's
 * pool and the engine's own; switched-off ones aren't (off is a global kill switch). Gerdr Agent's own skills are
 * Claude Code's; for MCP it only ever uses gerdr's.
 */
export function candidates(v: ExtView, kind: ExtKind) {
  const s = v.settings;
  const own = kind === "codex" ? "Codex" : "Claude Code";
  const skills: Cand[] = [];
  const mcp: Cand[] = [];
  const add = (list: Cand[], c: Cand) => {
    const had = list.find((x) => x.name === c.name);
    if (had)
      had.global ||= c.global; // same name on both sides: the engine gets one of them under "全部"
    else list.push(c);
  };
  for (const sk of v.builtin) if (sk.enabled) add(skills, { name: sk.name, from: t("agentSettings.fromBuiltin"), description: sk.description, global: true });
  for (const sk of v.skills) if (sk.enabled) add(skills, { name: sk.name, from: "Gerdr", description: sk.description, global: s.skillSource !== "agent" });
  // A project's list covers every engine: both engines' own are offered
  const engines = kind === "project" ? (["Claude Code", "Codex"] as const) : ([own] as const);
  for (const e of engines)
    for (const sk of e === "Codex" ? v.agent.codexSkills : v.agent.claudeSkills)
      add(skills, { name: sk.name, from: e, description: sk.description, global: s.skillSource !== "gerdr" });
  for (const m of s.mcp) if (m.enabled) add(mcp, { name: m.name, from: "Gerdr", global: kind === "native" || s.mcpSource !== "agent" });
  if (kind !== "native")
    for (const e of engines)
      for (const m of e === "Codex" ? v.agent.codexMCP : v.agent.claudeMCP) if (m.enabled) add(mcp, { name: m.name, from: e, global: s.mcpSource !== "gerdr" });
  return { skills, mcp };
}

/** An agent's engine, or "project": what a project switches off, which applies to every engine (ProjectExtDialog). */
type ExtKind = "claude" | "codex" | "native" | "project";

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
  const version = useStore((s) => s.extVersion);
  useEffect(() => {
    api
      .ext()
      .then(setView)
      .catch((e) => toast.error(t("agentSettings.extLoadFailed", { error: e.message })));
  }, [version]);
  if (!view) return <div className="text-[12px] text-muted-foreground">{t("agentSettings.extLoading")}</div>;
  const c = candidates(view, kind);
  return (
    <div className="grid gap-3 md:grid-cols-2">
      <Pick
        label="Skill"
        items={c.skills}
        value={skills}
        onChange={(v) => onChange({ skills: v })}
        note={kind === "claude" ? t("agentSettings.noteClaudeSkill") : kind === "codex" ? t("agentSettings.noteCodexSkill") : undefined}
      />
      <Pick
        label="MCP"
        items={c.mcp}
        value={mcp}
        onChange={(v) => onChange({ mcp: v })}
        note={kind === "claude" ? t("agentSettings.noteClaudeMcp") : undefined}
        allHint={kind === "native" ? t("agentSettings.allHintNative", { n: c.mcp.filter((i) => i.global).length }) : undefined}
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
  const gone = (value ?? []).filter((n) => !items.some((i) => i.name === n)).map((n): Cand => ({ name: n, from: t("agentSettings.fromGone"), global: false }));
  return (
    <div className="space-y-1.5 rounded-md border bg-card p-2.5">
      <div className="flex items-center gap-2">
        <span className="font-medium">{label}</span>
        <span className="flex-1" />
        <div className="flex rounded-md border text-[12px]">
          {(
            [
              [t("agentSettings.pickAll"), false],
              [t("agentSettings.pickSome"), true],
            ] as const
          ).map(([text, on]) => (
            <button
              key={text}
              className={cn(
                "px-2 py-0.5 first:rounded-l-md last:rounded-r-md",
                limited === on ? "bg-muted text-foreground" : "text-muted-foreground hover:text-foreground",
              )}
              // switching to a pick starts from what "全部" gives, so nothing changes until a box is touched
              onClick={() => onChange(on ? (value ?? items.filter((i) => i.global).map((i) => i.name)) : null)}
            >
              {text}
            </button>
          ))}
        </div>
      </div>
      {!limited ? (
        <div className="text-[12px] text-muted-foreground">{allHint ?? t("agentSettings.allHint", { label, n: items.filter((i) => i.global).length })}</div>
      ) : (
        <div className="max-h-52 space-y-0.5 overflow-auto">
          {items.length + gone.length === 0 && <div className="text-[12px] text-muted-foreground">{t("agentSettings.nothingToPick")}</div>}
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
  if (o?.modes?.availableModes.length)
    rows.push({ key: MODE, label: t("agentSettings.mode"), options: o.modes.availableModes.map((m) => ({ value: m.id, name: m.name })) });
  for (const c of o?.config ?? [])
    rows.push({
      key: c.id,
      label: c.id === "model" ? t("agentSettings.model") : c.id === "effort" ? t("agentSettings.effort") : c.name,
      options: flatOptions(c),
    });
  if (base === "native") {
    // Native 的模型直接按配置里的服务商列（不靠上次报的那份：那份是全部服务商的，换了勾选也不跟着变）
    const ps = (llm?.providers ?? []).filter((p) => !providers?.length || providers.includes(p.id));
    const models = ps.flatMap((p) =>
      p.models.map((m) => ({ value: `${p.id}/${m.id}`, name: `${m.name || m.id}${ps.length > 1 ? ` · ${p.name || p.id}` : ""}` })),
    );
    const row = { key: "model", label: t("agentSettings.model"), options: models };
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
      <div className="font-medium">{t("agentSettings.newSessionDefaults")}</div>
      {!rows.length ? (
        <div className="text-[12px] text-muted-foreground">{t("agentSettings.noOptions")}</div>
      ) : (
        <div className="grid grid-cols-[88px_minmax(0,1fr)] items-center gap-x-3 max-md:grid-cols-1 gap-y-2 max-md:gap-y-2.5 max-md:[&>div>span.text-muted-foreground]:-mb-1 max-md:[&>div>span.text-muted-foreground]:pt-0">
          {rows.map((r) => {
            const v = value[r.key];
            const known = !v || r.options.some((x) => x.value === v);
            return (
              <div key={r.key} className="contents">
                <span className="text-muted-foreground">{r.label}</span>
                <Select value={v ?? UNSET} onValueChange={(x) => put(r.key, x)}>
                  <SelectTrigger size="sm" className="w-64 max-w-full max-md:w-full">
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    <SelectItem value={UNSET}>
                      {t("agentSettings.unset", {
                        what: id === "native" && r.key === "model" ? t("agentSettings.unsetFirstModel") : t("agentSettings.unsetAgentDefault"),
                      })}
                    </SelectItem>
                    {!known && <SelectItem value={v}>{t("agentSettings.optionGone", { value: v })}</SelectItem>}
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
      <div className="text-[11px] text-muted-foreground">{t("agentSettings.defaultsNote")}</div>
    </div>
  );
}

function LoginHint({ cmd, then }: { cmd: string; then?: string }) {
  const [copied, copy] = useCopied();
  return (
    <div className="space-y-1">
      <div>{then ? t("agentSettings.runThisThen", { then }) : t("agentSettings.runThis")}</div>
      <div className="flex items-start gap-1.5">
        <code className="min-w-0 flex-1 rounded-sm bg-card px-2 py-1 font-mono break-all text-foreground">{cmd}</code>
        <Button size="icon" variant="ghost" className="size-7 shrink-0" title={t("common.copy")} onClick={() => copy(cmd)}>
          {copied ? <Check className="size-3.5 text-ok" /> : <Copy className="size-3.5" />}
        </Button>
      </div>
    </div>
  );
}
