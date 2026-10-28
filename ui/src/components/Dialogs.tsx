import { Check, FolderOpen, Trash2 } from "lucide-react";
import { useEffect, useState } from "react";
import { toast } from "sonner";
import { Button } from "@/components/ui/button";
import { CommandDialog, CommandEmpty, CommandGroup, CommandInput, CommandItem, CommandList, CommandShortcut } from "@/components/ui/command";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Switch } from "@/components/ui/switch";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { AgentSettings } from "./AgentSettings";
import { addWorkspace, basename, deleteSession, focusSession, openSessionHere, renameTab, renameWorkspace, selectWorkspace, tabName } from "@/lib/actions";
import { api } from "@/lib/api";
import { allCommands, runCommand } from "@/lib/commands";
import { ACTION_TITLES, DEFAULT_KEYS, formatCombo, type KeyAction } from "@/lib/keys";
import { locateSession } from "@/lib/layout";
import { pluginStatus } from "@/lib/plugins";
import { keyFor, set, setConfig, useStore, type Theme } from "@/lib/store";
import { cn } from "@/lib/utils";
import type { SessionSummary } from "@/lib/types";
import { AgentIcon, StatusDot } from "./AgentIcon";
import { MCPSettings, SkillSettings } from "./ExtSettings";
import { FileViewer } from "./FileViewer";
import { ImportHerdr } from "./ImportHerdr";
import { RemoteSettings } from "./RemoteSettings";
import { ResourceStats } from "./ResourceStats";
import { ago, DirPicker } from "./NewSession";

const close = () => set({ dialog: null });

export function Dialogs() {
  const d = useStore((s) => s.dialog);
  return (
    <>
      <Palette open={d?.kind === "palette"} />
      <Help open={d?.kind === "help"} />
      <SettingsDialog open={d?.kind === "settings"} />
      <Sessions open={d?.kind === "sessions"} />
      <Workspaces open={d?.kind === "workspaces"} />
      <Rename />
      <NewWorkspace open={d?.kind === "newWorkspace"} />
      <ImportHerdr open={d?.kind === "importHerdr"} />
      <FileViewer />
    </>
  );
}

function Palette({ open }: { open: boolean }) {
  useStore((s) => s.pluginVersion);
  const sessions = useStore((s) => s.sessions);
  const cmds = open ? allCommands() : [];
  return (
    <CommandDialog open={open} onOpenChange={(o) => !o && close()} title="命令面板" description="搜索命令或会话">
      <CommandInput placeholder="命令、会话…" />
      <CommandList>
        <CommandEmpty>没有匹配的</CommandEmpty>
        <CommandGroup heading="命令">
          {cmds.map((c) => (
            <CommandItem
              key={c.id}
              value={`${c.title} ${c.id}`}
              onSelect={() => {
                close();
                setTimeout(() => runCommand(c.id), 0);
              }}
            >
              {c.title}
              {c.source !== "builtin" && <span className="text-[11px] text-muted-foreground">· {c.source}</span>}
              {c.keys[0] && <CommandShortcut>{formatCombo(c.keys[0])}</CommandShortcut>}
            </CommandItem>
          ))}
        </CommandGroup>
        <CommandGroup heading="会话">
          {Object.values(sessions)
            .sort((a, b) => b.updatedAt - a.updatedAt)
            .slice(0, 50)
            .map((s) => (
              <CommandItem
                key={s.id}
                value={`${s.title ?? ""} ${s.cwd} ${s.id}`}
                onSelect={() => {
                  close();
                  focusSession(s.id);
                }}
              >
                <AgentIcon agent={s.agent} className="size-4" />
                <span className="truncate">{s.title || basename(s.cwd)}</span>
                <span className="truncate font-mono text-[11px] text-muted-foreground">{basename(s.cwd)}</span>
                <StatusDot status={s.status} />
              </CommandItem>
            ))}
        </CommandGroup>
      </CommandList>
    </CommandDialog>
  );
}

