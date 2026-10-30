import { Check, Download, Loader2, Pencil, Plus, X } from "lucide-react";
import { useState } from "react";
import { toast } from "sonner";
import { AgentIcon } from "@/components/AgentIcon";
import { ConfirmDelete } from "@/components/ConfirmDelete";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { api } from "@/lib/api";
import { setConfig, useStore, type LLMConfig, type LLMModel, type LLMProvider } from "@/lib/store";

// 设置 → Agent 里「引擎」那块的 Native 服务商：native 引擎直接调的模型接口。用 native 引擎的 agent 可以只挑几家（AgentDef.providers）。
// 存进 config.json 的 llm（后端 native/config.go 读，每轮现取：改了下一轮就用新的）。配了至少一个模型，新建会话时才出现 Gerdr Agent。
// The protocol is picked per provider (a gateway can't be recognized by its URL): OpenAI-compatible or Anthropic.
// On OpenAI-compatible ones the backend guesses per model id (GPT → Responses, others → Chat Completions); thinking
// levels and context window are guessed too. Wrong guesses are fixed in config.json (api / efforts / window on the model).

const stop = (e: React.KeyboardEvent) => e.stopPropagation();
const EMPTY: LLMConfig = {}; // 选择器里别 `?? {}`：每次一个新对象，zustand 认成变了，无限重渲染

type Protocol = "openai" | "anthropic";
type Draft = { id: string; name: string; api: Protocol; baseUrl: string; keyMode: "key" | "env"; apiKey: string; apiKeyEnv: string; models: LLMModel[] };

const PROTOCOLS: Record<Protocol, { label: string; baseUrl: string; key: string; model: string }> = {
  openai: { label: "OpenAI 兼容", baseUrl: "https://api.deepseek.com/v1（到 /v1 为止）", key: "sk-…", model: "deepseek-v4-pro" },
  anthropic: { label: "Anthropic", baseUrl: "https://api.anthropic.com", key: "sk-ant-…", model: "claude-opus-5-5" },
};

const toDraft = (p: LLMProvider): Draft => ({
  id: p.id,
  name: p.name ?? "",
  api: p.api === "anthropic" ? "anthropic" : "openai",
  baseUrl: p.baseUrl,
  keyMode: p.apiKeyEnv && !p.apiKey ? "env" : "key",
  apiKey: p.apiKey ?? "",
  apiKeyEnv: p.apiKeyEnv ?? "",
  models: p.models ?? [],
});

