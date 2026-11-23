import { PanelSettings } from "./PanelSettings";
import { GRID_TILES, gridMax } from "@/lib/overview";
import {
  Activity,
  Bell,
  Bot,
  Brain,
  ChevronLeft,
  ChevronRight,
  CircleArrowUp,
  FlaskConical,
  Globe,
  Keyboard,
  LayoutDashboard,
  Loader2,
  Plug,
  Puzzle,
  RadioTower,
  Search,
  Settings2,
  Sparkles,
  type LucideIcon,
} from "lucide-react";
import { useEffect, useMemo, useRef, useState } from "react";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogTitle } from "@/components/ui/dialog";
import { lightboxOpen } from "@/components/Lightbox";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Switch } from "@/components/ui/switch";
import { androidCan, isApp } from "@/lib/android";
import { api } from "@/lib/api";
import { usePhone } from "@/lib/phone";
import { closeDialog, fontSizeOf, set, setConfig, setFontSize, setPref, useStore, type Theme } from "@/lib/store";
import { cn } from "@/lib/utils";
import { LoaderSelect } from "./LoaderSelect";
import { languagePref, setLanguage, t, type LanguagePref } from "@/lib/i18n";
import type { MessageKey } from "@/lib/messages";
import { AddonSettings } from "./AddonSettings";
import { AgentSettings } from "./AgentSettings";
import { AndroidSettings } from "./AndroidSettings";
import { AwakeSetting } from "./AwakeSetting";
import { BrowserSettings } from "./BrowserSettings";
import { DoctorPanel } from "./DoctorPanel";
import { MCPSettings, SkillSettings } from "./ExtSettings";
import { KeySettings } from "./KeySettings";
import { MemorySettings } from "./MemorySettings";
import { RemoteSettings } from "./RemoteSettings";
import { ResourceStats } from "./ResourceStats";
import { ServiceSetting } from "./ServiceSetting";
import { RestartSetting } from "./RestartSetting";
import { UIUpgradeSetting } from "./UIUpgradeSetting";
import { UpdateSetting } from "./UpdateSetting";

// The pages, grouped down the left column. Was a row of tabs across the top with the Agent page's own row of four
// under it; at ten pages the row no longer fit and General had grown to several screens (user asked for a layout that
// scales: a sidebar of groups, like Linear / Discord / macOS System Settings).
type Page =
  "general" | "notify" | "keys" | "roles" | "skills" | "mcp" | "memory" | "panels" | "browser" | "addons" | "remote" | "update" | "resources" | "labs";

const NAV: { group?: MessageKey; items: { id: Page; icon: LucideIcon; label: () => string }[] }[] = [
  {
    items: [
      { id: "general", icon: Settings2, label: () => t("settings.tab.general") },
      { id: "notify", icon: Bell, label: () => t("settings.notify") },
      { id: "keys", icon: Keyboard, label: () => t("settings.tab.keys") },
    ],
  },
  {
    group: "settings.group.agent",
    items: [
      { id: "roles", icon: Bot, label: () => t("settings.agent.roles") },
      { id: "skills", icon: Sparkles, label: () => "Skill" },
      { id: "mcp", icon: Plug, label: () => "MCP" },
      { id: "memory", icon: Brain, label: () => t("settings.agent.memory") },
    ],
  },
  {
    group: "settings.group.workspace",
    items: [
      { id: "panels", icon: LayoutDashboard, label: () => t("panelSettings.tab") },
      { id: "browser", icon: Globe, label: () => t("settings.tab.browser") },
      { id: "addons", icon: Puzzle, label: () => t("settings.tab.addons") },
    ],
  },
  {
    group: "settings.group.connect",
    items: [{ id: "remote", icon: RadioTower, label: () => t("settings.tab.remote") }],
  },
  {
    group: "settings.group.system",
    items: [
      { id: "update", icon: CircleArrowUp, label: () => t("settings.versions") },
      { id: "resources", icon: Activity, label: () => t("settings.tab.resources") },
      { id: "labs", icon: FlaskConical, label: () => t("settings.tab.labs") },
    ],
  },
];
const ITEMS = NAV.flatMap((g) => g.items);
const itemOf = (p: Page) => ITEMS.find((i) => i.id === p)!;

