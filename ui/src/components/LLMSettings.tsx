import { Brain, Check, Download, Image as ImageIcon, Loader2, Pencil, Plus, X } from "lucide-react";
import { useEffect, useState } from "react";
import { toast } from "sonner";
import { AgentIcon } from "@/components/AgentIcon";
import { ConfirmDelete } from "@/components/ConfirmDelete";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Textarea } from "@/components/ui/textarea";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { api } from "@/lib/api";
import { t } from "@/lib/i18n";
import { setConfig, useStore, type LLMConfig, type LLMModel, type LLMProvider } from "@/lib/store";

// 设置 → Agent 里「引擎」那块的 Native 服务商：native 引擎直接调的模型接口。用 native 引擎的 agent 可以只挑几家（AgentDef.providers）。
// 存进 config.json 的 llm（后端 native/config.go 读，每轮现取：改了下一轮就用新的）。配了至少一个模型，新建会话时才出现 Gerdr Agent。
// The protocol is picked per provider (a gateway can't be recognized by its URL): OpenAI-compatible, Anthropic or Gemini.
// On OpenAI-compatible ones the backend guesses per model id (GPT → Responses, others → Chat Completions); thinking
// levels and context window are guessed too. Wrong guesses are fixed in config.json (api / efforts / window on the model).

const stop = (e: React.KeyboardEvent) => e.stopPropagation();
const EMPTY: LLMConfig = {}; // 选择器里别 `?? {}`：每次一个新对象，zustand 认成变了，无限重渲染

type Protocol = "openai" | "anthropic" | "gemini";
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
  gemini: { label: "Gemini", baseUrl: "https://generativelanguage.googleapis.com/v1beta", key: "AIza…", model: "gemini-3-pro-preview" },
};

// The badge and the test result's name for each protocol a provider / model can speak.
const PROTOCOL_NAMES: Record<string, string> = {
  anthropic: "Anthropic Messages",
  gemini: "Gemini generateContent",
  responses: "Responses",
  chat: "Chat Completions",
};
const HINTS = { openai: "llmSettings.openaiHint", anthropic: "llmSettings.anthropicHint", gemini: "llmSettings.geminiHint" } as const;

const toDraft = (p: LLMProvider): Draft => ({
  id: p.id,
  name: p.name ?? "",
  api: p.api === "anthropic" || p.api === "gemini" ? p.api : "openai",
  baseUrl: p.baseUrl,
  keyMode: p.apiKeyEnv && !p.apiKey ? "env" : "key",
  apiKey: p.apiKey ?? "",
  apiKeyEnv: p.apiKeyEnv ?? "",
  models: p.models ?? [],
});

