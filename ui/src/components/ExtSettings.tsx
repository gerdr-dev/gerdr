import { Download, Info, KeyRound, LogOut, MoreHorizontal, Pencil, Plus, Power, RefreshCw, RotateCw, Trash2 } from "lucide-react";
import { useCallback, useEffect, useState } from "react";
import { toast } from "sonner";
import { Button } from "@/components/ui/button";
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuSeparator, DropdownMenuTrigger } from "@/components/ui/dropdown-menu";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover";
import { Textarea } from "@/components/ui/textarea";
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { api, type MCPAuth } from "@/lib/api";
import { t } from "@/lib/i18n";
import type { MessageKey } from "@/lib/messages";
import { isNative, nativeOpenURL } from "@/lib/native";
import { useStore } from "@/lib/store";
import type { ExtSettings, ExtSkill, ExtSource, ExtView, MCPServer } from "@/lib/types";

// 设置 →「Skill」「MCP」两页。gerdr 自己那份在这里增删改；agent 自己的只读，能导入。

const SOURCES: { v: ExtSource; label: MessageKey }[] = [
  { v: "agent", label: "extSettings.source.agent" },
  { v: "both", label: "extSettings.source.both" },
  { v: "gerdr", label: "extSettings.source.gerdr" },
];

const stop = (e: React.KeyboardEvent) => e.stopPropagation();

function useExt(open: boolean) {
  const [v, setV] = useState<ExtView | null>(null);
  const version = useStore((s) => s.extVersion); // changed by `gerdr skill` / `gerdr mcp` or by hand
  useEffect(() => {
    if (open) api.ext().then(setV, (e) => toast.error(e.message));
  }, [open, version]);
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
      <SelectTrigger size="sm" className="w-40 text-[12px]">
        <SelectValue />
      </SelectTrigger>
      <SelectContent>
        {SOURCES.map((s) => (
          <SelectItem key={s.v} value={s.v} className="text-[12px]">
            {t(s.label)}
          </SelectItem>
        ))}
      </SelectContent>
    </Select>
  );
}

const Empty = ({ children }: { children: React.ReactNode }) => <div className="p-3 text-[12px] text-muted-foreground">{children}</div>;

// The Skill and MCP pages share one look: a header row (title + ⓘ, source, add), then groups of rows, each thing once.

// A popover, not a tooltip: tooltips only open on hover/focus, so on a phone tapping ⓘ showed nothing
function HelpTip({ lines }: { lines: string[] }) {
  return (
    <Popover>
      <PopoverTrigger asChild>
        <button className="text-muted-foreground hover:text-foreground" aria-label={t("extSettings.help")}>
          <Info className="size-3.5" />
        </button>
      </PopoverTrigger>
      <PopoverContent
        side="bottom"
        align="start"
        className="w-auto max-w-[min(24rem,calc(100vw-2rem))] space-y-1.5 px-3 py-2 text-[12px] leading-5 font-normal"
      >
        {lines.map((line) => (
          <p key={line}>{line}</p>
        ))}
      </PopoverContent>
    </Popover>
  );
}

function Group({ title, count, hint, children }: { title: string; count: number; hint?: string; children: React.ReactNode }) {
  return (
    <div className="space-y-1.5">
      <div className="flex items-baseline gap-2 px-0.5">
        <span className="shrink-0 text-[12px] font-medium whitespace-nowrap">{title}</span>
        <span className="shrink-0 text-[11px] text-muted-foreground tabular-nums">{count}</span>
        {hint && <span className="min-w-0 truncate text-[11px] text-muted-foreground">{hint}</span>}
      </div>
      <div className="divide-y rounded-md border">{children}</div>
    </div>
  );
}

/** The ⋯ menu at the end of a row. */
function RowMenu({ children }: { children: React.ReactNode }) {
  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <Button size="icon" variant="ghost" className="size-7 shrink-0" title={t("common.more")}>
          <MoreHorizontal className="size-3.5" />
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end">{children}</DropdownMenuContent>
    </DropdownMenu>
  );
}

