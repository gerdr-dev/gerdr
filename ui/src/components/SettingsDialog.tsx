import { useEffect, useState } from "react";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { lightboxOpen } from "@/components/Lightbox";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Switch } from "@/components/ui/switch";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { isAndroid } from "@/lib/android";
import { closeDialog, fontSizeOf, set, setConfig, setFontSize, useStore, type Theme } from "@/lib/store";
import { cn } from "@/lib/utils";
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
import { UIUpgradeSetting } from "./UIUpgradeSetting";
import { UpdateSetting } from "./UpdateSetting";

// Pages that became sections of the Agent page (what an agent can use): old links (a Work app's openSettings("mcp"),
// older code) still land on them.
const AGENT_SECTIONS = ["roles", "skills", "mcp", "memory"];

// Settings (prefix+s / ⌘,). The general page and labs live here; every other page is its own component file.
export function SettingsDialog({ open }: { open: boolean }) {
  const cfg = useStore((s) => s.config);
  const bat = cfg.batteryAlert ?? { on: true, percent: 20 };
  const fontSize = useStore(fontSizeOf);
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
            <DialogTitle className="max-md:text-[15px]">设置</DialogTitle>
            {/* The tabs don't fit one row on a phone: swipe sideways, no wrapping. overflow-x-auto makes y auto too, and Android's
                WebView ignores scrollbar-width, so it drew both scrollbars (a bar under the tabs, a white corner square) */}
            <TabsList className="mt-2 max-w-full justify-start overflow-x-auto overflow-y-hidden [scrollbar-width:none] [&::-webkit-scrollbar]:hidden max-md:mt-1 max-md:h-8 max-md:[&>button]:text-[13px]">
              <TabsTrigger value="general">通用</TabsTrigger>
              <TabsTrigger value="keys">快捷键</TabsTrigger>
              <TabsTrigger value="agents">Agent</TabsTrigger>
              <TabsTrigger value="addons">扩展</TabsTrigger>
              <TabsTrigger value="remote">
                远程访问
                {/* 有远程界面连着：数字红点（谁连着、连了多久在 tab 里面） */}
                {remoteConns > 0 && (
                  <span className="ml-1 inline-flex h-4 min-w-4 items-center justify-center rounded-full bg-bad px-1 text-[10px] leading-none font-medium text-white tabular-nums">
                    {remoteConns}
                  </span>
                )}
              </TabsTrigger>
              <TabsTrigger value="resources">资源</TabsTrigger>
              <TabsTrigger value="labs">
                实验室
                {/* the diagnosis found something broken */}
                {doctorBad && <span className="ml-1 size-1.5 rounded-full bg-bad" />}
              </TabsTrigger>
            </TabsList>
          </DialogHeader>
          {/* One size for the whole panel. Text nobody sized is 13px (section titles are 13px, descriptions 12px); shadcn's controls
              default to 14px text and 36px height (Input also sets md:text-sm), which looked oversized next to that, so on desktop
              every button / input / textarea / select here is 12px and the default-sized ones 32px tall, like size="sm". Pages can
              still size a control themselves; icon buttons keep their size. */}
          <div className="min-h-0 min-w-0 flex-1 overflow-auto px-6 pb-6 text-[13px] max-md:px-4 max-md:[&_[data-slot=button]:not([data-size^=icon])]:h-7 max-md:[&_[data-slot=button]:not([data-size^=icon])]:px-2.5 max-md:[&_[data-slot=button]]:text-[13px] md:[&_[data-slot=button]]:text-[12px] md:[&_[data-slot=button][data-size=default]]:h-8 md:[&_[data-slot=button][data-size=default]]:px-3 md:[&_[data-slot=input]]:h-8 md:[&_[data-slot=input]]:text-[12px] md:[&_[data-slot=textarea]]:text-[12px] md:[&_[data-slot=select-trigger]]:text-[12px] md:[&_[data-slot=select-trigger][data-size=default]]:h-8">
            <TabsContent value="general" className="space-y-6 pt-2">
              {/* Grouped by what a setting is about. Where it's stored (gerdr's config.json, shared by every device, or this
                  device only: localStorage / the Android shell) is a tag on the few device-only rows: grouping by storage put
                  font size a screen away from the theme (and without saying it, people can't guess whether a change on the
                  phone follows them to the computer). */}
              <div className="text-[12px] text-muted-foreground">
                设置存在跑 gerdr 的电脑上，电脑、手机、别的浏览器打开都是这一份；标了 <LocalTag /> 的只管这台设备。
              </div>
              <Section title="外观">
                <Row label="主题">
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
                <Row label="字号" local>
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
              </Section>
              <Section title="对话">
                <Row label="发送键" hint="另一种组合换行；手机上选「回车」时键盘的回车键直接发送">
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
                <Row label="默认 agent" hint="新会话先选中它">
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
                <Row label="改文件时展开 diff" hint="新建 / 编辑文件的那一行默认展开改动；关掉只留一行（文件名和 +/- 行数），点开再看">
                  <Switch checked={cfg.expandEdits !== false} onCheckedChange={(v) => setConfig({ expandEdits: v })} />
                </Row>
                <Row label="显示思考" hint="Agent 的思考过程单独成行、直接展开。Claude 模型建议打开。">
                  <Switch checked={cfg.showThinking !== false} onCheckedChange={(v) => setConfig({ showThinking: v })} />
                </Row>
                <Row label="打开链接前确认" hint="点对话里的外部链接先弹框，显示完整地址，可以复制或打开；关掉就直接打开">
                  <Switch checked={cfg.confirmLinks !== false} onCheckedChange={(v) => setConfig({ confirmLinks: v })} />
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
              </Section>
              <Section title="通知">
                <Row label="系统通知" hint="看不见的会话跑完了 / 在等你时">
                  <Switch checked={cfg.notifications} onCheckedChange={(v) => setConfig({ notifications: v })} />
                </Row>
                <Row label="电量低提醒" hint="运行 gerdr 的电脑用电池、电量低于这个数时放一条进收件箱（每次放电只提醒一次）">
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
                <Section title="这台手机" local>
                  <AndroidSettings />
                </Section>
              )}
              {/* The gerdr on the computer running it: which version, updating it (App only), the background service (macOS) */}
              <Section title="版本与后台">
                <UpdateSetting />
                <UIUpgradeSetting />
                <ServiceSetting />
              </Section>
              <Section title="导入">
                <Row label="从 herdr 导入" hint="工作空间、tab、分屏和每个 agent 的会话">
                  <Button size="sm" variant="outline" onClick={() => set({ dialog: { kind: "importHerdr" } })}>
                    导入…
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
                    角色
                  </TabsTrigger>
                  <TabsTrigger value="skills" className="flex-none px-3">
                    Skill
                  </TabsTrigger>
                  <TabsTrigger value="mcp" className="flex-none px-3">
                    MCP
                  </TabsTrigger>
                  <TabsTrigger value="memory" className="flex-none px-3">
                    记忆
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
              <div className="text-[13px] text-muted-foreground">还在试的功能，默认关着。</div>
              <Row label="对话虚拟滚动" hint="只渲染屏幕附近的消息，很长的对话更省内存；往上翻时带图的消息可能会跳动">
                <Switch checked={!!cfg.virtualList} onCheckedChange={(v) => setConfig({ virtualList: v })} />
              </Row>
              <Row label="选中即复制" hint="和 herdr 一样：拖着选中文字，松开鼠标就复制到剪贴板；双击选词也会复制。对话和终端里都管用，输入框里不复制">
                <Switch checked={!!cfg.copyOnSelect} onCheckedChange={(v) => setConfig({ copyOnSelect: v })} />
              </Row>
              <Row
                label="远程连着时不休眠"
                hint="手机等远程设备连着时电脑不睡，合上盖子也醒着（和 Amphetamine 一样），断开 15 分钟后恢复；下面的手动时段也合盖不睡。合盖要授权一次，用电池且电量 ≤ 20% 时自动放手，塞包里会发热。仅 macOS"
              >
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

function Section({ title, hint, local, children }: { title: string; hint?: string; local?: boolean; children: React.ReactNode }) {
  return (
    <section className="space-y-4">
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
  return <span className={cn("rounded-sm border px-1 py-px text-[10px] font-normal leading-none text-muted-foreground", className)}>仅本设备</span>;
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