const GROUPS: [string, KeyAction[]][] = [
  ["分屏", ["splitRight", "splitDown", "closePane", "zoom", "focusLeft", "focusDown", "focusUp", "focusRight", "swapLeft", "swapDown", "swapUp", "swapRight", "nextPane", "prevPane"]],
  ["tab", ["newTab", "nextTab", "prevTab", "lastTab", "tab1", "closeTab", "renameTab"]],
  ["工作空间", ["newWorkspace", "workspaces", "nextWorkspace", "prevWorkspace", "renameWorkspace", "closeWorkspace"]],
  ["会话", ["sessions", "nextAttention", "prevAttention", "focusAttention", "renamePane", "stop"]],
  ["其他", ["palette", "toggleSidebar", "settings", "help"]],
];

function Help({ open }: { open: boolean }) {
  const cfg = useStore((s) => s.config);
  useStore((s) => s.pluginVersion);
  const plugin = open ? allCommands().filter((c) => c.source !== "builtin" && c.keys.length) : [];
  return (
    <Dialog open={open} onOpenChange={(o) => !o && close()}>
      <DialogContent className="max-h-[85vh] max-w-3xl overflow-auto">
        <DialogHeader>
          <DialogTitle>快捷键</DialogTitle>
          <DialogDescription>
            先按前缀 <kbd className="rounded border bg-muted px-1 font-mono">{formatCombo(cfg.prefix)}</kbd> 松开，再按后面那个键（和 herdr / tmux 一样）。中文输入法开着也能用。
          </DialogDescription>
        </DialogHeader>
        <div className="grid grid-cols-1 gap-x-8 gap-y-4 sm:grid-cols-2">
          {GROUPS.map(([g, acts]) => (
            <div key={g}>
              <div className="mb-1 text-[11px] font-medium uppercase tracking-wide text-muted-foreground">{g}</div>
              {acts.map((a) => (
                <div key={a} className="flex items-center justify-between py-0.5 text-[13px]">
                  <span>{a === "tab1" ? "跳到 tab 1–9" : ACTION_TITLES[a]}</span>
                  <span className="font-mono text-[12px] text-muted-foreground">{(a === "tab1" ? ["prefix+1..9"] : keyFor(cfg, a)).map((k) => (k === "prefix+1..9" ? `${formatCombo(cfg.prefix)} 1…9` : formatCombo(k))).join("  ") || "—"}</span>
                </div>
              ))}
            </div>
          ))}
          {plugin.length > 0 && (
            <div>
              <div className="mb-1 text-[11px] font-medium uppercase tracking-wide text-muted-foreground">插件</div>
              {plugin.map((c) => (
                <div key={c.id} className="flex items-center justify-between py-0.5 text-[13px]">
                  <span>{c.title}</span>
                  <span className="font-mono text-[12px] text-muted-foreground">{formatCombo(c.keys[0])}</span>
                </div>
              ))}
            </div>
          )}
          <div>
            <div className="mb-1 text-[11px] font-medium uppercase tracking-wide text-muted-foreground">输入框里</div>
            {[
              ["发送 / 插话", cfg.sendKey === "enter" ? "↵" : "⌘↵"],
              ["换行", cfg.sendKey === "enter" ? "⇧↵" : "↵"],
              ["打断这一轮", "Esc"],
              ["斜杠命令", "/"],
            ].map(([a, k]) => (
              <div key={a} className="flex items-center justify-between py-0.5 text-[13px]">
                <span>{a}</span>
                <span className="font-mono text-[12px] text-muted-foreground">{k}</span>
              </div>
            ))}
          </div>
        </div>
      </DialogContent>
    </Dialog>
  );
}

