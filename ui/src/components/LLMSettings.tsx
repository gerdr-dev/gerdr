import { Check, Download, Loader2, Pencil, Plus, X } from "lucide-react";
import { useState } from "react";
import { toast } from "sonner";
import { AgentIcon } from "@/components/AgentIcon";
import { ConfirmDelete } from "@/components/ConfirmDelete";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { api } from "@/lib/api";
import { t } from "@/lib/i18n";
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

// Getters: the labels go through t()
const PROTOCOLS: Record<Protocol, { readonly label: string; readonly baseUrl: string; key: string; model: string }> = {
  openai: {
    get label() {
      return t("llmSettings.openai");
    },
    get baseUrl() {
      return t("llmSettings.openaiBaseUrl");
    },
    key: "sk-…",
    model: "deepseek-v4-pro",
  },
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
  if (!d.baseUrl.trim()) throw new Error(t(d.api === "anthropic" ? "llmSettings.needBaseUrl" : "llmSettings.needBaseUrlV1"));
  if (!/^https?:\/\//.test(d.baseUrl.trim())) throw new Error(t("llmSettings.badBaseUrl"));
  if (!d.id.trim()) throw new Error(t("llmSettings.needName"));
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
      toast.success(t(isNew && p.models.length ? "llmSettings.addedToast" : "llmSettings.savedToast"));
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
          {t("llmSettings.desc")}
        </span>
        {providers.some((p) => p.models.length) ? (
          <span className="ml-auto shrink-0 text-[12px] text-ok">
            {t(providers.length === 1 ? "llmSettings.configuredOne" : "llmSettings.configured", { n: providers.length })}
          </span>
        ) : (
          <span className="ml-auto shrink-0 text-[12px] text-bad">{t("llmSettings.noProviders")}</span>
        )}
      </div>
      <div className="text-[12px] text-muted-foreground">{t("llmSettings.hint")}</div>

      {providers.map((p) => (
        <div key={p.id} className="space-y-1.5 rounded-md bg-muted/40 px-3 py-2">
          <div className="flex items-center gap-2">
            {/* Capped and truncated rather than wrapped: the URL next to it shrinks first */}
            <span className="max-w-[50%] shrink-0 truncate font-medium">{p.name || p.id}</span>
            {p.api === "anthropic" && <span className="shrink-0 rounded-sm bg-muted px-1 text-[11px] text-muted-foreground">Anthropic</span>}
            <span className="min-w-0 truncate font-mono text-[12px] text-muted-foreground">{p.baseUrl}</span>
            {!p.apiKey && !p.apiKeyEnv && <span className="shrink-0 text-[12px] text-warn">{t("llmSettings.noKey")}</span>}
            {p.apiKeyEnv && !p.apiKey && (
              <span className="shrink-0 text-[12px] text-muted-foreground">{t("llmSettings.keyFromEnv", { env: p.apiKeyEnv })}</span>
            )}
            <span className="flex-1" />
            <Button
              size="icon"
              variant="ghost"
              className="size-7"
              title={t("common.edit")}
              onClick={() => {
                setIsNew(false);
                setEdit(toDraft(p));
              }}
            >
              <Pencil className="size-3.5" />
            </Button>
            <ConfirmDelete title={t("llmSettings.deleteTitle")} onConfirm={() => save({ ...llm, providers: providers.filter((x) => x.id !== p.id) })} />
          </div>
          <div className="flex flex-wrap gap-1">
            {p.models.length ? (
              p.models.map((m) => (
                <span key={m.id} className="rounded-sm bg-muted px-1.5 py-0.5 font-mono text-[12px]">
                  {m.id}
                </span>
              ))
            ) : (
              <span className="text-[12px] text-warn">{t("llmSettings.noModels")}</span>
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
          <Plus className="size-3.5" /> {t("llmSettings.addProvider")}
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
      if (!r.models.length) toast(t("llmSettings.noModelsFetched"));
    } catch (e: any) {
      toast.error(t("llmSettings.fetchFailed", { error: e.message }));
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
      toast.success(t("llmSettings.testOk", { id: m.id, proto, reply: r.reply.slice(0, 60) }));
    } catch (e: any) {
      toast.error(t("llmSettings.testFailed", { id: m.id, error: e.message }));
    } finally {
      setTesting(null);
    }
  };
  const shown = (fetched ?? []).filter((id) => !filter || id.toLowerCase().includes(filter.toLowerCase()));

  return (
    <div className="space-y-3 rounded-lg border bg-muted/30 p-3">
      {/* Phones: one column, each label pulled up against the field under it (a label column left the fields too narrow) */}
      <div className="grid grid-cols-[88px_minmax(0,1fr)] items-center gap-x-3 max-md:grid-cols-1 gap-y-2.5 max-md:[&>span.text-muted-foreground]:-mb-1.5 max-md:[&>span.text-muted-foreground]:pt-0">
        <span className="text-muted-foreground">{t("llmSettings.name")}</span>
        <Input
          value={d.name}
          onChange={(e) => set(isNew ? { name: e.target.value, id: slug(e.target.value, taken) } : { name: e.target.value })}
          onKeyDown={stop}
          className="h-8"
          placeholder={t("llmSettings.namePlaceholder")}
        />
        <span className="text-muted-foreground">{t("llmSettings.protocol")}</span>
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
        <span className="text-muted-foreground">{t("llmSettings.baseUrl")}</span>
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
              <SelectItem value="key">{t("llmSettings.keyDirect")}</SelectItem>
              <SelectItem value="env">{t("llmSettings.keyEnv")}</SelectItem>
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
              placeholder={t("llmSettings.keyPlaceholder", { example: PROTOCOLS[d.api].key })}
            />
          ) : (
            <Input
              value={d.apiKeyEnv}
              onChange={(e) => set({ apiKeyEnv: e.target.value })}
              onKeyDown={stop}
              className="h-8 font-mono text-[12px]"
              placeholder={t("llmSettings.envPlaceholder")}
            />
          )}
        </div>
        <span className="self-start pt-1.5 text-muted-foreground">{t("llmSettings.models")}</span>
        <div className="space-y-2">
          <div className="flex flex-wrap gap-1">
            {d.models.map((m) => (
              <span key={m.id} className="flex items-center gap-1 rounded-sm bg-card py-0.5 pr-0.5 pl-1.5 font-mono text-[12px] ring-1 ring-border">
                {m.id}
                <button
                  className="rounded-sm px-1 text-muted-foreground hover:bg-muted hover:text-foreground"
                  title={t("llmSettings.testTitle")}
                  onClick={() => test(m)}
                >
                  {testing === m.id ? <Loader2 className="size-3 animate-spin" /> : t("llmSettings.test")}
                </button>
                <button
                  className="rounded-sm p-0.5 text-muted-foreground hover:bg-muted hover:text-foreground"
                  title={t("llmSettings.removeModel")}
                  onClick={() => set({ models: d.models.filter((x) => x.id !== m.id) })}
                >
                  <X className="size-3" />
                </button>
              </span>
            ))}
            {!d.models.length && <span className="text-[12px] text-muted-foreground">{t("llmSettings.modelsEmpty")}</span>}
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
              placeholder={t("llmSettings.modelPlaceholder", { example: PROTOCOLS[d.api].model })}
            />
            <Button size="sm" variant="outline" className="shrink-0" onClick={fetchModels} disabled={busy}>
              {busy ? <Loader2 className="size-3.5 animate-spin" /> : <Download className="size-3.5" />} {t("llmSettings.fetch")}
            </Button>
          </div>
          {fetched && fetched.length > 0 && (
            <div className="space-y-1.5 rounded-md border bg-card p-2">
              <Input
                value={filter}
                onChange={(e) => setFilter(e.target.value)}
                onKeyDown={stop}
                className="h-7 text-[12px]"
                placeholder={t("llmSettings.filter", { n: fetched.length })}
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
        {rich(t(d.api === "anthropic" ? "llmSettings.anthropicHint" : "llmSettings.openaiHint"), {
          api: <code className="font-mono">api</code>,
          efforts: <code className="font-mono">efforts</code>,
          window: <code className="font-mono">window</code>,
          maxOutput: <code className="font-mono">maxOutput</code>,
        })}
      </div>
      <div className="flex justify-end gap-2">
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

// A message with {name} placeholders filled by elements (a <code> in the middle of a sentence).
function rich(text: string, parts: Record<string, React.ReactNode>): React.ReactNode[] {
  return text.split(/\{(\w+)\}/).map((p, i) => (i % 2 ? <span key={i}>{parts[p]}</span> : p));
}