// Where an opener's tab lands (store dialog.tab: the rail, the inbox's 设置, a panel's openSettings, ?settings=remote).
// The old names stay valid: "agents" was the page holding roles / Skill / MCP / memory, "inbox" a section of General.
const ALIAS: Record<string, Page> = { agents: "roles", inbox: "notify" };
const pageOf = (tab: string): Page => ALIAS[tab] ?? (ITEMS.some((i) => i.id === tab) ? (tab as Page) : "general");

// What the search finds besides page names: the rows drawn here, by their label and hint. Pages drawn by their own
// components (Remote, Add-ons…) are found by name. A row is jumped to by its label (Row's data-setting).
// prettier-ignore
const INDEX: [Page, MessageKey, MessageKey?][] = [
  ["general", "settings.language", "settings.language.hint"],
  ["general", "settings.theme"],
  ["general", "settings.fontSize"],
  ["general", "settings.loader", "settings.loader.hint"],
  ["general", "settings.sendKey", "settings.sendKey.hint"],
  ["general", "settings.defaultAgent", "settings.defaultAgent.hint"],
  ["general", "settings.expandEdits", "settings.expandEdits.hint"],
  ["general", "settings.autoSendShell", "settings.autoSendShell.hint"],
  ["general", "settings.confirmLinks", "settings.confirmLinks.hint"],
  ["general", "settings.keepTabs", "settings.keepTabs.hint"],
  ["general", "settings.gridTiles", "settings.gridTiles.hint"],
  ["general", "settings.import.herdr", "settings.import.herdr.hint"],
  ["general", "settings.import.claudeDesktop", "settings.import.claudeDesktop.hint"],
  ["notify", "settings.notify.system", "settings.notify.system.hint"],
  ["notify", "settings.inbox.permission", "settings.inbox.permission.hint"],
  ["notify", "settings.inbox.ask", "settings.inbox.ask.hint"],
  ["notify", "settings.inbox.error", "settings.inbox.error.hint"],
  ["notify", "settings.inbox.battery", "settings.inbox.battery.hint"],
  ["skills", "settings.skillUpdates", "settings.skillUpdates.hint"],
  ["remote", "settings.labs.keepAwake", "settings.labs.keepAwake.hint"],
  ["labs", "settings.labs.virtualList", "settings.labs.virtualList.hint"],
  ["labs", "settings.labs.readFromTop", "settings.labs.readFromTop.hint"],
  ["labs", "settings.labs.copyOnSelect", "settings.labs.copyOnSelect.hint"],
  ["labs", "settings.labs.prodReact", "settings.labs.prodReact.hint"],
];

type Hit = { page: Page; label?: string };

function search(q: string): Hit[] {
  const words = q.toLowerCase().split(/\s+/).filter(Boolean);
  if (!words.length) return [];
  const has = (...texts: string[]) => {
    const all = texts.join(" ").toLowerCase();
    return words.every((w) => all.includes(w));
  };
  const pages: Hit[] = ITEMS.filter((i) => has(i.label(), i.id)).map((i) => ({ page: i.id }));
  const rows: Hit[] = INDEX.filter(([, l, h]) => has(t(l), h ? t(h) : "")).map(([page, l]) => ({ page, label: t(l) }));
  return [...pages, ...rows];
}