const Tag = ({ children }: { children: React.ReactNode }) => (
  <span className="shrink-0 rounded-sm bg-muted px-1 text-[10px] text-muted-foreground">{children}</span>
);

// Settings text is 12px; shadcn's Input / Textarea / Select / Button default to 14px (Input / Textarea also set md:text-sm,
// which beats a bare text-[12px] on wide screens), so the editors state it on every control.
const TEXT12 = "text-[12px] md:text-[12px]";
const LABEL = "text-[12px] font-normal text-muted-foreground";

// ---------------- Skill ----------------

const TEMPLATE = (name: string) => `---
name: ${name}
description: ${t("extSettings.templateDescription")}
---

# ${name}

${t("extSettings.templateBody")}
`;

const skillHelp = () => [t("extSettings.skillHelp")];

type SkillRow = { key: string; sk: ExtSkill; group: "builtin" | "gerdr" | "claude" | "codex"; also: string[] };

function skillRows(v: ExtView): SkillRow[] {
  const base = (n: string) => n.split(":").pop()!;
  const rows: SkillRow[] = [
    ...v.builtin.map((sk) => ({ key: "b:" + sk.name, sk, group: "builtin" as const, also: [] })),
    ...v.skills.map((sk) => ({ key: "g:" + sk.name, sk, group: "gerdr" as const, also: [] as string[] })),
  ];
  const add = (sk: ExtSkill, group: "claude" | "codex", tag: string) => {
    const g = rows.find((r) => r.group === "gerdr" && r.sk.name === base(sk.name));
    if (g) return void (g.also.includes(tag) || g.also.push(tag)); // imported into Gerdr: one row, tagged
    rows.push({ key: group + ":" + sk.dir, sk, group, also: [] });
  };
  for (const sk of v.agent.claudeSkills) add(sk, "claude", "Claude");
  for (const sk of v.agent.codexSkills) add(sk, "codex", "Codex");
  return rows;
}