function fromDraft(d: Draft): LLMProvider {
  if (!d.baseUrl.trim()) throw new Error(d.api === "anthropic" ? "填接口地址" : "填接口地址（到 /v1 为止）");
  if (!/^https?:\/\//.test(d.baseUrl.trim())) throw new Error("接口地址要以 http:// 或 https:// 开头");
  if (!d.id.trim()) throw new Error("起个名字");
  const p: LLMProvider = {
    id: d.id,
    name: d.name.trim() || undefined,
    api: d.api === "anthropic" ? "anthropic" : undefined,
    baseUrl: d.baseUrl.trim().replace(/\/+$/, ""),
    models: d.models,
  };
  if (d.keyMode === "key") p.apiKey = d.apiKey.trim() || undefined;
  else p.apiKeyEnv = d.apiKeyEnv.trim() || undefined;
  return p;
}

function slug(name: string, taken: Set<string>) {
  const base =
    name
      .toLowerCase()
      .replace(/[^a-z0-9]+/g, "-")
      .replace(/^-|-$/g, "") || "provider";
  let id = base;
  for (let n = 2; taken.has(id); n++) id = `${base}-${n}`;
  return id;
}

export function LLMSettings() {
  const llm = useStore((s) => s.config.llm ?? EMPTY);
  const providers = llm.providers ?? [];
  const [edit, setEdit] = useState<Draft | null>(null);
  const [isNew, setIsNew] = useState(false);
  const save = (next: LLMConfig) => setConfig({ llm: next });

  const onSave = () => {
    if (!edit) return;
    try {
      const p = fromDraft(edit);
      const list = isNew ? [...providers, p] : providers.map((x) => (x.id === p.id ? p : x));
      save({ ...llm, providers: list });
      toast.success(isNew && p.models.length ? "加好了：Gerdr Agent 和用 Native 引擎的 Agent 都能用它" : "已保存（下一轮就用新的）");
      setEdit(null);
    } catch (e: any) {
      toast.error(e.message);
    }
  };

  // 和上面 Claude Code / Codex 两张引擎卡片一个样子：图标 + 名字 + 一句话 + 右边状态；服务商列在卡片里面
  return (
    <div className="space-y-2.5 rounded-md border px-3 py-2 text-[13px]">
      {/* Same layout as the CLI engine cards: on phones the description gets its own row instead of being cut */}
      <div className="flex flex-wrap items-center gap-x-2 gap-y-0.5">
        <AgentIcon agent="native" className="size-4" />
        <span className="shrink-0 font-medium">Native</span>
        <span className="min-w-0 flex-1 truncate text-[12px] text-muted-foreground max-md:order-last max-md:basis-full max-md:pl-6 max-md:whitespace-normal">
          Gerdr 自己的引擎，直接调下面这些模型服务商（OpenAI 兼容或 Anthropic 接口）
        </span>
        {providers.some((p) => p.models.length) ? (
          <span className="ml-auto shrink-0 text-[12px] text-ok">配了 {providers.length} 家</span>
        ) : (
          <span className="ml-auto shrink-0 text-[12px] text-bad">还没配服务商</span>
        )}
      </div>
      <div className="text-[12px] text-muted-foreground">
        Gerdr Agent 用全部服务商；用 Native 引擎新建的 Agent 可以只挑其中几家。默认模型在各个 Agent 的「新会话默认」里设。
      </div>

      {providers.map((p) => (
        <div key={p.id} className="space-y-1.5 rounded-md bg-muted/40 px-3 py-2">
          <div className="flex items-center gap-2">
            {/* Capped and truncated rather than wrapped: the URL next to it shrinks first */}
            <span className="max-w-[50%] shrink-0 truncate font-medium">{p.name || p.id}</span>
            {p.api === "anthropic" && <span className="shrink-0 rounded-sm bg-muted px-1 text-[11px] text-muted-foreground">Anthropic</span>}
            <span className="min-w-0 truncate font-mono text-[12px] text-muted-foreground">{p.baseUrl}</span>
            {!p.apiKey && !p.apiKeyEnv && <span className="shrink-0 text-[12px] text-warn">没填 key</span>}
            {p.apiKeyEnv && !p.apiKey && <span className="shrink-0 text-[12px] text-muted-foreground">key 取自 ${p.apiKeyEnv}</span>}
            <span className="flex-1" />
            <Button
              size="icon"
              variant="ghost"
              className="size-7"
              title="编辑"
              onClick={() => {
                setIsNew(false);
                setEdit(toDraft(p));
              }}
            >
              <Pencil className="size-3.5" />
            </Button>
            <ConfirmDelete
              title="删除（用它的 Agent 会没有可用的模型）"
              onConfirm={() => save({ ...llm, providers: providers.filter((x) => x.id !== p.id) })}
            />
          </div>
          <div className="flex flex-wrap gap-1">
            {p.models.length ? (
              p.models.map((m) => (
                <span key={m.id} className="rounded-sm bg-muted px-1.5 py-0.5 font-mono text-[12px]">
                  {m.id}
                </span>
              ))
            ) : (
              <span className="text-[12px] text-warn">还没加模型</span>
            )}
          </div>
        </div>
      ))}

      {!edit && (
        <Button
          size="sm"
          variant="outline"
          onClick={() => {
            setIsNew(true);
            const taken = new Set(providers.map((p) => p.id));
            setEdit({ id: slug("provider", taken), name: "", api: "openai", baseUrl: "", keyMode: "key", apiKey: "", apiKeyEnv: "", models: [] });
          }}
        >
          <Plus className="size-3.5" /> 加一个模型服务商
        </Button>
      )}

      {edit && <Editor d={edit} isNew={isNew} taken={new Set(providers.map((p) => p.id))} onChange={setEdit} onSave={onSave} onCancel={() => setEdit(null)} />}
    </div>
  );
}

function Editor({
  d,
  isNew,
  taken,
  onChange,
  onSave,
  onCancel,
}: {
  d: Draft;
  isNew: boolean;
  taken: Set<string>;
  onChange: (d: Draft) => void;
  onSave: () => void;
  onCancel: () => void;
}) {
  const set = (p: Partial<Draft>) => onChange({ ...d, ...p });
  const [fetched, setFetched] = useState<string[] | null>(null);
  const [filter, setFilter] = useState("");
  const [busy, setBusy] = useState(false);
  const [adding, setAdding] = useState("");
  const [testing, setTesting] = useState<string | null>(null);
  const has = (id: string) => d.models.some((m) => m.id === id);
  const addModel = (id: string) => {
    id = id.trim();
    if (id && !has(id)) set({ models: [...d.models, { id }] });
  };
  const draftProvider = (): LLMProvider | null => {
    try {
      return fromDraft(d);
    } catch (e: any) {
      toast.error(e.message);
      return null;
    }
  };
  const fetchModels = async () => {
    const p = draftProvider();
    if (!p) return;
    setBusy(true);
    try {
      const r = await api.llmModels(p);
      setFetched(r.models);
      if (!r.models.length) toast("接口没返回模型，手动填 id");
    } catch (e: any) {
      toast.error(`拉不到模型列表：${e.message}`);
    } finally {
      setBusy(false);
    }
  };
  const test = async (m: LLMModel) => {
    const p = draftProvider();
    if (!p) return;
    setTesting(m.id);
    try {
      const r = await api.llmTest(p, m);
      const proto = r.api === "anthropic" ? "Anthropic Messages" : r.api === "responses" ? "Responses" : "Chat Completions";
      toast.success(`${m.id} 能用（${proto}）：${r.reply.slice(0, 60)}`);
    } catch (e: any) {
      toast.error(`${m.id} 不通：${e.message}`);
    } finally {
      setTesting(null);
    }
  };
  const shown = (fetched ?? []).filter((id) => !filter || id.toLowerCase().includes(filter.toLowerCase()));

  return (
    <div className="space-y-3 rounded-lg border bg-muted/30 p-3">
      {/* Phones: one column, each label pulled up against the field under it (a label column left the fields too narrow) */}
      <div className="grid grid-cols-[88px_minmax(0,1fr)] items-center gap-x-3 max-md:grid-cols-1 gap-y-2.5 max-md:[&>span.text-muted-foreground]:-mb-1.5 max-md:[&>span.text-muted-foreground]:pt-0">
        <span className="text-muted-foreground">名字</span>
        <Input
          value={d.name}
          onChange={(e) => set(isNew ? { name: e.target.value, id: slug(e.target.value, taken) } : { name: e.target.value })}
          onKeyDown={stop}
          className="h-8"
          placeholder="比如 DeepSeek、公司网关"
        />
        <span className="text-muted-foreground">协议</span>
        <Select value={d.api} onValueChange={(v) => set({ api: v as Protocol })}>
          <SelectTrigger size="sm" className="w-40 max-w-full max-md:w-full">
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            {(Object.keys(PROTOCOLS) as Protocol[]).map((k) => (
              <SelectItem key={k} value={k}>
                {PROTOCOLS[k].label}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
        <span className="text-muted-foreground">接口地址</span>
        <Input
          value={d.baseUrl}
          onChange={(e) => set({ baseUrl: e.target.value })}
          onKeyDown={stop}
          className="h-8 font-mono text-[12px]"
          placeholder={PROTOCOLS[d.api].baseUrl}
        />
        <span className="text-muted-foreground">API key</span>
        <div className="flex items-center gap-2">
          <Select value={d.keyMode} onValueChange={(v) => set({ keyMode: v as Draft["keyMode"] })}>
            <SelectTrigger size="sm" className="w-32 shrink-0">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value="key">直接填</SelectItem>
              <SelectItem value="env">取环境变量</SelectItem>
            </SelectContent>
          </Select>
          {d.keyMode === "key" ? (
            <Input
              type="password"
              autoComplete="off"
              value={d.apiKey}
              onChange={(e) => set({ apiKey: e.target.value })}
              onKeyDown={stop}
              className="h-8 font-mono text-[12px]"
              placeholder={`${PROTOCOLS[d.api].key}（存在跑 Gerdr 的电脑的 secrets.json 里，不进 config.json）`}
            />
          ) : (
            <Input
              value={d.apiKeyEnv}
              onChange={(e) => set({ apiKeyEnv: e.target.value })}
              onKeyDown={stop}
              className="h-8 font-mono text-[12px]"
              placeholder="环境变量名，比如 DEEPSEEK_API_KEY（Gerdr 启动时的环境里要有）"
            />
          )}
        </div>
        <span className="self-start pt-1.5 text-muted-foreground">模型</span>
        <div className="space-y-2">
          <div className="flex flex-wrap gap-1">
            {d.models.map((m) => (
              <span key={m.id} className="flex items-center gap-1 rounded-sm bg-card py-0.5 pr-0.5 pl-1.5 font-mono text-[12px] ring-1 ring-border">
                {m.id}
                <button
                  className="rounded-sm px-1 text-muted-foreground hover:bg-muted hover:text-foreground"
                  title="试一下：让它说一句话"
                  onClick={() => test(m)}
                >
                  {testing === m.id ? <Loader2 className="size-3 animate-spin" /> : "试"}
                </button>
                <button
                  className="rounded-sm p-0.5 text-muted-foreground hover:bg-muted hover:text-foreground"
                  title="去掉"
                  onClick={() => set({ models: d.models.filter((x) => x.id !== m.id) })}
                >
                  <X className="size-3" />
                </button>
              </span>
            ))}
            {!d.models.length && <span className="text-[12px] text-muted-foreground">还没加，手动填 id 或者从接口拉</span>}
          </div>
          <div className="flex items-center gap-2">
            <Input
              value={adding}
              onChange={(e) => setAdding(e.target.value)}
              onKeyDown={(e) => {
                stop(e);
                if (e.key === "Enter" && !e.nativeEvent.isComposing) {
                  addModel(adding);
                  setAdding("");
                }
              }}
              className="h-8 font-mono text-[12px]"
              placeholder={`模型 id，回车加上（比如 ${PROTOCOLS[d.api].model}）`}
            />
            <Button size="sm" variant="outline" className="shrink-0" onClick={fetchModels} disabled={busy}>
              {busy ? <Loader2 className="size-3.5 animate-spin" /> : <Download className="size-3.5" />} 从接口拉
            </Button>
          </div>
          {fetched && fetched.length > 0 && (
            <div className="space-y-1.5 rounded-md border bg-card p-2">
              <Input
                value={filter}
                onChange={(e) => setFilter(e.target.value)}
                onKeyDown={stop}
                className="h-7 text-[12px]"
                placeholder={`在 ${fetched.length} 个里找`}
              />
              <div className="flex max-h-40 flex-wrap gap-1 overflow-auto">
                {shown.map((id) => (
                  <button
                    key={id}
                    className="flex items-center gap-1 rounded-sm px-1.5 py-0.5 font-mono text-[12px] hover:bg-muted data-[on=true]:bg-brand/10 data-[on=true]:text-brand"
                    data-on={has(id)}
                    onClick={() => (has(id) ? set({ models: d.models.filter((x) => x.id !== id) }) : addModel(id))}
                  >
                    {has(id) && <Check className="size-3" />}
                    {id}
                  </button>
                ))}
              </div>
            </div>
          )}
        </div>
      </div>
      <div className="text-[12px] text-muted-foreground">
        {d.api === "anthropic" ? (
          <>
            每个模型都走 Anthropic Messages（/v1/messages，地址带不带 /v1 都行）。Claude 4.6 及以后自动开 adaptive 思考，档位 low → max；
            别的模型不发思考参数。猜错了在 config.json 的模型上写 <code className="font-mono">efforts</code>、<code className="font-mono">window</code>、
            <code className="font-mono">maxOutput</code>。
          </>
        ) : (
          <>
            协议、思考档位、上下文窗口按模型 id 猜：gpt / o 系列走 Responses，别的走 Chat Completions；DeepSeek 是纯文本模型、思考档位 关 / 高 / 最高。 猜错了在
            config.json 的模型上写 <code className="font-mono">api</code>、<code className="font-mono">efforts</code>、<code className="font-mono">window</code>
            。
          </>
        )}
      </div>
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