// Settings (prefix+s / ⌘,). A large dialog: groups of pages down the left with a search over them, the page on the
// right; Esc closes it. On a phone the list and the page take the dialog in turn (back returns to the list). The
// general / notification / labs pages live here; every other page is its own component file.
export function SettingsDialog({ open }: { open: boolean }) {
  const cfg = useStore((s) => s.config);
  const bat = cfg.batteryAlert ?? { on: true, percent: 20 };
  const inb = cfg.inbox ?? {};
  const fontSize = useStore(fontSizeOf);
  // The settings each device keeps for itself (DEVICE_KEYS): its own value, else the shared one
  const device = useStore((s) => s.device);
  const dev = { ...cfg, ...device };
  const agents = useStore((s) => s.agents);
  const remoteConns = useStore((s) => s.remoteConns.length);
  const doctorBad = useStore((s) => s.doctor?.level === "bad");
  const phone = usePhone();
  const [page, setPage] = useState<Page>("general");
  // Phone only: the list of pages is showing instead of a page
  const [listing, setListing] = useState(true);
  const [query, setQuery] = useState("");
  const hits = useMemo(() => search(query), [query]);
  const body = useRef<HTMLDivElement>(null);
  // A row to scroll to and flash once its page has drawn (a search hit, or an opener's section)
  const [target, setTarget] = useState<string | null>(null);

  const go = (p: Page, row?: string) => {
    setPage(p);
    setListing(false);
    setTarget(row ?? null);
    if (!row) body.current?.scrollTo({ top: 0 });
  };

  // 从别处直接打开某一页（比如「去批准」→ 远程访问）
  const wantTab = useStore((s) => (s.dialog?.kind === "settings" ? s.dialog.tab : undefined));
  useEffect(() => {
    if (!open) return;
    setQuery("");
    if (!wantTab) {
      // A phone opens on the list; a wider screen on the page last looked at
      setListing(true);
      return;
    }
    // The inbox's 设置 button: its section, not the top of a page with other things on it
    go(pageOf(wantTab), wantTab === "inbox" ? "#settings-inbox" : undefined);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open, wantTab]);

  useEffect(() => {
    if (!target) return;
    const id = requestAnimationFrame(() => {
      const el = target.startsWith("#")
        ? document.getElementById(target.slice(1))
        : body.current?.querySelector<HTMLElement>(`[data-setting="${CSS.escape(target)}"]`);
      if (!el) return;
      el.scrollIntoView({ block: target.startsWith("#") ? "start" : "center" });
      if (!target.startsWith("#"))
        el.animate([{ backgroundColor: "color-mix(in oklab, var(--brand) 18%, transparent)" }, { backgroundColor: "transparent" }], {
          duration: 1600,
          easing: "ease-out",
        });
      setTarget(null);
    });
    return () => cancelAnimationFrame(id);
  }, [target, page]);

  const badge = (p: Page) =>
    p === "remote" && remoteConns > 0 ? (
      // 有远程界面连着：数字红点（谁连着、连了多久在页面里面）
      <span className="ml-auto inline-flex h-4 min-w-4 items-center justify-center rounded-full bg-bad px-1 text-[10px] leading-none font-medium text-white tabular-nums">
        {remoteConns}
      </span>
    ) : p === "labs" && doctorBad ? (
      // the diagnosis found something broken
      <span className="ml-auto size-1.5 rounded-full bg-bad" />
    ) : null;

  const navButton = (id: Page, sub?: string) => {
    const it = itemOf(id);
    const Icon = it.icon;
    const on = !phone && !sub && page === id && !query;
    return (
      <button
        key={id + (sub ?? "")}
        type="button"
        onClick={() => go(id, sub)}
        className={cn(
          "flex w-full items-center gap-2.5 rounded-md px-2.5 text-left text-[13px] outline-none focus-visible:ring-2 focus-visible:ring-ring/50",
          sub ? "py-1.5" : phone ? "h-11" : "h-8",
          phone && "text-[15px]",
          on ? "bg-accent font-medium text-foreground" : "text-foreground/80 hover:bg-accent/60 hover:text-foreground",
        )}
      >
        <Icon className={cn("shrink-0 text-muted-foreground", phone ? "size-[18px]" : "size-4")} />
        {sub ? (
          <span className="min-w-0 flex-1">
            <span className="block truncate">{sub}</span>
            <span className="block truncate text-[11px] text-muted-foreground">{it.label()}</span>
          </span>
        ) : (
          <span className="min-w-0 flex-1 truncate">{it.label()}</span>
        )}
        {!sub && badge(id)}
        {phone && !sub && <ChevronRight className="size-4 shrink-0 text-muted-foreground" />}
      </button>
    );
  };

  const nav = (
    <div className={cn("flex min-h-0 flex-col", phone ? "flex-1" : "w-56 shrink-0 border-r bg-sidebar")}>
      <div className={cn(phone ? "px-4 pt-4 pb-2" : "px-4 pt-5 pb-3")}>
        <DialogTitle className={phone ? "text-[17px]" : "text-[15px]"}>{t("settings.title")}</DialogTitle>
      </div>
      <div className={cn("relative", phone ? "px-4 pb-2" : "px-3 pb-2")}>
        <Search className={cn("pointer-events-none absolute top-1/2 size-3.5 -translate-y-1/2 text-muted-foreground", phone ? "left-6.5" : "left-5.5")} />
        <input
          // First in the dialog, so it takes the focus on opening (desktop): type to find a setting
          value={query}
          onChange={(e) => setQuery(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === "Enter" && hits[0]) go(hits[0].page, hits[0].label);
          }}
          placeholder={t("settings.search")}
          className="h-8 w-full rounded-md border border-input bg-background pr-2 pl-7 text-[13px] outline-none placeholder:text-muted-foreground focus-visible:border-ring max-md:text-base dark:bg-input/30"
        />
      </div>
      <div className={"min-h-0 flex-1 overflow-auto px-2 pb-3"}>
        {query ? (
          hits.length ? (
            <div className="space-y-0.5">{hits.map((h) => navButton(h.page, h.label))}</div>
          ) : (
            <div className="px-2.5 py-2 text-[12px] text-muted-foreground">{t("settings.noResults")}</div>
          )
        ) : (
          NAV.map((g, i) => (
            <div key={i} className={cn(i > 0 && "mt-3")}>
              {g.group && <div className="px-2.5 pb-1 text-[11px] font-medium text-muted-foreground">{t(g.group)}</div>}
              <div className="space-y-0.5">{g.items.map((it) => navButton(it.id))}</div>
            </div>
          ))
        )}
      </div>
    </div>
  );

  const content = (
    <div className="flex min-h-0 min-w-0 flex-1 flex-col">
      <div className={cn("flex shrink-0 items-center gap-1", phone ? "px-2 pt-3 pb-2" : "px-8 pt-5 pb-3")}>
        {phone && (
          <Button variant="ghost" size="icon" className="size-8" aria-label={t("common.back")} onClick={() => setListing(true)}>
            <ChevronLeft className="size-5" />
          </Button>
        )}
        <div className={cn("text-[15px] font-medium", !phone && "mx-auto w-full max-w-3xl")}>{itemOf(page).label()}</div>
      </div>
      {/* One size for the whole panel. Text nobody sized is 13px (section titles are 13px, descriptions 12px); shadcn's controls
        default to 14px text and 36px height (Input also sets md:text-sm), which looked oversized next to that, so on desktop
        every button / input / textarea / select here is 12px and the default-sized ones 32px tall, like size="sm". Pages can
        still size a control themselves; icon buttons keep their size. On a phone buttons are 28px with 13px text, and inputs /
        selects are 28px too so a button next to an input lines up (inputs keep 16px text there). */}
      <div
        ref={body}
        className={cn(
          "min-h-0 flex-1 overflow-auto text-[13px] max-md:[&_[data-slot=button]:not([data-size^=icon])]:h-7 max-md:[&_[data-slot=button]:not([data-size^=icon])]:px-2.5 max-md:[&_[data-slot=button]]:text-[13px] max-md:[&_[data-slot=input]]:h-7 max-md:[&_[data-slot=input]]:py-0 max-md:[&_[data-slot=select-trigger][data-size=default]]:h-7 md:[&_[data-slot=button]]:text-[12px] md:[&_[data-slot=button][data-size=default]]:h-8 md:[&_[data-slot=button][data-size=default]]:px-3 md:[&_[data-slot=input]]:h-8 md:[&_[data-slot=input]]:text-[12px] md:[&_[data-slot=textarea]]:text-[12px] md:[&_[data-slot=select-trigger]]:text-[12px] md:[&_[data-slot=select-trigger][data-size=default]]:h-8",
          phone ? "px-4 pb-6" : "px-8 pb-8",
        )}
      >
        {/* key: a page's own state (scroll inside lists, half-filled forms) starts fresh on each visit, as the tabs did */}
        <div key={page} className="mx-auto max-w-3xl">
          {renderPage()}
        </div>
      </div>
    </div>
  );

  function renderPage() {
    switch (page) {
      case "general":
        return (
          <div className="space-y-6 pt-1">
            {/* Grouped by what a setting is about. Where it's stored (gerdr's config.json, shared by every device, or this
              device only: localStorage / the Android shell) is a tag on the few device-only rows: grouping by storage put
              font size a screen away from the theme (and without saying it, people can't guess whether a change on the
              phone follows them to the computer). */}
            <div className="text-[12px] text-muted-foreground">
              {(() => {
                const [before, after] = t("settings.where").split("{tag}");
                return (
                  <>
                    {before}
                    <LocalTag />
                    {after}
                  </>
                );
              })()}
            </div>
            <Section title={t("settings.look")}>
              <Row label={t("settings.language")} hint={t("settings.language.hint")} local>
                <Select value={languagePref()} onValueChange={(v) => setLanguage(v as LanguagePref)}>
                  <SelectTrigger className="w-36">
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    <SelectItem value="system">{t("settings.language.system")}</SelectItem>
                    {/* Each language in its own name: someone who can't read the current one still finds theirs */}
                    <SelectItem value="zh">中文</SelectItem>
                    <SelectItem value="en">English</SelectItem>
                  </SelectContent>
                </Select>
              </Row>
              <Row label={t("settings.theme")} local>
                <Select value={dev.theme} onValueChange={(v) => setPref({ theme: v as Theme })}>
                  <SelectTrigger className="w-36">
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    <SelectItem value="system">{t("settings.theme.system")}</SelectItem>
                    <SelectItem value="light">{t("settings.theme.light")}</SelectItem>
                    <SelectItem value="dark">{t("settings.theme.dark")}</SelectItem>
                  </SelectContent>
                </Select>
              </Row>
              <Row label={t("settings.fontSize")} local>
                <Select value={String(fontSize)} onValueChange={(v) => setFontSize(Number(v))}>
                  <SelectTrigger className="w-36">
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    {[12, 13, 14, 15, 16, 17, 18, 19, 20].map((n) => (
                      <SelectItem key={n} value={String(n)}>
                        {n}px
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              </Row>
              <LoaderSelect
                row={(control) => (
                  <Row label={t("settings.loader")} hint={t("settings.loader.hint")}>
                    {control}
                  </Row>
                )}
              />
            </Section>
            <Section title={t("settings.chat")}>
              <Row label={t("settings.sendKey")} hint={t("settings.sendKey.hint")} local>
                <Select value={dev.sendKey} onValueChange={(v) => setPref({ sendKey: v as any })}>
                  <SelectTrigger className="w-36">
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    <SelectItem value="enter">{t("settings.sendKey.enter")}</SelectItem>
                    <SelectItem value="mod+enter">{t("settings.sendKey.modEnter")}</SelectItem>
                  </SelectContent>
                </Select>
              </Row>
              <Row label={t("settings.defaultAgent")} hint={t("settings.defaultAgent.hint")}>
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
              <Row label={t("settings.expandEdits")} hint={t("settings.expandEdits.hint")}>
                <Switch checked={cfg.expandEdits !== false} onCheckedChange={(v) => setConfig({ expandEdits: v })} />
              </Row>
              <Row label={t("settings.autoSendShell")} hint={t("settings.autoSendShell.hint")}>
                <Switch checked={cfg.autoSendShell !== false} onCheckedChange={(v) => setConfig({ autoSendShell: v })} />
              </Row>
              <Row label={t("settings.confirmLinks")} hint={t("settings.confirmLinks.hint")}>
                <Switch checked={cfg.confirmLinks !== false} onCheckedChange={(v) => setConfig({ confirmLinks: v })} />
              </Row>
              <Row label={t("settings.keepTabs")} hint={t("settings.keepTabs.hint")} local>
                <Select value={String(dev.keepTabs ?? 6)} onValueChange={(v) => setPref({ keepTabs: Number(v) })}>
                  <SelectTrigger className="w-36">
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    {[1, 3, 6, 10, 20].map((n) => (
                      <SelectItem key={n} value={String(n)}>
                        {n === 1 ? t("settings.keepTabs.none") : t("settings.keepTabs.recent", { n })}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              </Row>
            </Section>
            <Section title={t("settings.grid")}>
              <Row label={t("settings.gridTiles")} hint={t("settings.gridTiles.hint")} local>
                <Select value={String(gridMax(dev.gridTiles))} onValueChange={(v) => setPref({ gridTiles: Number(v) })}>
                  <SelectTrigger className="w-36">
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    {GRID_TILES.map((n) => (
                      <SelectItem key={n} value={String(n)}>
                        {t("settings.gridTiles.n", { n })}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              </Row>
            </Section>
            {/* Only in a phone App (its shell keeps these): Android's background notifications, volume keys, server; iOS's server */}
            {isApp() && (
              <Section title={t("settings.thisPhone")} local>
                <AndroidSettings />
              </Section>
            )}
            <Section title={t("settings.import")}>
              <Row label={t("settings.import.herdr")} hint={t("settings.import.herdr.hint")}>
                <Button size="sm" variant="outline" onClick={() => set({ dialog: { kind: "importHerdr" } })}>
                  {t("settings.import.button")}
                </Button>
              </Row>
              <Row label={t("settings.import.claudeDesktop")} hint={t("settings.import.claudeDesktop.hint")}>
                <Button size="sm" variant="outline" onClick={() => set({ dialog: { kind: "importClaudeDesktop" } })}>
                  {t("settings.import.button")}
                </Button>
              </Row>
            </Section>
          </div>
        );
      case "notify":
        return (
          <div className="space-y-6 pt-1">
            <Section title={t("settings.notify")}>
              <Row label={t("settings.notify.system")} hint={t("settings.notify.system.hint")} local>
                <Switch checked={dev.notifications} onCheckedChange={(v) => setPref({ notifications: v })} />
              </Row>
            </Section>
            <Section title={t("settings.inbox")} id="settings-inbox">
              <Row label={t("settings.inbox.permission")} hint={t("settings.inbox.permission.hint")}>
                <Switch checked={inb.permission !== false} onCheckedChange={(v) => setConfig({ inbox: { ...inb, permission: v } })} />
              </Row>
              <Row label={t("settings.inbox.ask")} hint={t("settings.inbox.ask.hint")}>
                <Switch checked={inb.ask !== false} onCheckedChange={(v) => setConfig({ inbox: { ...inb, ask: v } })} />
              </Row>
              <Row label={t("settings.inbox.error")} hint={t("settings.inbox.error.hint")}>
                <Switch checked={inb.error !== false} onCheckedChange={(v) => setConfig({ inbox: { ...inb, error: v } })} />
              </Row>
              <Row label={t("settings.inbox.battery")} hint={t("settings.inbox.battery.hint")}>
                <div className="flex items-center gap-2">
                  {bat.on && (
                    <Select value={String(bat.percent)} onValueChange={(v) => setConfig({ batteryAlert: { ...bat, percent: Number(v) } })}>
                      <SelectTrigger className="w-24">
                        <SelectValue />
                      </SelectTrigger>
                      <SelectContent>
                        {[5, 10, 15, 20, 30, 40, 50].map((n) => (
                          <SelectItem key={n} value={String(n)}>
                            {n}%
                          </SelectItem>
                        ))}
                      </SelectContent>
                    </Select>
                  )}
                  <Switch checked={bat.on} onCheckedChange={(v) => setConfig({ batteryAlert: { ...bat, on: v } })} />
                </div>
              </Row>
            </Section>
          </div>
        );
      case "keys":
        return <KeySettings />;
      case "roles":
        return <AgentSettings />;
      case "skills":
        return (
          <div className="space-y-4">
            <Row label={t("settings.skillUpdates")} hint={t("settings.skillUpdates.hint")}>
              <Switch checked={cfg.skillUpdates !== false} onCheckedChange={(v) => setConfig({ skillUpdates: v })} />
            </Row>
            <SkillSettings open={open} />
          </div>
        );
      case "mcp":
        return <MCPSettings open={open} />;
      case "memory":
        return <MemorySettings open={open} />;
      case "panels":
        return <PanelSettings />;
      case "browser":
        return <BrowserSettings open={open} />;
      case "addons":
        return <AddonSettings open={open} />;
      case "remote":
        return (
          <div className="space-y-4">
            <RemoteSettings open={open} />
            {/* Staying awake is for being reached from elsewhere: it sits with remote access (asked for; was in Labs) */}
            <div className="space-y-4 border-t pt-4">
              <Row label={t("settings.labs.keepAwake")} hint={t("settings.labs.keepAwake.hint")}>
                <Switch checked={!!cfg.keepAwakeRemote} onCheckedChange={(v) => setConfig({ keepAwakeRemote: v })} />
              </Row>
              <AwakeSetting open={open} />
            </div>
          </div>
        );
      case "update":
        // The gerdr on the computer running it: which version, updating it (App only), the background service (macOS)
        return (
          <div className="space-y-4">
            <UpdateSetting />
            <UIUpgradeSetting />
            <ServiceSetting />
            <RestartSetting />
          </div>
        );
      case "resources":
        return <ResourceStats open={open} />;
      case "labs":
        return (
          <div className="space-y-4">
            <div className="text-[13px] text-muted-foreground">{t("settings.labs.intro")}</div>
            <Row label={t("settings.labs.virtualList")} hint={t("settings.labs.virtualList.hint")} local>
              <Switch checked={!!dev.virtualList} onCheckedChange={(v) => setPref({ virtualList: v })} />
            </Row>
            <Row label={t("settings.labs.readFromTop")} hint={t("settings.labs.readFromTop.hint")} local>
              <Switch checked={!!dev.readFromTop} onCheckedChange={(v) => setPref({ readFromTop: v })} />
            </Row>
            <Row label={t("settings.labs.copyOnSelect")} hint={t("settings.labs.copyOnSelect.hint")} local>
              <Switch checked={!!dev.copyOnSelect} onCheckedChange={(v) => setPref({ copyOnSelect: v })} />
            </Row>
            {androidCan("keepConnected") && (
              <Row label={t("settings.labs.keepConnected")} hint={t("settings.labs.keepConnected.hint")} local>
                <Switch checked={!!dev.keepConnected} onCheckedChange={(v) => setPref({ keepConnected: v })} />
              </Row>
            )}
            <ProdReactRow />
            <div className="border-t pt-4">
              <DoctorPanel />
            </div>
          </div>
        );
    }
  }

  return (
    <Dialog open={open} onOpenChange={(o) => !o && closeDialog()}>
      <DialogContent
        className={cn(
          "flex gap-0 overflow-hidden p-0",
          phone ? "h-[85vh] flex-col" : "h-[min(820px,88vh)] w-[min(1080px,calc(100vw-4rem))] sm:max-w-[calc(100vw-4rem)]",
        )}
        // The lightbox (an add-on's screenshots) sits above this dialog but outside it: clicking or pressing Esc
        // there is for the lightbox, not a reason to close Settings
        onPointerDownOutside={(e) => lightboxOpen() && e.preventDefault()}
        onEscapeKeyDown={(e) => {
          if (lightboxOpen()) return e.preventDefault();
          // A phone's back (Android's key, lib/android.ts closeTop, sends Esc): from a page back to the list first
          if (phone && !listing) {
            e.preventDefault();
            setListing(true);
          }
        }}
        // A phone has no keyboard: focusing the search would pop the soft keyboard up over the list
        onOpenAutoFocus={(e) => phone && e.preventDefault()}
      >
        {phone ? (
          listing ? (
            nav
          ) : (
            content
          )
        ) : (
          <>
            {nav}
            {content}
          </>
        )}
      </DialogContent>
    </Dialog>
  );
}

function Section({ id, title, hint, local, children }: { id?: string; title: string; hint?: string; local?: boolean; children: React.ReactNode }) {
  return (
    <section id={id} className="space-y-4">
      <div className="border-b pb-1.5">
        <div className="flex items-center gap-1.5 text-[12px] font-medium text-muted-foreground">
          {title}
          {local && <LocalTag />}
        </div>
        {hint && <div className="text-[11px] text-muted-foreground/80">{hint}</div>}
      </div>
      {children}
    </section>
  );
}

/** Marks a setting kept on this device only (localStorage / the Android shell), not in gerdr's shared config. */
function LocalTag({ className }: { className?: string }) {
  return (
    <span className={cn("rounded-sm border px-1 py-px text-[10px] font-normal leading-none text-muted-foreground", className)}>{t("settings.localTag")}</span>
  );
}

/**
 * Production React, on by default (internal/uisite/prod.go): a cookie the backend reads when it serves the page, so
 * the switch is a reload. The page says which React it got (window.__GERDR_UI__.react).
 */
function ProdReactRow() {
  const asked = !/(?:^|;\s*)gerdr_react=development(?:;|$)/.test(document.cookie);
  const got = (window as { __GERDR_UI__?: { react?: string } }).__GERDR_UI__?.react;
  // Switching to development takes seconds the first time (its packages are bundled on demand), then reloads: the
  // switch says so from the tap until the page goes, instead of sitting still (reported)
  const [busy, setBusy] = useState<boolean | null>(null);
  const [failed, setFailed] = useState<"preparing" | "unavailable" | "">("");
  const toggle = async (on: boolean) => {
    setBusy(on);
    setFailed("");
    if (!on) {
      const r = await api.prepareDevReact().catch(() => ({ ready: false, state: "unavailable" }));
      if (!r.ready) {
        setBusy(null);
        setFailed(r.state === "preparing" ? "preparing" : "unavailable");
        return;
      }
    }
    // On = back to the default (no cookie); off = development, remembered on this device
    document.cookie = on ? "gerdr_react=; path=/; max-age=0; samesite=lax" : "gerdr_react=development; path=/; max-age=315360000; samesite=lax";
    location.reload();
  };
  const state: "preparing" | "unavailable" | "" = failed || (!asked && (got === "preparing" || got === "unavailable") ? got : "");
  return (
    <div className="space-y-1">
      <Row label={t("settings.labs.prodReact")} hint={t("settings.labs.prodReact.hint")} local>
        <div className="flex shrink-0 items-center gap-2">
          {busy !== null && (
            <span className="flex items-center gap-1.5 text-[12px] text-muted-foreground">
              <Loader2 className="size-3.5 animate-spin" />
              {t("settings.labs.prodReact.switching")}
            </span>
          )}
          <Switch checked={busy ?? asked} disabled={busy !== null} onCheckedChange={toggle} />
        </div>
      </Row>
      {state && <div className="text-[12px] text-warn">{t(`settings.labs.prodReact.${state}`)}</div>}
    </div>
  );
}

function Row({ label, hint, local, children }: { label: string; hint?: string; local?: boolean; children: React.ReactNode }) {
  return (
    // data-setting: the search jumps here by label. Rounded with a little bleed so the flash on arrival has room
    <div data-setting={label} className="-mx-2 flex items-center justify-between gap-4 rounded-md px-2 py-0.5">
      <div>
        <div className="flex items-center gap-1.5 text-[13px]">
          {label}
          {local && <LocalTag />}
        </div>
        {hint && <div className="text-[12px] text-muted-foreground">{hint}</div>}
      </div>
      {children}
    </div>
  );
}