function fromDraft(d: Draft): LLMProvider {
  if (!d.baseUrl.trim()) throw new Error(t(d.api === "openai" ? "llmSettings.needBaseUrlV1" : "llmSettings.needBaseUrl"));
  if (!/^https?:\/\//.test(d.baseUrl.trim())) throw new Error(t("llmSettings.badBaseUrl"));
  if (!d.id.trim()) throw new Error(t("llmSettings.needName"));
  const p: LLMProvider = {
    id: d.id,
    name: d.name.trim() || undefined,
    api: d.api === "openai" ? undefined : d.api,
    baseUrl: d.baseUrl.trim().replace(/\/+$/, ""),
    models: d.models,
  };
  if (d.keyMode === "key") p.apiKey = d.apiKey.trim() || undefined;
  else p.apiKeyEnv = d.apiKeyEnv.trim() || undefined;
  return p;
}

// Image generation models, guessed from the id like the backend does (native/image.go imageModelID): Nano Banana
// (gemini-…-image), gpt-image, dall-e. Not Imagen (Google's :predict API, unsupported). A model's kind overrides it.
function imageId(id: string) {
  const s = id.toLowerCase().split("/").pop() ?? "";
  if (s.includes("imagen")) return false;
  return s.includes("-image") || s.startsWith("gpt-image") || s.startsWith("dall-e") || s.includes("nano-banana");
}
const isImageModel = (m: LLMModel) => (m.kind ? m.kind === "image" : imageId(m.id));

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
  const chatProviders = providers.filter((p) => p.models.some((m) => !isImageModel(m))).length;

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
        {/* Counted like the backend's Available(): only providers with a chat model make Gerdr Agent usable, so one
            holding just image models doesn't read as "configured" while Gerdr Agent is missing from new sessions */}
        {chatProviders > 0 ? (
          <span className="ml-auto shrink-0 text-[12px] text-ok">
            {t(chatProviders === 1 ? "llmSettings.configuredOne" : "llmSettings.configured", { n: chatProviders })}
          </span>
        ) : (
          <span className="ml-auto shrink-0 text-[12px] text-bad">{t(providers.length ? "llmSettings.noChatModels" : "llmSettings.noProviders")}</span>
        )}
      </div>
      <div className="text-[12px] text-muted-foreground">{t("llmSettings.hint")}</div>

      {providers.map((p) => (
        <div key={p.id} className="space-y-1.5 rounded-md bg-muted/40 px-3 py-2">
          <div className="flex items-center gap-2">
            {/* Capped and truncated rather than wrapped: the URL next to it shrinks first */}
            <span className="max-w-[50%] shrink-0 truncate font-medium">{p.name || p.id}</span>
            {p.api && <span className="shrink-0 rounded-sm bg-muted px-1 text-[11px] text-muted-foreground">{PROTOCOLS[p.api].label}</span>}
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
                <span key={m.id} className="flex items-center gap-1 rounded-sm bg-muted px-1.5 py-0.5 font-mono text-[12px]">
                  {isImageModel(m) && <ImageIcon className="size-3 text-muted-foreground" />}
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
  const [levels, setLevels] = useState<string | null>(null); // the model whose thinking levels are open
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
      const proto = PROTOCOL_NAMES[r.api] ?? PROTOCOL_NAMES.chat;
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
                  className="flex items-center gap-0.5 rounded-sm px-1 text-muted-foreground hover:bg-muted hover:text-foreground data-[on=true]:text-brand"
                  data-on={isImageModel(m)}
                  title={t("llmSettings.imageToggle")}
                  onClick={() => {
                    const kind = isImageModel(m) ? "chat" : "image";
                    // Stored only when it differs from the guess, so config.json stays just the id
                    const next = { ...m, kind: kind === (imageId(m.id) ? "image" : "chat") ? undefined : kind } as LLMModel;
                    if (!next.kind) delete next.kind;
                    set({ models: d.models.map((x) => (x.id === m.id ? next : x)) });
                  }}
                >
                  <ImageIcon className="size-3" />
                  {isImageModel(m) && <span className="font-sans">{t("llmSettings.imageTag")}</span>}
                </button>
                {!isImageModel(m) && (
                  <button
                    className="flex items-center gap-0.5 rounded-sm px-1 text-muted-foreground hover:bg-muted hover:text-foreground data-[on=true]:bg-muted data-[on=true]:text-foreground data-[own=true]:text-brand"
                    data-on={levels === m.id}
                    data-own={!!m.efforts?.length}
                    title={t("llmSettings.levelsTitle")}
                    onClick={() => setLevels(levels === m.id ? null : m.id)}
                  >
                    <Brain className="size-3" />
                    {m.efforts?.length ? <span className="font-sans">{m.efforts.length}</span> : null}
                  </button>
                )}
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
          {(() => {
            const m = d.models.find((x) => x.id === levels);
            const p = m && draftProvider();
            return m && p ? (
              <LevelEditor
                key={m.id}
                provider={p}
                model={m}
                onChange={(next) => set({ models: d.models.map((x) => (x.id === m.id ? next : x)) })}
                onClose={() => setLevels(null)}
              />
            ) : null;
          })()}
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
        {rich(t(HINTS[d.api]), {
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

type ImageRow = { key: string; provider: LLMProvider; model: LLMModel };

/** The image models in config order, and the one generation uses without --model: llm.imageModel, else the first on
 *  a Gemini provider, then on an OpenAI-compatible one (backend native/image.go resolveImage). Anthropic can't make images. */
function imageRows(llm: LLMConfig): { rows: ImageRow[]; current?: string } {
  const rows: ImageRow[] = [];
  for (const p of llm.providers ?? []) {
    if (p.api === "anthropic") continue;
    for (const m of p.models ?? []) if (isImageModel(m)) rows.push({ key: `${p.id}/${m.id}`, provider: p, model: m });
  }
  const current = llm.imageModel || (rows.find((r) => r.provider.api === "gemini") ?? rows.find((r) => !r.provider.api))?.key;
  return { rows, current };
}

// The protocols an image model can be on, with the model a new one starts from
const IMAGE_PROTOCOLS = { gemini: "gemini-2.5-flash-image", openai: "gpt-image-1" } as const;
type ImageProtocol = keyof typeof IMAGE_PROTOCOLS;
const NEW = "__new";

/**
 * Settings → Agent, under the engines: the model that makes images for every agent (`gerdr image gen`, Gerdr Agent's
 * generate_image). Its own section, not inside the Native card, because Claude Code / Codex use it too. The data
 * stays in one place: an image model is a model of kind image on one of llm.providers (keys and URLs only there),
 * the default is llm.imageModel. Adding one to a provider here is the same as adding it in the provider's editor.
 */
export function ImageModels() {
  const llm = useStore((s) => s.config.llm ?? EMPTY);
  const providers = (llm.providers ?? []).filter((p) => p.api !== "anthropic");
  const { rows, current } = imageRows(llm);
  const [adding, setAdding] = useState(false);
  const [editing, setEditing] = useState<string | null>(null);
  const [trying, setTrying] = useState<string | null>(null);
  const save = (next: LLMConfig) => setConfig({ llm: next });
  const remove = (r: ImageRow) =>
    save({
      ...llm,
      imageModel: llm.imageModel === r.key ? undefined : llm.imageModel,
      providers: (llm.providers ?? []).map((p) => (p.id === r.provider.id ? { ...p, models: p.models.filter((m) => m.id !== r.model.id) } : p)),
    });
  const code = (x: string) => <code className="font-mono">{x}</code>;

  return (
    <div className="space-y-3 border-t pt-4">
      <div>
        <div className="font-medium">{t("llmSettings.imageModel")}</div>
        <div className="text-[12px] text-muted-foreground">{rich(t("llmSettings.imageHint"), { cmd: code("gerdr image gen") })}</div>
      </div>
      {rows.length > 0 && (
        <div className="space-y-1.5">
          {rows.map((r) => {
            const isDefault = r.key === current;
            const pinned = r.key === llm.imageModel;
            if (editing === r.key) return <ImageAdder key={r.key} providers={providers} llm={llm} edit={r} onDone={() => setEditing(null)} />;
            return (
              <div key={r.key} className="rounded-md border text-[13px]">
                <div className="flex flex-wrap items-center gap-x-2 gap-y-0.5 px-3 py-2">
                  <ImageIcon className="size-4 shrink-0 text-muted-foreground" />
                  <span className="min-w-0 truncate font-mono text-[12px]">{r.model.id}</span>
                  <span className="shrink-0 text-[12px] text-muted-foreground">
                    {t("llmSettings.imageFrom", { provider: r.provider.name || r.provider.id })}
                  </span>
                  <span className="shrink-0 rounded-sm bg-muted px-1 text-[11px] text-muted-foreground">
                    {r.provider.api === "gemini" ? "Gemini" : "OpenAI"}
                  </span>
                  {isDefault && (
                    <span className="shrink-0 rounded-sm bg-brand/10 px-1 text-[11px] text-brand">
                      {t(pinned ? "llmSettings.imageDefault" : "llmSettings.imageDefaultAuto")}
                    </span>
                  )}
                  <span className="flex-1" />
                  {/* Words, not a star: a star read as "favourite" and nobody could tell what it did. Pinned → back to automatic */}
                  {(!isDefault || pinned) && (
                    <Button
                      size="sm"
                      variant="ghost"
                      className="h-7 px-2"
                      title={t(pinned ? "llmSettings.imageUnpinTitle" : "llmSettings.imagePinTitle")}
                      onClick={() => save({ ...llm, imageModel: pinned ? undefined : r.key })}
                    >
                      {t(pinned ? "llmSettings.imageUnpin" : "llmSettings.imagePin")}
                    </Button>
                  )}
                  <Button
                    size="sm"
                    variant="ghost"
                    className="h-7 px-2 data-[on=true]:bg-muted"
                    data-on={trying === r.key}
                    onClick={() => setTrying(trying === r.key ? null : r.key)}
                    title={t("llmSettings.imageTestTitle")}
                  >
                    {t("llmSettings.imageTry")}
                  </Button>
                  <Button size="icon" variant="ghost" className="size-7" title={t("common.edit")} onClick={() => setEditing(r.key)}>
                    <Pencil className="size-3.5" />
                  </Button>
                  <ConfirmDelete title={t("llmSettings.removeModel")} onConfirm={() => remove(r)} />
                </div>
                {trying === r.key && <ImageTry modelKey={r.key} onClose={() => setTrying(null)} />}
              </div>
            );
          })}
        </div>
      )}
      {!rows.length && !adding && (
        <div className="text-[12px] text-muted-foreground">
          {rich(t("llmSettings.imageNone"), { gemini: code(IMAGE_PROTOCOLS.gemini), openai: code(IMAGE_PROTOCOLS.openai) })}
        </div>
      )}
      {adding ? (
        <ImageAdder providers={providers} llm={llm} onDone={() => setAdding(false)} />
      ) : (
        <Button size="sm" variant="outline" onClick={() => setAdding(true)}>
          <Plus className="size-3.5" /> {t("llmSettings.imageAdd")}
        </Button>
      )}
    </div>
  );
}

// The levels people pick from, low to high; a value not here (a number for a thinking budget, a gateway's own
// name) keeps its place after them
const LEVELS = ["off", "none", "minimal", "low", "medium", "high", "xhigh", "max"];
const byLevel = (a: string, b: string) => {
  const i = LEVELS.indexOf(a),
    j = LEVELS.indexOf(b);
  return (i < 0 ? 99 : i) - (j < 0 ? 99 : j);
};
type LevelInfo = { efforts: string[] | null; default: string; source: "config" | "list" | "guess" | ""; listed?: string[]; guessed?: string[] };

/**
 * A model's thinking levels: what it gets now and from where (its own config, the provider's model list, the guess
 * from its id — /api/llm/efforts), and editing them. Any edit makes them the model's own (efforts / defaultEffort in
 * config.json, saved with the provider); "Back to automatic" drops them again. Values go to the API as written.
 */
function LevelEditor({ provider, model, onChange, onClose }: { provider: LLMProvider; model: LLMModel; onChange: (m: LLMModel) => void; onClose: () => void }) {
  const [info, setInfo] = useState<LevelInfo | null>(null);
  const [err, setErr] = useState("");
  const [custom, setCustom] = useState("");
  useEffect(() => {
    // Asked without the model's own levels: the page shows what automatic would be next to them
    const { efforts: _e, defaultEffort: _d, ...bare } = model;
    api
      .llmEfforts(provider, bare)
      .then(setInfo)
      .catch((e) => setErr(e.message));
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);
  const own = !!model.efforts?.length;
  const list = own ? model.efforts! : (info?.efforts ?? []);
  const def = (own ? model.defaultEffort : info?.default) ?? "";
  const write = (efforts: string[], d: string) => {
    const next: LLMModel = { ...model, efforts: [...efforts].sort(byLevel), defaultEffort: efforts.includes(d) ? d : undefined };
    if (!next.defaultEffort) delete next.defaultEffort;
    onChange(next);
  };
  const auto = () => {
    const next = { ...model };
    delete next.efforts;
    delete next.defaultEffort;
    onChange(next);
  };
  const add = (v: string) => {
    v = v.trim();
    if (v && !list.includes(v)) write([...list, v], def);
  };
  const source = own ? "config" : (info?.source ?? "");
  const suggestions = [...new Set([...LEVELS, ...(info?.listed ?? []), ...(info?.guessed ?? [])])].filter((v) => !list.includes(v)).sort(byLevel);
  return (
    <div className="space-y-2 rounded-md border bg-card p-2.5 text-[12px]">
      <div className="flex items-center gap-2">
        <Brain className="size-3.5 text-muted-foreground" />
        <span className="font-medium">{t("llmSettings.levels", { id: model.id })}</span>
        {info || own ? (
          <span className="rounded-sm bg-muted px-1 text-[11px] text-muted-foreground">{t(`llmSettings.levelsFrom.${source || "none"}`)}</span>
        ) : (
          !err && <Loader2 className="size-3 animate-spin text-muted-foreground" />
        )}
        <span className="flex-1" />
        {own && (
          <Button size="sm" variant="ghost" className="h-6 px-2 text-[12px]" onClick={auto} title={t("llmSettings.levelsAutoTitle")}>
            {t("llmSettings.levelsAuto")}
          </Button>
        )}
        <Button size="icon" variant="ghost" className="size-6" onClick={onClose} title={t("common.close")}>
          <X className="size-3.5" />
        </Button>
      </div>
      {err && <div className="text-bad">{err}</div>}
      <div className="flex flex-wrap items-center gap-1">
        {list.map((v) => (
          <span
            key={v}
            className="flex items-center gap-1 rounded-sm py-0.5 pr-0.5 pl-1.5 font-mono ring-1 ring-border data-[on=true]:bg-brand/10 data-[on=true]:text-brand data-[on=true]:ring-brand/40"
            data-on={v === def}
          >
            <button title={t("llmSettings.levelsMakeDefault")} onClick={() => write(list, v)}>
              {v}
              {v === def && <span className="ml-1 font-sans text-[11px]">{t("llmSettings.imageDefault")}</span>}
            </button>
            <button
              className="rounded-sm p-0.5 text-muted-foreground hover:bg-muted hover:text-foreground"
              title={t("common.delete")}
              onClick={() =>
                write(
                  list.filter((x) => x !== v),
                  def,
                )
              }
            >
              <X className="size-3" />
            </button>
          </span>
        ))}
        {!list.length && (info || own) && <span className="text-muted-foreground">{t("llmSettings.levelsNone")}</span>}
      </div>
      {suggestions.length > 0 && (
        <div className="flex flex-wrap items-center gap-1">
          {suggestions.map((v) => (
            <button key={v} className="rounded-sm px-1.5 py-0.5 font-mono text-muted-foreground hover:bg-muted hover:text-foreground" onClick={() => add(v)}>
              + {v}
            </button>
          ))}
          <Input
            value={custom}
            onChange={(e) => setCustom(e.target.value)}
            onKeyDown={(e) => {
              stop(e);
              if (e.key === "Enter" && !e.nativeEvent.isComposing) {
                add(custom);
                setCustom("");
              }
            }}
            className="h-6 w-36 font-mono text-[12px]"
            placeholder={t("llmSettings.levelsCustom")}
          />
        </div>
      )}
      <div className="text-muted-foreground">{t("llmSettings.levelsHint")}</div>
    </div>
  );
}

/**
 * Trying an image model for real: a prompt (prefilled, editable), Generate, and the image itself shown here. Goes
 * through /api/image like `gerdr image gen`, so it tests what the agents will get. Each try costs one generation,
 * hence nothing runs until Generate is pressed. Errors stay in the panel (the provider's own words), not a toast.
 */
function ImageTry({ modelKey, onClose }: { modelKey: string; onClose: () => void }) {
  const [prompt, setPrompt] = useState(() => t("llmSettings.imageTryPrompt"));
  const [busy, setBusy] = useState(false);
  const [secs, setSecs] = useState(0);
  const [res, setRes] = useState<{ src: string; text: string; ms: number } | null>(null);
  const [err, setErr] = useState("");
  const run = async () => {
    if (!prompt.trim() || busy) return;
    setBusy(true);
    setErr("");
    setSecs(0);
    const start = Date.now();
    const tick = setInterval(() => setSecs(Math.round((Date.now() - start) / 1000)), 1000);
    try {
      const r = await api.imageGen({ prompt, model: modelKey });
      const im = r.images[0];
      setRes({ src: `data:${im.mime};base64,${im.data}`, text: r.text, ms: Date.now() - start });
    } catch (e: any) {
      setErr(e.message);
    } finally {
      clearInterval(tick);
      setBusy(false);
    }
  };
  return (
    <div className="space-y-2 border-t px-3 py-2.5">
      <Textarea
        value={prompt}
        onChange={(e) => setPrompt(e.target.value)}
        onKeyDown={(e) => {
          stop(e);
          if (e.key === "Enter" && (e.metaKey || e.ctrlKey)) run();
        }}
        rows={2}
        className="min-h-0 resize-y text-[13px]"
        placeholder={t("llmSettings.imageTryPlaceholder")}
      />
      <div className="flex items-center gap-2">
        <span className="min-w-0 flex-1 text-[12px] text-muted-foreground">
          {busy ? t("llmSettings.imageTryBusy", { n: secs }) : t("llmSettings.imageTryCost")}
        </span>
        <Button size="sm" variant="ghost" onClick={onClose}>
          {t("common.cancel")}
        </Button>
        <Button size="sm" onClick={run} disabled={busy || !prompt.trim()}>
          {busy && <Loader2 className="size-3.5 animate-spin" />}
          {t(res ? "llmSettings.imageTryAgain" : "llmSettings.imageTryGo")}
        </Button>
      </div>
      {err && <div className="text-[12px] break-words text-bad">{err}</div>}
      {res && (
        <div className="space-y-1">
          <img src={res.src} alt={prompt} className="max-h-80 max-w-full rounded-md border" />
          <div className="text-[12px] text-muted-foreground">
            {t("llmSettings.imageTryDone", { s: (res.ms / 1000).toFixed(1) })}
            {res.text && ` · ${res.text}`}
          </div>
        </div>
      )}
    </div>
  );
}

/** Adding an image model: to a provider there already is, or with a new provider (the only way when there is none
 *  yet, e.g. someone on Claude Code only) — then the provider is saved with just this model. With edit, the same form
 *  changes one: its id, or which provider it is on (moved there, the default following it). */
function ImageAdder({ providers, llm, edit, onDone }: { providers: LLMProvider[]; llm: LLMConfig; edit?: ImageRow; onDone: () => void }) {
  const [target, setTarget] = useState(edit?.provider.id ?? providers[0]?.id ?? NEW);
  const firstProto: ImageProtocol = providers[0]?.api === "gemini" || !providers[0] ? "gemini" : "openai";
  const [model, setModel] = useState<string>(edit?.model.id ?? IMAGE_PROTOCOLS[firstProto]);
  const [proto, setProto] = useState<ImageProtocol>("gemini");
  const [name, setName] = useState("");
  const [baseUrl, setBaseUrl] = useState("");
  const [key, setKey] = useState("");
  const [fetched, setFetched] = useState<string[] | null>(null);
  const [busy, setBusy] = useState(false);
  const existing = providers.find((p) => p.id === target);
  // A provider picked earlier may be gone (deleted meanwhile): then it is a new one, as when there were none
  const isNew = !existing;

  const pickTarget = (v: string) => {
    setTarget(v);
    setFetched(null);
    if (edit) return; // keep the id being edited
    const p = providers.find((x) => x.id === v);
    setModel(IMAGE_PROTOCOLS[v === NEW ? proto : p?.api === "gemini" ? "gemini" : "openai"]);
  };
  const newProvider = (): LLMProvider => {
    const taken = new Set((llm.providers ?? []).map((p) => p.id));
    const label = name.trim() || (proto === "gemini" ? "Gemini" : "OpenAI");
    return {
      id: slug(label, taken),
      name: label,
      api: proto === "gemini" ? "gemini" : undefined,
      baseUrl: (baseUrl.trim() || PROTOCOLS[proto].baseUrl).replace(/\/+$/, ""),
      apiKey: key.trim() || undefined,
      models: [],
    };
  };
  // Marked image only when the id doesn't say so already: config.json stays just the id
  const entry = (id: string): LLMModel => (imageId(id) ? { id } : { id, kind: "image" });

  const fetchModels = async () => {
    const p = isNew ? newProvider() : existing;
    if (!p) return;
    setBusy(true);
    try {
      const r = await api.llmModels(p);
      // Image models first: a gateway lists dozens of chat models
      setFetched([...r.models.filter(imageId), ...r.models.filter((x) => !imageId(x))]);
    } catch (e: any) {
      toast.error(t("llmSettings.fetchFailed", { error: e.message }));
    } finally {
      setBusy(false);
    }
  };
  const onSave = () => {
    const id = model.trim();
    if (!id) return toast.error(t("llmSettings.imageNeedModel"));
    let list = llm.providers ?? [];
    let imageModel = llm.imageModel;
    if (edit) {
      // Take it off where it was (keeping its other settings), then put it where it goes now
      const old = edit.model;
      list = list.map((p) => (p.id === edit.provider.id ? { ...p, models: p.models.filter((m) => m.id !== old.id) } : p));
      const p = list.find((x) => x.id === target);
      if (!p) return;
      const moved: LLMModel = { ...old, id, kind: imageId(id) ? undefined : "image" };
      if (!moved.kind) delete moved.kind;
      const models = p.models.some((m) => m.id === id) ? p.models.map((m) => (m.id === id ? moved : m)) : [...p.models, moved];
      if (imageModel === edit.key) imageModel = `${p.id}/${id}`;
      setConfig({ llm: { ...llm, imageModel, providers: list.map((x) => (x.id === p.id ? { ...x, models } : x)) } });
      toast.success(t("llmSettings.savedToast"));
      return onDone();
    }
    if (isNew) {
      const p = newProvider();
      if (!/^https?:\/\//.test(p.baseUrl)) return toast.error(t("llmSettings.badBaseUrl"));
      setConfig({ llm: { ...llm, providers: [...list, { ...p, models: [entry(id)] }] } });
    } else if (existing) {
      const has = existing.models.find((m) => m.id === id);
      const models = has ? existing.models.map((m) => (m.id === id ? { ...m, kind: "image" as const } : m)) : [...existing.models, entry(id)];
      setConfig({ llm: { ...llm, providers: list.map((p) => (p.id === existing.id ? { ...p, models } : p)) } });
    }
    toast.success(t("llmSettings.imageAdded", { id }));
    onDone();
  };

  return (
    <div className="space-y-2.5 rounded-lg border bg-muted/30 p-3 text-[13px]">
      <div className="grid grid-cols-[88px_minmax(0,1fr)] items-center gap-x-3 gap-y-2.5 max-md:grid-cols-1">
        {providers.length > 0 && (
          <>
            <span className="text-muted-foreground">{t("llmSettings.imageProvider")}</span>
            <Select value={target} onValueChange={pickTarget}>
              <SelectTrigger size="sm" className="w-auto max-w-full min-w-48 max-md:w-full">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {providers.map((p) => (
                  <SelectItem key={p.id} value={p.id}>
                    {p.name || p.id}
                  </SelectItem>
                ))}
                {!edit && <SelectItem value={NEW}>{t("llmSettings.imageNewProvider")}</SelectItem>}
              </SelectContent>
            </Select>
          </>
        )}
        {isNew && (
          <>
            <span className="text-muted-foreground">{t("llmSettings.protocol")}</span>
            <Select
              value={proto}
              onValueChange={(v) => {
                setProto(v as ImageProtocol);
                setModel(IMAGE_PROTOCOLS[v as ImageProtocol]);
              }}
            >
              <SelectTrigger size="sm" className="w-40 max-w-full max-md:w-full">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="gemini">Gemini</SelectItem>
                <SelectItem value="openai">{PROTOCOLS.openai.label}</SelectItem>
              </SelectContent>
            </Select>
            <span className="text-muted-foreground">{t("llmSettings.name")}</span>
            <Input
              value={name}
              onChange={(e) => setName(e.target.value)}
              onKeyDown={stop}
              className="h-8"
              placeholder={proto === "gemini" ? "Gemini" : "OpenAI"}
            />
            <span className="text-muted-foreground">{t("llmSettings.baseUrl")}</span>
            <Input
              value={baseUrl}
              onChange={(e) => setBaseUrl(e.target.value)}
              onKeyDown={stop}
              className="h-8 font-mono text-[12px]"
              placeholder={PROTOCOLS[proto].baseUrl}
            />
            <span className="text-muted-foreground">API key</span>
            <Input
              type="password"
              autoComplete="off"
              value={key}
              onChange={(e) => setKey(e.target.value)}
              onKeyDown={stop}
              className="h-8 font-mono text-[12px]"
              placeholder={t("llmSettings.keyPlaceholder", { example: PROTOCOLS[proto].key })}
            />
          </>
        )}
        <span className="text-muted-foreground">{t("llmSettings.imageModelId")}</span>
        <div className="flex items-center gap-2">
          <Input value={model} onChange={(e) => setModel(e.target.value)} onKeyDown={stop} className="h-8 font-mono text-[12px]" />
          <Button size="sm" variant="outline" className="shrink-0" onClick={fetchModels} disabled={busy}>
            {busy ? <Loader2 className="size-3.5 animate-spin" /> : <Download className="size-3.5" />} {t("llmSettings.fetch")}
          </Button>
        </div>
      </div>
      {fetched && fetched.length > 0 && (
        <div className="flex max-h-32 flex-wrap gap-1 overflow-auto rounded-md border bg-card p-2">
          {fetched.map((id) => (
            <button
              key={id}
              className="rounded-sm px-1.5 py-0.5 font-mono text-[12px] hover:bg-muted data-[on=true]:bg-brand/10 data-[on=true]:text-brand"
              data-on={id === model}
              onClick={() => setModel(id)}
            >
              {id}
            </button>
          ))}
        </div>
      )}
      <div className="text-[12px] text-muted-foreground">
        {t(edit ? "llmSettings.imageEditHint" : isNew ? "llmSettings.imageAddNewHint" : "llmSettings.imageAddHint")}
      </div>
      <div className="flex justify-end gap-2">
        <Button size="sm" variant="ghost" onClick={onDone}>
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
