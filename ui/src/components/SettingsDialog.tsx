import { Check, FolderOpen } from "lucide-react";
import { useEffect, useState } from "react";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Switch } from "@/components/ui/switch";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { isNative, nativeOpenPath } from "@/lib/native";
import { pluginStatus } from "@/lib/plugins";
import { closeDialog, fontSizeOf, set, setConfig, setFontSize, useStore, type Theme } from "@/lib/store";
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

// Settings (prefix+s / ⌘,). The general and plugins pages and labs live here; every other page is its own component file.
export function SettingsDialog({ open }: { open: boolean }) {
  const cfg = useStore((s) => s.config);
  const fontSize = useStore(fontSizeOf);
  const agents = useStore((s) => s.agents);
  const pluginsDir = useStore((s) => s.pluginsDir);
  const version = useStore((s) => s.version);
  useStore((s) => s.pluginVersion);
  const [tabOpen, setTabOpen] = useState("general");
  const remoteConns = useStore((s) => s.remoteConns.length);
  const doctorBad = useStore((s) => s.doctor?.level === "bad");
  // 从别处直接打开某一页（比如「去批准」→ 远程访问）
  const wantTab = useStore((s) => (s.dialog?.kind === "settings" ? s.dialog.tab : undefined));
  useEffect(() => {
    if (open && wantTab) setTabOpen(wantTab);
  }, [open, wantTab]);

  return (
    <Dialog open={open} onOpenChange={(o) => !o && closeDialog()}>
      <DialogContent className="max-h-[85vh] overflow-hidden p-0 sm:max-w-3xl">
        <Tabs defaultValue="general" value={tabOpen} onValueChange={setTabOpen} className="flex h-[75vh] min-w-0 flex-col">
          {/* 手机上整页缩一号（标题 15px、分页 13px、按钮矮一档）：组件默认的 18 / 14px 是照桌面定的，放进不到 400px 宽的弹窗里
              标题和按钮显得又大又挤（用户在安卓 App 里截图报的）。只在 max-md 下生效，桌面不动 */}
          <DialogHeader className="min-w-0 px-6 pt-5 max-md:gap-1 max-md:px-4 max-md:pt-3">
            <DialogTitle className="max-md:text-[15px]">设置</DialogTitle>
            {/* 八个分页在手机上一行放不下：横滑，不换行 */}
            <TabsList className="mt-2 max-w-full justify-start overflow-x-auto [scrollbar-width:none] max-md:mt-1 max-md:h-8 max-md:[&>button]:text-[13px]">
              <TabsTrigger value="general">通用</TabsTrigger>
              <TabsTrigger value="keys">快捷键</TabsTrigger>
              <TabsTrigger value="skills">Skill</TabsTrigger>
              <TabsTrigger value="mcp">MCP</TabsTrigger>
              <TabsTrigger value="plugins">插件</TabsTrigger>
              <TabsTrigger value="agents">Agent</TabsTrigger>
              <TabsTrigger value="memory">记忆</TabsTrigger>
              <TabsTrigger value="resources">资源</TabsTrigger>
              <TabsTrigger value="remote">
                远程访问
                {/* 有远程界面连着：数字红点（谁连着、连了多久在 tab 里面） */}
                {remoteConns > 0 && (
                  <span className="ml-1 inline-flex h-4 min-w-4 items-center justify-center rounded-full bg-bad px-1 text-[10px] leading-none font-medium text-white tabular-nums">
                    {remoteConns}
                  </span>
                )}
              </TabsTrigger>
              <TabsTrigger value="labs">
                实验室
                {/* the diagnosis found something broken */}
                {doctorBad && <span className="ml-1 size-1.5 rounded-full bg-bad" />}
              </TabsTrigger>
            </TabsList>
          </DialogHeader>
          <div className="min-h-0 min-w-0 flex-1 overflow-auto px-6 pb-6 max-md:px-4 max-md:[&_[data-slot=button]:not([data-size^=icon])]:h-7 max-md:[&_[data-slot=button]:not([data-size^=icon])]:px-2.5 max-md:[&_[data-slot=button]]:text-[13px]">
            <TabsContent value="general" className="space-y-6 pt-2">
              {/* 设置分两种：存 gerdr 服务端、所有设备共用（config.json），和只存这台设备（localStorage / 安卓壳）。
                  不分组的话在手机上调了一项，电脑上跟着变还是不变，用户猜不出来 */}
              <Section title="所有设备共用" hint="存在跑 gerdr 的电脑上，电脑、手机、别的浏览器打开都是这一份；其他各页的设置也都是共用的">
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
                <Row label="改文件时展开 diff" hint="新建 / 编辑文件的那一行默认展开改动；关掉只留一行（文件名和 +/- 行数），点开再看">
                  <Switch checked={cfg.expandEdits !== false} onCheckedChange={(v) => setConfig({ expandEdits: v })} />
                </Row>
                <Row label="显示思考" hint="agent 的思考过程单独成行、直接展开；关掉就和工具调用收在一组里，点开再看">
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
                <Row label="系统通知" hint="看不见的会话跑完了 / 在等你时">
                  <Switch checked={cfg.notifications} onCheckedChange={(v) => setConfig({ notifications: v })} />
                </Row>
                <ServiceSetting />
                <Row label="从 herdr 导入" hint="工作空间、tab、分屏和每个 agent 的会话">
                  <Button size="sm" variant="outline" onClick={() => set({ dialog: { kind: "importHerdr" } })}>
                    导入…
                  </Button>
                </Row>
              </Section>
              <Section title="只管这台设备" hint="存在这台设备上，别的设备各设各的">
                <Row label="字号">
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
                <AndroidSettings />
              </Section>
              <div className="pt-2 text-[12px] text-muted-foreground">gerdr {version}</div>
            </TabsContent>
            <TabsContent value="keys" className="pt-2">
              <KeySettings />
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
              <div className="flex items-start justify-between gap-4">
                <div className="text-[13px]">
                  插件放在 <code className="rounded-sm bg-muted px-1 font-mono text-[12px]">{pluginsDir}</code>，一个 .ts 文件一个，保存即生效。API 见同目录的{" "}
                  <code className="font-mono text-[12px]">gerdr.d.ts</code>。
                </div>
                {/* 只有 App 里能开本机目录；浏览器里（可能是远程连进来的）开不了，就不放这个按钮 */}
                {isNative() && (
                  <Button
                    size="sm"
                    variant="outline"
                    className="shrink-0"
                    disabled={!pluginsDir}
                    title={pluginsDir}
                    onClick={() => nativeOpenPath(pluginsDir, undefined, false)}
                  >
                    <FolderOpen className="size-3.5" /> 打开目录
                  </Button>
                )}
              </div>
              <div className="divide-y rounded-md border">
                {pluginStatus.length === 0 && <div className="p-3 text-[13px] text-muted-foreground">没有插件</div>}
                {pluginStatus.map((p) => (
                  <div key={p.id} className="p-3">
                    <div className="flex items-center gap-2 text-[13px]">
                      {p.loaded ? <Check className="size-3.5 text-ok" /> : <span className="size-2 rounded-full bg-bad" />}
                      <span className="shrink-0 font-medium">{p.id}</span>
                      <span className="truncate font-mono text-[11px] text-muted-foreground">{p.path}</span>
                    </div>
                    {p.error && <pre className="mt-1.5 whitespace-pre-wrap rounded-sm bg-bad/10 p-2 font-mono text-[11px] text-bad">{p.error}</pre>}
                  </div>
                ))}
              </div>
            </TabsContent>
            <TabsContent value="agents" className="pt-2">
              <AgentSettings />
            </TabsContent>
            <TabsContent value="memory" className="pt-2">
              <MemorySettings open={open && tabOpen === "memory"} />
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

function Section({ title, hint, children }: { title: string; hint: string; children: React.ReactNode }) {
  return (
    <section className="space-y-4">
      <div className="border-b pb-1.5">
        <div className="text-[12px] font-medium text-muted-foreground">{title}</div>
        <div className="text-[11px] text-muted-foreground/80">{hint}</div>
      </div>
      {children}
    </section>
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
