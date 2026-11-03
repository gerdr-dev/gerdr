import { useEffect, useState } from "react";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { lightboxOpen } from "@/components/Lightbox";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Switch } from "@/components/ui/switch";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { isAndroid } from "@/lib/android";
import { closeDialog, fontSizeOf, set, setConfig, setFontSize, setPref, useStore, type Theme } from "@/lib/store";
import { cn } from "@/lib/utils";
import { LoaderSelect } from "./LoaderSelect";
import { languagePref, setLanguage, t, type LanguagePref } from "@/lib/i18n";
import { AddonSettings } from "./AddonSettings";
import { AgentSettings } from "./AgentSettings";
import { AndroidSettings } from "./AndroidSettings";
import { AwakeSetting } from "./AwakeSetting";
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

// Pages that became sections of the Agent page (what an agent can use): old links (a Work app's openSettings("mcp"),
// older code) still land on them.
const AGENT_SECTIONS = ["roles", "skills", "mcp", "memory"];

// Settings (prefix+s / ⌘,). The general page and labs live here; every other page is its own component file.
export function SettingsDialog({ open }: { open: boolean }) {
  const cfg = useStore((s) => s.config);
  const bat = cfg.batteryAlert ?? { on: true, percent: 20 };
  const inb = cfg.inbox ?? {};
  const fontSize = useStore(fontSizeOf);
  // The settings each device keeps for itself (DEVICE_KEYS): its own value, else the shared one
  const device = useStore((s) => s.device);
  const dev = { ...cfg, ...device };
  const agents = useStore((s) => s.agents);
  const [tabOpen, setTabOpen] = useState("general");
  const [agentTab, setAgentTab] = useState("roles");
  const remoteConns = useStore((s) => s.remoteConns.length);
  const doctorBad = useStore((s) => s.doctor?.level === "bad");
  // 从别处直接打开某一页（比如「去批准」→ 远程访问）
  const wantTab = useStore((s) => (s.dialog?.kind === "settings" ? s.dialog.tab : undefined));
  useEffect(() => {
    if (!open || !wantTab) return;
    if (AGENT_SECTIONS.includes(wantTab)) {
      setTabOpen("agents");
      setAgentTab(wantTab);
    } else if (wantTab === "inbox") {
      // Not a page of its own, a section far down General: the inbox's 设置 button landing on the top of General
      // left people wondering what it had to do with the inbox
      setTabOpen("general");
      requestAnimationFrame(() => document.getElementById("settings-inbox")?.scrollIntoView({ block: "start" }));
    } else setTabOpen(wantTab);
  }, [open, wantTab]);
  const agentOpen = open && tabOpen === "agents";

  return (
    <Dialog open={open} onOpenChange={(o) => !o && closeDialog()}>
      <DialogContent
        className="max-h-[85vh] overflow-hidden p-0 sm:max-w-3xl"
        // The lightbox (an add-on's screenshots) sits above this dialog but outside it: clicking or pressing Esc
        // there is for the lightbox, not a reason to close Settings
        onPointerDownOutside={(e) => lightboxOpen() && e.preventDefault()}
        onEscapeKeyDown={(e) => lightboxOpen() && e.preventDefault()}
      >
        <Tabs defaultValue="general" value={tabOpen} onValueChange={setTabOpen} className="flex h-[75vh] min-w-0 flex-col">
          {/* 手机上整页缩一号（标题 15px、分页 13px、按钮矮一档）：组件默认的 18 / 14px 是照桌面定的，放进不到 400px 宽的弹窗里
              标题和按钮显得又大又挤（用户在安卓 App 里截图报的）。只在 max-md 下生效，桌面不动 */}
          <DialogHeader className="min-w-0 px-6 pt-5 max-md:gap-1 max-md:px-4 max-md:pt-3">
            <DialogTitle className="max-md:text-[15px]">{t("settings.title")}</DialogTitle>
            {/* The tabs don't fit one row on a phone: swipe sideways, no wrapping. overflow-x-auto makes y auto too, and Android's
                WebView ignores scrollbar-width, so it drew both scrollbars (a bar under the tabs, a white corner square) */}
            <TabsList className="mt-2 max-w-full justify-start overflow-x-auto overflow-y-hidden [scrollbar-width:none] [&::-webkit-scrollbar]:hidden max-md:mt-1 max-md:h-8 max-md:[&>button]:text-[13px]">
              <TabsTrigger value="general">{t("settings.tab.general")}</TabsTrigger>
              <TabsTrigger value="keys">{t("settings.tab.keys")}</TabsTrigger>
              <TabsTrigger value="agents">Agent</TabsTrigger>
              <TabsTrigger value="addons">{t("settings.tab.addons")}</TabsTrigger>
              <TabsTrigger value="remote">
                {t("settings.tab.remote")}
                {/* 有远程界面连着：数字红点（谁连着、连了多久在 tab 里面） */}
                {remoteConns > 0 && (
                  <span className="ml-1 inline-flex h-4 min-w-4 items-center justify-center rounded-full bg-bad px-1 text-[10px] leading-none font-medium text-white tabular-nums">
                    {remoteConns}
                  </span>
                )}
              </TabsTrigger>
              <TabsTrigger value="resources">{t("settings.tab.resources")}</TabsTrigger>
              <TabsTrigger value="labs">
                {t("settings.tab.labs")}
                {/* the diagnosis found something broken */}
                {doctorBad && <span className="ml-1 size-1.5 rounded-full bg-bad" />}
              </TabsTrigger>
            </TabsList>
          </DialogHeader>
          {/* One size for the whole panel. Text nobody sized is 13px (section titles are 13px, descriptions 12px); shadcn's controls
              default to 14px text and 36px height (Input also sets md:text-sm), which looked oversized next to that, so on desktop
              every button / input / textarea / select here is 12px and the default-sized ones 32px tall, like size="sm". Pages can
              still size a control themselves; icon buttons keep their size. On a phone buttons are 28px with 13px text, and inputs /
              selects are 28px too so a button next to an input lines up (inputs keep 16px text there). */}
          <div className="min-h-0 min-w-0 flex-1 overflow-auto px-6 pb-6 text-[13px] max-md:px-4 max-md:[&_[data-slot=button]:not([data-size^=icon])]:h-7 max-md:[&_[data-slot=button]:not([data-size^=icon])]:px-2.5 max-md:[&_[data-slot=button]]:text-[13px] max-md:[&_[data-slot=input]]:h-7 max-md:[&_[data-slot=input]]:py-0 max-md:[&_[data-slot=select-trigger][data-size=default]]:h-7 md:[&_[data-slot=button]]:text-[12px] md:[&_[data-slot=button][data-size=default]]:h-8 md:[&_[data-slot=button][data-size=default]]:px-3 md:[&_[data-slot=input]]:h-8 md:[&_[data-slot=input]]:text-[12px] md:[&_[data-slot=textarea]]:text-[12px] md:[&_[data-slot=select-trigger]]:text-[12px] md:[&_[data-slot=select-trigger][data-size=default]]:h-8">
            <TabsContent value="general" className="space-y-6 pt-2">
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
                <Row label={t("settings.showThinking")} hint={t("settings.showThinking.hint")}>
                  <Switch checked={cfg.showThinking !== false} onCheckedChange={(v) => setConfig({ showThinking: v })} />
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
              {/* Only in the Android App (its shell keeps these): notifications in the background, volume keys, server */}
              {isAndroid() && (
                <Section title={t("settings.thisPhone")} local>
                  <AndroidSettings />
                </Section>
              )}
              {/* The gerdr on the computer running it: which version, updating it (App only), the background service (macOS) */}
              <Section title={t("settings.versions")}>
                <UpdateSetting />
                <UIUpgradeSetting />
                <ServiceSetting />
                <RestartSetting />
              </Section>
              <Section title={t("settings.import")}>
                <Row label={t("settings.import.herdr")} hint={t("settings.import.herdr.hint")}>
                  <Button size="sm" variant="outline" onClick={() => set({ dialog: { kind: "importHerdr" } })}>
                    {t("settings.import.button")}
                  </Button>
                </Row>
              </Section>
            </TabsContent>
            <TabsContent value="keys" className="pt-2">
              <KeySettings />
            </TabsContent>
            <TabsContent value="resources" className="pt-2">
              <ResourceStats open={open && tabOpen === "resources"} />
            </TabsContent>
            <TabsContent value="addons" className="pt-2">
              <AddonSettings open={open && tabOpen === "addons"} />
            </TabsContent>
            {/* What an agent is and what it can use: roles, Skill, MCP, memory. Were four top-level pages; one page with
                sections keeps the top row short enough to read (ten tabs didn't fit, and these four belong together) */}
            <TabsContent value="agents" className="pt-2">
              <Tabs value={agentTab} onValueChange={setAgentTab} className="gap-4">
                <TabsList variant="line" className="h-8 border-b w-full justify-start rounded-none p-0">
                  <TabsTrigger value="roles" className="flex-none px-3">
                    {t("settings.agent.roles")}
                  </TabsTrigger>
                  <TabsTrigger value="skills" className="flex-none px-3">
                    Skill
                  </TabsTrigger>
                  <TabsTrigger value="mcp" className="flex-none px-3">
                    MCP
                  </TabsTrigger>
                  <TabsTrigger value="memory" className="flex-none px-3">
                    {t("settings.agent.memory")}
                  </TabsTrigger>
                </TabsList>
                <TabsContent value="roles">
                  <AgentSettings />
                </TabsContent>
                <TabsContent value="skills">
                  <SkillSettings open={agentOpen && agentTab === "skills"} />
                </TabsContent>
                <TabsContent value="mcp">
                  <MCPSettings open={agentOpen && agentTab === "mcp"} />
                </TabsContent>
                <TabsContent value="memory">
                  <MemorySettings open={agentOpen && agentTab === "memory"} />
                </TabsContent>
              </Tabs>
            </TabsContent>
            <TabsContent value="labs" className="space-y-4 pt-2">
              <div className="text-[13px] text-muted-foreground">{t("settings.labs.intro")}</div>
              <Row label={t("settings.labs.virtualList")} hint={t("settings.labs.virtualList.hint")} local>
                <Switch checked={!!dev.virtualList} onCheckedChange={(v) => setPref({ virtualList: v })} />
              </Row>
              <Row label={t("settings.labs.copyOnSelect")} hint={t("settings.labs.copyOnSelect.hint")} local>
                <Switch checked={!!dev.copyOnSelect} onCheckedChange={(v) => setPref({ copyOnSelect: v })} />
              </Row>
              <Row label={t("settings.labs.keepAwake")} hint={t("settings.labs.keepAwake.hint")}>
                <Switch checked={!!cfg.keepAwakeRemote} onCheckedChange={(v) => setConfig({ keepAwakeRemote: v })} />
              </Row>
              <AwakeSetting open={open && tabOpen === "labs"} />
              <div className="border-t pt-4">
                <DoctorPanel />
              </div>
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

function Row({ label, hint, local, children }: { label: string; hint?: string; local?: boolean; children: React.ReactNode }) {
  return (
    <div className="flex items-center justify-between gap-4">
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