function KeyRecorder({ value, onChange }: { value: string; onChange: (v: string) => void }) {
  const [rec, setRec] = useState(false);
  return (
    <Input
      readOnly
      className={cn("h-7 w-44 font-mono text-[12px]", rec && "border-brand")}
      value={rec ? "按下组合键…" : value}
      onFocus={() => setRec(true)}
      onBlur={() => setRec(false)}
      onKeyDown={(e) => {
        e.preventDefault();
        e.stopPropagation();
        if (["Shift", "Control", "Alt", "Meta"].includes(e.key)) return;
        const parts = [];
        if (e.ctrlKey) parts.push("ctrl");
        if (e.altKey) parts.push("alt");
        if (e.shiftKey) parts.push("shift");
        if (e.metaKey) parts.push("meta");
        const k = e.code.startsWith("Key") ? e.code.slice(3).toLowerCase() : e.code.startsWith("Digit") ? e.code.slice(5) : e.key.toLowerCase();
        parts.push(k);
        onChange(parts.join("+"));
        (e.target as HTMLInputElement).blur();
      }}
    />
  );
}

function SettingsDialog({ open }: { open: boolean }) {
  const cfg = useStore((s) => s.config);
  const agents = useStore((s) => s.agents);
  const pluginsDir = useStore((s) => s.pluginsDir);
  const version = useStore((s) => s.version);
  useStore((s) => s.pluginVersion);
  const [tabOpen, setTabOpen] = useState("general");
  const setKeys = (a: KeyAction, v: string) => {
    const keys = { ...cfg.keys };
    const list = v.split(",").map((x) => x.trim()).filter(Boolean);
    if (JSON.stringify(list) === JSON.stringify(DEFAULT_KEYS[a])) delete keys[a];
    else keys[a] = list;
    setConfig({ keys });
  };
  return (
    <Dialog open={open} onOpenChange={(o) => !o && close()}>
      <DialogContent className="max-h-[85vh] max-w-2xl overflow-hidden p-0">
        <Tabs defaultValue="general" value={tabOpen} onValueChange={setTabOpen} className="flex h-[75vh] min-w-0 flex-col">
          <DialogHeader className="min-w-0 px-6 pt-5 max-md:px-4">
            <DialogTitle>设置</DialogTitle>
            {/* 八个分页在手机上一行放不下：横滑，不换行 */}
            <TabsList className="mt-2 max-w-full justify-start overflow-x-auto [scrollbar-width:none]">
              <TabsTrigger value="general">通用</TabsTrigger>
              <TabsTrigger value="keys">快捷键</TabsTrigger>
              <TabsTrigger value="skills">Skill</TabsTrigger>
              <TabsTrigger value="mcp">MCP</TabsTrigger>
              <TabsTrigger value="plugins">插件</TabsTrigger>
              <TabsTrigger value="agents">Agent</TabsTrigger>
              <TabsTrigger value="resources">资源</TabsTrigger>
              <TabsTrigger value="remote">远程访问</TabsTrigger>
            </TabsList>
          </DialogHeader>
          <div className="min-h-0 min-w-0 flex-1 overflow-auto px-6 pb-6 max-md:px-4">
            <TabsContent value="general" className="space-y-5 pt-2">
              <Row label="外观">
                <Select value={cfg.theme} onValueChange={(v) => setConfig({ theme: v as Theme })}>
                  <SelectTrigger className="w-36">
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    <SelectItem value="system">跟随系统</SelectItem>
                    <SelectItem value="light">浅色</SelectItem>
                    <SelectItem value="dark">深色</SelectItem>
                  </SelectContent>
                </Select>
              </Row>
              <Row label="字号">
                <Select value={String(cfg.fontSize)} onValueChange={(v) => setConfig({ fontSize: Number(v) })}>
                  <SelectTrigger className="w-36">
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    {[12, 13, 14, 15, 16, 17, 18].map((n) => (
                      <SelectItem key={n} value={String(n)}>
                        {n}px
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              </Row>
              <Row label="发送键" hint="另一种组合换行">
                <Select value={cfg.sendKey} onValueChange={(v) => setConfig({ sendKey: v as any })}>
                  <SelectTrigger className="w-36">
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    <SelectItem value="enter">回车</SelectItem>
                    <SelectItem value="mod+enter">⌘ / Ctrl + 回车</SelectItem>
                  </SelectContent>
                </Select>
              </Row>
              <Row label="默认 agent">
                <Select value={cfg.defaultAgent} onValueChange={(v) => setConfig({ defaultAgent: v })}>
                  <SelectTrigger className="w-36">
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    {agents.map((a) => (
                      <SelectItem key={a.id} value={a.id}>
                        {a.name}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              </Row>
              <Row label="系统通知" hint="看不见的会话跑完了 / 在等你时">
                <Switch checked={cfg.notifications} onCheckedChange={(v) => setConfig({ notifications: v })} />
              </Row>
              <Row label="切走的 tab 留在页面里" hint="展开状态、滚动位置都保住；越多越占内存（设置 → 资源 里看）">
                <Select value={String(cfg.keepTabs ?? 6)} onValueChange={(v) => setConfig({ keepTabs: Number(v) })}>
                  <SelectTrigger className="w-36">
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    {[1, 3, 6, 10, 20].map((n) => (
                      <SelectItem key={n} value={String(n)}>
                        {n === 1 ? "不留" : `最近 ${n} 个`}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              </Row>
              <Row label="从 herdr 导入" hint="工作空间、tab、分屏和每个 agent 的会话">
                <Button size="sm" variant="outline" onClick={() => set({ dialog: { kind: "importHerdr" } })}>
                  导入…
                </Button>
              </Row>
              <Row label="回退时默认恢复文件">
                <Switch checked={cfg.rewindFiles} onCheckedChange={(v) => setConfig({ rewindFiles: v })} />
              </Row>
              <div className="pt-2 text-[12px] text-muted-foreground">gerdr {version}</div>
            </TabsContent>
            <TabsContent value="keys" className="pt-2">
              <Row label="前缀键" hint="默认 ctrl+b">
                <KeyRecorder value={cfg.prefix} onChange={(v) => setConfig({ prefix: v })} />
              </Row>
              <div className="mt-3 text-[12px] text-muted-foreground">多个键用逗号隔开；「prefix+」表示先按前缀。清空 = 不绑定。</div>
              <div className="mt-2 divide-y">
                {(Object.keys(ACTION_TITLES) as KeyAction[]).map((a) => (
                  <div key={a} className="flex items-center justify-between gap-4 py-1.5 text-[13px]">
                    <span>{ACTION_TITLES[a]}</span>
                    <Input
                      className="h-7 w-56 font-mono text-[12px]"
                      defaultValue={keyFor(cfg, a).join(", ")}
                      onBlur={(e) => setKeys(a, e.target.value)}
                      onKeyDown={(e) => e.stopPropagation()}
                    />
                  </div>
                ))}
              </div>
            </TabsContent>
            <TabsContent value="skills" className="pt-2">
              <SkillSettings open={open} />
            </TabsContent>
            <TabsContent value="mcp" className="pt-2">
              <MCPSettings open={open} />
            </TabsContent>
            <TabsContent value="resources" className="pt-2">
              <ResourceStats open={open && tabOpen === "resources"} />
            </TabsContent>
            <TabsContent value="plugins" className="space-y-3 pt-2">
              <div className="text-[13px]">
                插件放在 <code className="rounded bg-muted px-1 font-mono text-[12px]">{pluginsDir}</code>，一个 .ts 文件一个，保存即生效。API 见同目录的 <code className="font-mono text-[12px]">gerdr.d.ts</code>。
              </div>
              <div className="divide-y rounded-md border">
                {pluginStatus.length === 0 && <div className="p-3 text-[13px] text-muted-foreground">没有插件</div>}
                {pluginStatus.map((p) => (
                  <div key={p.id} className="p-3">
                    <div className="flex items-center gap-2 text-[13px]">
                      {p.loaded ? <Check className="size-3.5 text-ok" /> : <span className="size-2 rounded-full bg-bad" />}
                      <span className="font-medium">{p.id}</span>
                      <span className="truncate font-mono text-[11px] text-muted-foreground">{p.path}</span>
                    </div>
                    {p.error && <pre className="mt-1.5 whitespace-pre-wrap rounded bg-bad/10 p-2 font-mono text-[11px] text-bad">{p.error}</pre>}
                  </div>
                ))}
              </div>
            </TabsContent>
            <TabsContent value="agents" className="pt-2">
              <AgentSettings />
            </TabsContent>
            <TabsContent value="remote" className="pt-2">
              <RemoteSettings open={open && tabOpen === "remote"} />
            </TabsContent>
          </div>
        </Tabs>
      </DialogContent>
    </Dialog>
  );
}

function Row({ label, hint, children }: { label: string; hint?: string; children: React.ReactNode }) {
  return (
    <div className="flex items-center justify-between gap-4">
      <div>
        <div className="text-[13px]">{label}</div>
        {hint && <div className="text-[12px] text-muted-foreground">{hint}</div>}
      </div>
      {children}
    </div>
  );
}

// 会话列表（prefix+g）：和命令面板一个样子。「开着的」= 界面上有 pane 显示它；关掉 pane 就是关掉会话（和 herdr 一样），
// 在「关掉的」里，点一下在当前位置接着聊（历史都在）。
function Sessions({ open }: { open: boolean }) {
  const sessions = useStore((s) => s.sessions);
  const layout = useStore((s) => s.layout);
  const unread = useStore((s) => s.unread);
  const all = Object.values(sessions).sort((a, b) => b.updatedAt - a.updatedAt);
  const isOpen = (id: string) => !!locateSession(layout, id);
  const opened = all.filter((s) => isOpen(s.id));
  const closed = all.filter((s) => !isOpen(s.id));
  const row = (s: SessionSummary, shown: boolean) => (
    <CommandItem
      key={s.id}
      value={`${s.id} ${s.title ?? ""} ${s.cwd} ${s.agent}`}
      className="group gap-2.5"
      onSelect={() => {
        close();
        if (shown) focusSession(s.id);
        else openSessionHere(s.id);
      }}
    >
      <AgentIcon agent={s.agent} className="size-4 shrink-0" />
      <span className="min-w-0 truncate">{s.title || basename(s.cwd)}</span>
      <span className="min-w-0 shrink truncate font-mono text-[11px] text-muted-foreground">{basename(s.cwd)}</span>
      <span className="ml-auto flex shrink-0 items-center gap-2">
        <StatusDot status={s.status} unread={!!unread[s.id]} />
        <span className="w-16 text-right text-[11px] text-muted-foreground">{ago(s.updatedAt)}</span>
        {!shown && (
          <button
            className="hidden rounded p-0.5 text-muted-foreground hover:text-bad group-data-[selected=true]:block"
            title="删掉这条记录（agent 自己的会话记录不动）"
            onPointerDown={(e) => e.stopPropagation()}
            onClick={(e) => {
              e.stopPropagation();
              deleteSession(s.id).catch((err) => toast.error(err.message));
            }}
          >
            <Trash2 className="size-3.5" />
          </button>
        )}
      </span>
    </CommandItem>
  );
  return (
    <CommandDialog open={open} onOpenChange={(o) => !o && close()} title="会话" description="所有会话" className="max-w-2xl">
      <CommandInput placeholder="搜会话：标题、目录…" />
      <CommandList className="max-h-[60vh]">
        <CommandEmpty>没有</CommandEmpty>
        {opened.length > 0 && <CommandGroup heading={`开着的 · ${opened.length}`}>{opened.map((s) => row(s, true))}</CommandGroup>}
        {closed.length > 0 && <CommandGroup heading={`关掉的 · ${closed.length}`}>{closed.map((s) => row(s, false))}</CommandGroup>}
      </CommandList>
    </CommandDialog>
  );
}

function Workspaces({ open }: { open: boolean }) {
  const layout = useStore((s) => s.layout);
  return (
    <CommandDialog open={open} onOpenChange={(o) => !o && close()} title="工作空间" description="切换工作空间">
      <CommandInput placeholder="工作空间…" />
      <CommandList>
        <CommandEmpty>没有</CommandEmpty>
        <CommandGroup>
          {layout.workspaces.map((w, i) => (
            <CommandItem
              key={w.id}
              value={`${i + 1} ${w.name}`}
              onSelect={() => {
                close();
                selectWorkspace(w.id);
              }}
            >
              <span className="w-4 font-mono text-[11px] text-muted-foreground">{i + 1}</span>
              {w.name}
              <span className="text-[11px] text-muted-foreground">{w.tabs.map(tabName).join(" · ")}</span>
            </CommandItem>
          ))}
        </CommandGroup>
      </CommandList>
    </CommandDialog>
  );
}

function Rename() {
  const d = useStore((s) => s.dialog);
  const r = d?.kind === "rename" ? d : null;
  const [v, setV] = useState("");
  useEffect(() => {
    if (r) setV(r.value);
  }, [r?.id, r?.target]);
  const save = () => {
    if (!r) return;
    if (r.target === "tab") renameTab(r.id, v);
    else if (r.target === "workspace") renameWorkspace(r.id, v);
    else api.rename(r.id, v).catch((e) => toast.error(e.message));
    close();
  };
  return (
    <Dialog open={!!r} onOpenChange={(o) => !o && close()}>
      <DialogContent className="max-w-sm">
        <DialogHeader>
          <DialogTitle>{r?.target === "tab" ? "重命名 tab" : r?.target === "workspace" ? "重命名工作空间" : "重命名会话"}</DialogTitle>
        </DialogHeader>
        <Input
          autoFocus
          value={v}
          onChange={(e) => setV(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === "Enter" && !e.nativeEvent.isComposing && e.keyCode !== 229) save();
          }}
          placeholder={r?.target === "tab" ? "留空 = 用目录名" : ""}
        />
        <DialogFooter>
          <Button onClick={save}>好</Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

function NewWorkspace({ open }: { open: boolean }) {
  const home = useStore((s) => s.home);
  const [name, setName] = useState("");
  const [cwd, setCwd] = useState("");
  useEffect(() => {
    if (open) {
      setName("");
      setCwd("~/");
    }
  }, [open]);
  const create = () => {
    const abs = cwd.startsWith("~") ? home + cwd.slice(1) : cwd;
    const n = name.trim() || (cwd.replace(/\/+$/, "") && cwd !== "~" ? basename(abs) : "");
    addWorkspace(n, abs.replace(/\/+$/, "") || undefined);
    close();
  };
  return (
    <Dialog open={open} onOpenChange={(o) => !o && close()}>
      <DialogContent className="max-w-md">
        <DialogHeader>
          <DialogTitle>新工作空间</DialogTitle>
          <DialogDescription>一个项目一个工作空间：里面新开的 tab 默认在这个目录。</DialogDescription>
        </DialogHeader>
        <div className="space-y-3">
          <div className="space-y-1.5">
            <Label>目录</Label>
            <DirPicker value={cwd} onChange={setCwd} onEnter={create} autoFocus />
          </div>
          <div className="space-y-1.5">
            <Label>名字</Label>
            <Input value={name} placeholder="留空 = 目录名" onChange={(e) => setName(e.target.value)} onKeyDown={(e) => e.key === "Enter" && !e.nativeEvent.isComposing && create()} />
          </div>
        </div>
        <DialogFooter>
          <Button onClick={create}>
            <FolderOpen className="size-4" /> 创建
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