export function SkillSettings({ open }: { open: boolean }) {
  const { v, run, save } = useExt(open);
  const [edit, setEdit] = useState<{ name: string; content: string; isNew: boolean } | null>(null);
  if (!v) return <Empty>{t("common.loading")}</Empty>;
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

  if (edit)
    return (
      <SkillEditor
        edit={edit}
        onCancel={() => setEdit(null)}
        onSave={async (name, content) => {
          if (edit.isNew && mine.has(name)) {
            toast.error(t("extSettings.skillExists", { name }));
            return;
          }
          if (await run(api.writeSkill(name, content))) setEdit(null);
        }}
      />
    );

  const rows = skillRows(v);
  const groups = [
    { id: "builtin", title: t("extSettings.group.builtin"), hint: t("extSettings.group.builtinHint") },
    { id: "gerdr", title: "Gerdr", hint: t("extSettings.group.gerdrSkillHint") },
    { id: "claude", title: "Claude Code", hint: t("extSettings.group.claudeSkillHint") },
    { id: "codex", title: "Codex", hint: t("extSettings.group.codexSkillHint") },
  ]
    .map((g) => ({ ...g, rows: rows.filter((r) => r.group === g.id) }))
    .filter((g) => g.rows.length || g.id === "gerdr");

  return (
    <div className="min-w-0 space-y-3">
      {/* On a phone the source picker gets its own row (order-last + full width): in one row it pushed the buttons past
          the dialog's edge and squeezed the label to one character per line */}
      <div className="flex flex-wrap items-center gap-2">
        <div className="flex flex-1 items-center gap-1.5 text-[13px] font-medium">
          Skill
          <HelpTip lines={skillHelp()} />
        </div>
        <div className="flex items-center gap-2 max-md:order-last max-md:w-full max-md:justify-between">
          <span className="text-[12px] whitespace-nowrap text-muted-foreground">{t("extSettings.agentUses")}</span>
          <SourcePicker value={s.skillSource} onChange={(x) => save({ skillSource: x })} />
        </div>
        <Button size="sm" variant="outline" className="text-[12px]" onClick={() => setEdit({ name: "", content: "", isNew: true })}>
          <Plus className="size-3.5" />
          {t("extSettings.new")}
        </Button>
      </div>
      {groups.map((g) => (
        <Group key={g.id} title={g.title} count={g.rows.length} hint={g.hint}>
          {g.rows.length === 0 && <Empty>{t("extSettings.skillEmpty")}</Empty>}
          {g.rows.map((r) => {
            const sk = r.sk;
            const ours = r.group === "builtin" || r.group === "gerdr";
            const off = ours && !sk.enabled;
            const plugin = sk.origin.startsWith("claude-plugin:") ? sk.origin.slice("claude-plugin:".length) : "";
            const taken = mine.has(sk.name.split(":").pop()!);
            return (
              <div key={r.key} className="flex items-center gap-2.5 px-3 py-2">
                <span className={`size-2 shrink-0 rounded-full ${off ? "border border-muted-foreground/50" : "bg-ok"}`} />
                <div className="min-w-0 flex-1">
                  <div className="flex items-center gap-1.5 text-[13px]">
                    <span className="truncate">{sk.name}</span>
                    {plugin && <Tag>{t("extSettings.plugin", { name: plugin })}</Tag>}
                    {r.also.map((x) => (
                      <Tag key={x}>{t("extSettings.alsoIn", { name: x })}</Tag>
                    ))}
                  </div>
                  {sk.description && <div className="truncate text-[11px] text-muted-foreground">{sk.description}</div>}
                </div>
                {off && <span className="shrink-0 text-[11px] text-muted-foreground">{t("extSettings.disabled")}</span>}
                <RowMenu>
                  {r.group === "gerdr" && (
                    <DropdownMenuItem onClick={() => openEdit(sk.name)}>
                      <Pencil /> {t("common.edit")}
                    </DropdownMenuItem>
                  )}
                  {ours && (
                    <DropdownMenuItem onClick={() => toggle(sk.name, !sk.enabled)}>
                      <Power /> {sk.enabled ? t("common.disable") : t("common.enable")}
                    </DropdownMenuItem>
                  )}
                  {!ours && (
                    <DropdownMenuItem
                      disabled={taken}
                      onClick={() => run(api.importSkill(sk.dir)).then((ok) => ok && toast.success(t("extSettings.addedToGerdr", { name: sk.name })))}
                    >
                      <Download /> {taken ? t("extSettings.gerdrHasSameName") : t("extSettings.addToGerdr")}
                    </DropdownMenuItem>
                  )}
                  {r.group === "gerdr" && (
                    <>
                      <DropdownMenuSeparator />
                      <DropdownMenuItem
                        variant="destructive"
                        onClick={() => run(api.deleteSkill(sk.name)).then((ok) => ok && toast.success(t("extSettings.deleted", { name: sk.name })))}
                      >
                        <Trash2 /> {t("common.delete")}
                      </DropdownMenuItem>
                    </>
                  )}
                </RowMenu>
              </div>
            );
          })}
        </Group>
      ))}
      {s.skillSource === "gerdr" && <div className="text-[12px] text-muted-foreground">{t("extSettings.skillGerdrOnly")}</div>}
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
    <div className="min-w-0 space-y-3">
      <div className="text-[13px] font-medium">{edit.isNew ? t("extSettings.newSkill") : t("extSettings.editTitle", { name: edit.name })}</div>
      <div className="space-y-3 rounded-md border p-3">
        <div className="space-y-1.5">
          <Label className={LABEL}>{t("extSettings.name")}</Label>
          <Input
            className={`h-8 font-mono ${TEXT12}`}
            placeholder="my-skill"
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
          {name && !valid && <div className="text-[12px] text-bad">{t("extSettings.nameInvalid")}</div>}
        </div>
        <div className="space-y-1.5">
          <Label className={LABEL}>SKILL.md</Label>
          <Textarea
            className={`h-80 font-mono leading-relaxed ${TEXT12}`}
            value={content}
            placeholder={TEMPLATE("my-skill")}
            onChange={(e) => setContent(e.target.value)}
            onKeyDown={stop}
          />
        </div>
      </div>
      <div className="flex justify-end gap-2">
        <Button size="sm" variant="ghost" className="text-[12px]" onClick={onCancel}>
          {t("common.cancel")}
        </Button>
        <Button size="sm" className="text-[12px]" disabled={!valid} onClick={() => onSave(name, content)}>
          {t("common.save")}
        </Button>
      </div>
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

// Sign-in state of remote servers (OAuth). Polls while an authorization is waiting for the browser.
function useMCPAuth(open: boolean) {
  const [list, setList] = useState<MCPAuth[]>([]);
  const [busy, setBusy] = useState(false);
  const load = useCallback((refresh?: boolean) => {
    setBusy(true);
    return api
      .mcpAuth(refresh)
      .then(setList, (e) => toast.error(e.message))
      .finally(() => setBusy(false));
  }, []);
  useEffect(() => {
    if (open) load();
  }, [open, load]);
  const pending = list.some((a) => a.pending);
  useEffect(() => {
    if (!open || !pending) return;
    const timer = setInterval(() => api.mcpAuth().then(setList, () => {}), 2000);
    return () => clearInterval(timer);
  }, [open, pending]);
  const get = (m: MCPServer) => list.find((a) => a.name === m.name && a.url === m.url);
  const authorize = async (m: MCPServer) => {
    // In a browser the window has to open inside the click, or it gets blocked as a popup.
    const w = isNative() ? null : window.open("", "_blank");
    try {
      const { url } = await api.mcpAuthStart(m.name, m.url ?? "");
      if (w) w.location.href = url;
      else nativeOpenURL(url);
      setList((l) => l.map((a) => (a.name === m.name && a.url === m.url ? { ...a, pending: true } : a)));
    } catch (e: any) {
      w?.close();
      toast.error(`${m.name}: ${e.message}`);
    }
  };
  const logout = async (m: MCPServer) => {
    try {
      await api.mcpAuthLogout(m.name);
      await load(true);
    } catch (e: any) {
      toast.error(e.message);
    }
  };
  return { get, authorize, logout, reload: () => load(true), busy, any: list.length > 0 };
}

// One row per server, however many places configure it (gerdr's list, Claude Code's, Codex's): same name and address = same row.
type MCPRow = { key: string; server: MCPServer; gerdr: number; claude?: MCPServer; codex?: MCPServer };

function mcpRows(v: ExtView): MCPRow[] {
  const rows: MCPRow[] = [];
  const target = (m: MCPServer) => (isRemote(m) ? m.url : m.command) ?? "";
  const row = (m: MCPServer) => {
    const key = m.name + "\0" + target(m);
    let r = rows.find((x) => x.key === key);
    if (!r) rows.push((r = { key, server: m, gerdr: -1 }));
    return r;
  };
  v.settings.mcp.forEach((m, i) => (row(m).gerdr = i));
  for (const m of v.agent.claudeMCP) row(m).claude = m;
  for (const m of v.agent.codexMCP) row(m).codex = m;
  return rows;
}

const hostOf = (m: MCPServer) => {
  if (!isRemote(m)) return [m.command, ...(m.args ?? [])].join(" ");
  try {
    return new URL(m.url ?? "").host;
  } catch {
    return m.url ?? "";
  }
};

type Reconnect = { busy?: boolean; tools?: number; error?: string };

/** The status of one row: the last reconnect if there was one, else the remote sign-in check, else nothing known. */
function rowStatus(r: MCPRow, a: MCPAuth | undefined, rc: Reconnect | undefined, off: boolean) {
  if (off) return { dot: "border border-muted-foreground/50", text: t("extSettings.disabled"), cls: "text-muted-foreground" };
  if (rc?.error) return { dot: "bg-bad", text: t("extSettings.cantConnect"), cls: "text-bad", title: rc.error };
  if (rc?.tools !== undefined) return { dot: "bg-ok", text: t("extSettings.connected", { tools: toolCount(rc.tools) }), cls: "text-muted-foreground" };
  if (a?.state === "error") return { dot: "bg-bad", text: t("extSettings.cantConnect"), cls: "text-bad", title: a.error };
  if (a?.state === "needs_auth") return { dot: "bg-warn", text: a.pending ? t("extSettings.waitingAuth") : t("extSettings.signedOut"), cls: "text-warn" };
  if (a?.state === "ok")
    return {
      dot: "bg-ok",
      text: a.auth === "claude" ? t("extSettings.signedInClaude") : a.auth === "gerdr" ? t("extSettings.signedIn") : t("extSettings.available"),
      cls: "text-muted-foreground",
      title: a.auth === "claude" ? t("extSettings.claudeAuthTitle") : undefined,
    };
  return { dot: "bg-muted-foreground/40", text: isRemote(r.server) ? t("extSettings.checking") : "", cls: "text-muted-foreground" };
}

const toolCount = (n: number) => t(n === 1 ? "extSettings.toolsOne" : "extSettings.tools", { n });

const mcpHelp = () => [
  t("extSettings.mcpHelp1"),
  // Account-side connectors live in claude.ai / ChatGPT settings and Gerdr deliberately leaves them alone, so asking
  // an agent "what MCP do you have" lists more than this page does
  t("extSettings.mcpHelp2"),
];

export function MCPSettings({ open }: { open: boolean }) {
  const { v, save } = useExt(open);
  const auth = useMCPAuth(open);
  const [edit, setEdit] = useState<{ index: number; server: MCPServer } | null>(null);
  const [rc, setRc] = useState<Record<string, Reconnect>>({});
  if (!v) return <Empty>{t("common.loading")}</Empty>;
  const s = v.settings;
  const names = new Set(s.mcp.map((m) => m.name));
  const put = (list: MCPServer[]) => save({ mcp: list });
  const rows = mcpRows(v);
  // Grouped by the main place a server is configured in — gerdr's own list first — each server once
  const groups = [
    { tag: "Gerdr", title: "Gerdr", hint: t("extSettings.group.gerdrMcpHint"), rows: rows.filter((r) => r.gerdr >= 0) },
    { tag: "Claude", title: "Claude Code", hint: t("extSettings.group.claudeMcpHint"), rows: rows.filter((r) => r.gerdr < 0 && r.claude) },
    { tag: "Codex", title: "Codex", hint: t("extSettings.group.codexMcpHint"), rows: rows.filter((r) => r.gerdr < 0 && !r.claude && r.codex) },
  ].filter((g) => g.rows.length);
  const reconnect = async (r: MCPRow) => {
    const m = r.server;
    setRc((x) => ({ ...x, [r.key]: { busy: true } }));
    try {
      const res = await api.mcpReconnect(m.name, isRemote(m) ? (m.url ?? "") : "");
      setRc((x) => ({ ...x, [r.key]: res }));
      const more = res.agents ? t(res.agents === 1 ? "extSettings.reconnectMoreOne" : "extSettings.reconnectMore", { n: res.agents }) : "";
      if (res.error) toast.error(t("extSettings.reconnectFailed", { name: m.name, error: res.error }));
      else toast.success(t("extSettings.reconnected", { name: m.name, tools: toolCount(res.tools ?? 0), more }));
      if (isRemote(m)) auth.reload();
    } catch (e: any) {
      setRc((x) => ({ ...x, [r.key]: { error: e.message } }));
    }
  };

  if (edit)
    return (
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
    );

  return (
    <div className="min-w-0 space-y-3">
      <div className="flex flex-wrap items-center gap-2">
        <div className="flex flex-1 items-center gap-1.5 text-[13px] font-medium">
          MCP
          <HelpTip lines={mcpHelp()} />
        </div>
        <div className="flex items-center gap-2 max-md:order-last max-md:w-full max-md:justify-between">
          <span className="text-[12px] whitespace-nowrap text-muted-foreground">{t("extSettings.mcpUses")}</span>
          <SourcePicker value={s.mcpSource} onChange={(x) => save({ mcpSource: x })} />
        </div>
        <Button size="icon" variant="ghost" className="size-8" title={t("extSettings.recheck")} disabled={auth.busy} onClick={auth.reload}>
          <RefreshCw className={`size-3.5 ${auth.busy ? "animate-spin" : ""}`} />
        </Button>
        <Button size="sm" variant="outline" className="text-[12px]" onClick={() => setEdit({ index: -1, server: blankServer() })}>
          <Plus className="size-3.5" />
          {t("common.add")}
        </Button>
      </div>

      {rows.length === 0 && (
        <div className="rounded-md border">
          <Empty>{t("extSettings.mcpEmpty")}</Empty>
        </div>
      )}
      {groups.map((g) => (
        <Group key={g.tag} title={g.title} count={g.rows.length} hint={g.hint}>
          {g.rows.map((r) => {
            const m = r.server;
            const mine = r.gerdr >= 0 ? s.mcp[r.gerdr] : undefined;
            const off = mine ? !mine.enabled : !(r.claude?.enabled ?? r.codex?.enabled ?? true);
            const a = isRemote(m) ? auth.get(m) : undefined;
            const st = rowStatus(r, a, rc[r.key], off);
            const busy = rc[r.key]?.busy;
            // Tags: the other places that also configure it (the group already says the main one)
            const from = ([mine && "Gerdr", r.claude && "Claude", r.codex && "Codex"].filter(Boolean) as string[]).filter((f) => f !== g.tag);
            return (
              <div key={r.key} className="flex items-center gap-2.5 px-3 py-2">
                <span className={`size-2 shrink-0 rounded-full ${st.dot}`} />
                <div className="min-w-0 flex-1">
                  <div className="flex items-center gap-1.5 text-[13px]">
                    <span className="truncate">{m.name}</span>
                    {from.map((f) => (
                      <Tag key={f}>{t("extSettings.alsoIn", { name: f })}</Tag>
                    ))}
                  </div>
                  <div className="truncate font-mono text-[11px] text-muted-foreground" title={isRemote(m) ? m.url : hostOf(m)}>
                    {hostOf(m)}
                  </div>
                </div>
                {/* "未登录" next to the 授权 button says the same thing twice; on a phone the name needs that room */}
                {st.text &&
                  (st.title ? (
                    <Tooltip>
                      <TooltipTrigger asChild>
                        <span className={`shrink-0 cursor-default text-[11px] ${st.cls} ${a?.state === "needs_auth" && !off ? "max-md:hidden" : ""}`}>
                          {st.text}
                        </span>
                      </TooltipTrigger>
                      <TooltipContent className="max-w-sm text-[12px] leading-5">{st.title}</TooltipContent>
                    </Tooltip>
                  ) : (
                    <span className={`shrink-0 text-[11px] ${st.cls} ${a?.state === "needs_auth" && !off ? "max-md:hidden" : ""}`}>{st.text}</span>
                  ))}
                {a?.state === "needs_auth" && !off && (
                  <Button size="sm" variant="outline" className="h-7 shrink-0 text-[12px]" onClick={() => auth.authorize(m)}>
                    <KeyRound className="size-3.5" />
                    {a.pending ? t("extSettings.reopen") : t("extSettings.authorize")}
                  </Button>
                )}
                <Button
                  size="icon"
                  variant="ghost"
                  className="size-7 shrink-0"
                  title={t("extSettings.reconnect")}
                  disabled={busy || off}
                  onClick={() => reconnect(r)}
                >
                  <RotateCw className={`size-3.5 ${busy ? "animate-spin" : ""}`} />
                </Button>
                <RowMenu>
                  {mine ? (
                    <>
                      <DropdownMenuItem onClick={() => setEdit({ index: r.gerdr, server: mine })}>
                        <Pencil /> {t("common.edit")}
                      </DropdownMenuItem>
                      <DropdownMenuItem onClick={() => put(s.mcp.map((x, j) => (j === r.gerdr ? { ...x, enabled: !x.enabled } : x)))}>
                        <Power /> {mine.enabled ? t("common.disable") : t("common.enable")}
                      </DropdownMenuItem>
                    </>
                  ) : (
                    <DropdownMenuItem
                      disabled={names.has(m.name)}
                      onClick={() =>
                        put([...s.mcp, { ...m, type: isRemote(m) ? m.type || "http" : "stdio", enabled: true }])?.then(
                          (ok) => ok && toast.success(t("extSettings.addedToGerdr", { name: m.name })),
                        )
                      }
                    >
                      <Download /> {names.has(m.name) ? t("extSettings.gerdrHasSameName") : t("extSettings.addToGerdrNative")}
                    </DropdownMenuItem>
                  )}
                  {a?.hasOwn && (
                    <DropdownMenuItem onClick={() => auth.logout(m)}>
                      <LogOut /> {t("extSettings.logout")}
                    </DropdownMenuItem>
                  )}
                  {mine && (
                    <>
                      <DropdownMenuSeparator />
                      <DropdownMenuItem variant="destructive" onClick={() => put(s.mcp.filter((_, j) => j !== r.gerdr))}>
                        <Trash2 /> {t("extSettings.removeFromGerdr")}
                      </DropdownMenuItem>
                    </>
                  )}
                </RowMenu>
              </div>
            );
          })}
        </Group>
      ))}
      {s.mcpSource === "gerdr" && <div className="text-[12px] text-muted-foreground">{t("extSettings.mcpGerdrOnly")}</div>}
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
  const text = TEXT12;
  const field = `h-8 font-mono ${text}`;
  const area = `min-h-20 font-mono ${text}`;
  const label = LABEL;
  const save = () =>
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
    );
  return (
    <div className="min-w-0 space-y-3">
      <div className="text-[13px] font-medium">{server.name ? t("extSettings.editTitle", { name: server.name }) : t("extSettings.addMcp")}</div>
      <div className="space-y-3 rounded-md border p-3">
        <div className="flex gap-2">
          <div className="min-w-0 flex-1 space-y-1.5">
            <Label className={label}>{t("extSettings.name")}</Label>
            <Input className={field} placeholder="my-mcp" value={m.name} onChange={(e) => setM({ ...m, name: e.target.value })} onKeyDown={stop} autoFocus />
          </div>
          <div className="w-28 space-y-1.5">
            <Label className={label}>{t("extSettings.type")}</Label>
            <Select value={m.type || "stdio"} onValueChange={(x) => setM({ ...m, type: x as any })}>
              <SelectTrigger size="sm" className={`w-full ${text}`}>
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {["stdio", "http", "sse"].map((x) => (
                  <SelectItem key={x} value={x} className="text-[12px]">
                    {x}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </div>
        </div>
        {m.name && !nameOk && <div className="text-[12px] text-bad">{taken(m.name) ? t("extSettings.nameTaken") : t("extSettings.nameInvalid")}</div>}
        {remote ? (
          <>
            <div className="space-y-1.5">
              <Label className={label}>{t("extSettings.url")}</Label>
              <Input
                className={field}
                placeholder="https://example.com/mcp"
                value={m.url ?? ""}
                onChange={(e) => setM({ ...m, url: e.target.value })}
                onKeyDown={stop}
              />
            </div>
            <div className="space-y-1.5">
              <Label className={label}>{t("extSettings.headers")}</Label>
              <Textarea className={area} value={headers} onChange={(e) => setHeaders(e.target.value)} onKeyDown={stop} />
            </div>
          </>
        ) : (
          <>
            <div className="space-y-1.5">
              <Label className={label}>{t("extSettings.command")}</Label>
              <Input
                className={field}
                placeholder={t("extSettings.commandPlaceholder")}
                value={m.command ?? ""}
                onChange={(e) => setM({ ...m, command: e.target.value })}
                onKeyDown={stop}
              />
            </div>
            <div className="space-y-1.5">
              <Label className={label}>{t("extSettings.args")}</Label>
              <Textarea className={area} value={args} onChange={(e) => setArgs(e.target.value)} onKeyDown={stop} />
            </div>
            <div className="space-y-1.5">
              <Label className={label}>{t("extSettings.env")}</Label>
              <Textarea className={area} value={env} onChange={(e) => setEnv(e.target.value)} onKeyDown={stop} />
            </div>
          </>
        )}
      </div>
      <div className="flex justify-end gap-2">
        <Button size="sm" variant="ghost" className="text-[12px]" onClick={onCancel}>
          {t("common.cancel")}
        </Button>
        <Button size="sm" className="text-[12px]" disabled={!ok} onClick={save}>
          {t("common.save")}
        </Button>
      </div>
    </div>
  );
}
