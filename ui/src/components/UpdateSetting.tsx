import { Loader2 } from "lucide-react";
import { useEffect, useRef, useState } from "react";
import { toast } from "sonner";
import { api } from "@/lib/api";
import { Button } from "@/components/ui/button";
import { cn } from "@/lib/utils";
import { Switch } from "@/components/ui/switch";
import { isNative, nativeApplyUpdate, nativeAskUpdate, nativeSetAutoUpdate, type AppUpdateStatus } from "@/lib/native";
import { set, useStore } from "@/lib/store";

// Settings → 通用 →「版本」: which gerdr is running and where the App's self-update is (desktop/updater.js, pushed
// by desktop/main.js updateStatus): downloading, waiting for agents to finish, upgrading. A downloaded backend
// update is applied by the App only when nothing would be cut off (docs/dev/UPDATE.md), so "waiting" says on what.
// Off the App (a phone over remote access, a browser) there's no shell: the App's own page relays its status and the
// buttons through the backend (server/appupdate.go), which works while the App has a window open on the computer.

export function UpdateSetting() {
  const version = useStore((s) => s.version);
  const u = useStore((s) => s.appUpdate);
  const [service, setService] = useState(false);

  useEffect(() => {
    if (!isNative()) {
      api
        .appUpdate()
        .then((r) => set({ appUpdate: r.status }))
        .catch(() => {});
      return;
    }
    nativeAskUpdate();
    api
      .service()
      .then((st) => setService(st.installed))
      .catch(() => {});
  }, []);

  const installedAt = useStore((s) => s.installedAt);
  const running = u?.running || version;
  // When it was installed, not just which commit: a dirty-tree build keeps the same version string for days
  const when = installedAt
    ? ` · 装于 ${new Date(installedAt).toLocaleString("zh-CN", { month: "numeric", day: "numeric", hour: "2-digit", minute: "2-digit" })}（${since(installedAt)}）`
    : "";
  const shown = (u && u.app !== running ? `Gerdr ${running} · App ${u.app}` : `Gerdr ${running}`) + when;
  const { text, tone } = u ? describe(u, service) : { text: "", tone: "" };
  const p = u?.pending;
  const native = isNative();
  const checking = useChecking(u);
  const ask = () => {
    checking.start();
    if (native) nativeAskUpdate(true);
    else relay("check").catch(checking.stop);
  };
  const apply = native ? nativeApplyUpdate : () => relay("apply").catch(() => {});
  const setAuto = native ? nativeSetAutoUpdate : (on: boolean) => relay("auto", on).catch(() => {});

  return (
    <>
      <div className="flex items-center justify-between gap-4">
        <div className="min-w-0">
          <div className="text-[13px]">版本</div>
          <div className="text-[12px] text-muted-foreground">{shown}</div>
          {text && <div className={cn("text-[12px]", tone || "text-muted-foreground")}>{text}</div>}
          {!u && !native && <div className="text-[12px] text-muted-foreground">电脑上的 Gerdr App 开着窗口时，这里能检查和安装更新</div>}
        </div>
        {u?.enabled &&
          (p ? (
            <Button size="sm" disabled={u.phase === "applying"} onClick={apply}>
              现在更新
            </Button>
          ) : (
            <Button size="sm" variant="outline" disabled={u.phase !== "idle" || checking.on} onClick={ask}>
              {(u.phase === "checking" || checking.on) && <Loader2 className="size-3.5 animate-spin" />}
              检查更新
            </Button>
          ))}
      </div>
      {/* Only from a shell that knows the switch (u.auto present): an older one would ignore it */}
      {u?.enabled && typeof u.auto === "boolean" && (
        <div className="flex items-center justify-between gap-4">
          <div>
            <div className="text-[13px]">自动更新</div>
            <div className="text-[12px] text-muted-foreground">
              {u.auto ? "自己检查、下载，没有 agent 在跑、没开终端时换上" : "不自己检查和下载：点「检查更新」下好后，再点「现在更新」"}
            </div>
          </div>
          <Switch checked={u.auto} onCheckedChange={setAuto} />
        </div>
      )}
    </>
  );
}

/** Off the App: the backend hands it to the App's page, whose next status report shows what happened. */
function relay(op: "check" | "apply" | "auto", on?: boolean) {
  return api.appUpdateOp(op, on).catch((e) => {
    toast.error((e as Error).message);
    throw e;
  });
}

/**
 * "检查更新" spins from the click until that check's answer is in: the shell's "checking" phase alone lasts a few
 * hundred ms, and off the App it comes through a relay, so it would mostly never show. Done = checkedAt or the error
 * moved, a download started, or it went through "checking" and back (a repeat of the same failure moves neither);
 * at least 600ms so it doesn't just flicker, at most 30s in case the answer is lost.
 */
function useChecking(u: AppUpdateStatus | null) {
  const [on, setOn] = useState(false);
  const from = useRef({ at: 0, checkedAt: 0, error: "", saw: false });
  const stop = () => setOn(false);
  useEffect(() => {
    if (!on || !u) return;
    const f = from.current;
    if (u.phase === "checking") f.saw = true;
    const done = u.checkedAt !== f.checkedAt || u.error !== f.error || u.phase === "downloading" || !!u.pending || (f.saw && u.phase === "idle");
    if (!done) return;
    const t = setTimeout(stop, Math.max(0, f.at + 600 - Date.now()));
    return () => clearTimeout(t);
  }, [on, u]);
  useEffect(() => {
    if (!on) return;
    const t = setTimeout(stop, 30_000);
    return () => clearTimeout(t);
  }, [on]);
  return {
    on,
    stop,
    start: () => {
      from.current = { at: Date.now(), checkedAt: u?.checkedAt ?? 0, error: u?.error ?? "", saw: false };
      setOn(true);
    },
  };
}

function describe(u: AppUpdateStatus, service: boolean): { text: string; tone: string } {
  const p = u.pending;
  if (u.phase === "applying" && p) return { text: `正在升级到 ${p.version}…`, tone: "text-brand" };
  if (u.phase === "downloading") return { text: `正在下载 ${u.downloading}（${u.progress}%）`, tone: "" };
  if (u.phase === "checking") return { text: "正在检查更新…", tone: "" };
  if (p?.kind === "app") return { text: `有升级：${p.version}。这次要换整个 App，更新时会退出再打开`, tone: "text-brand" };
  if (p) {
    const b = u.busy;
    const who = service ? "后台服务" : "gerdr";
    if (!b) return { text: `有升级：${p.version}`, tone: "text-brand" };
    const wait: string[] = [];
    if (b.turns) wait.push(`${b.turns} 个 agent 跑完`);
    if (b.waiting) wait.push(`${b.waiting} 个请求被批准`);
    if (b.tasks) wait.push(`${b.tasks} 个后台任务结束`);
    if (b.terms) wait.push(`${b.terms} 个终端关掉`);
    if (u.auto === false) return { text: `有升级：${p.version}，点「现在更新」换上`, tone: "text-brand" };
    if (!wait.length) return { text: `有升级：${p.version}，马上自动升级`, tone: "text-brand" };
    return { text: `${who}等待升级到 ${p.version}：等 ${wait.join("、")}`, tone: "text-warn" };
  }
  if (u.error) return { text: `检查更新失败：${u.error}`, tone: "text-bad" };
  if (!u.enabled) return { text: "这个构建不自动更新（开发版或设了 GERDR_NO_UPDATE）", tone: "" };
  if (!u.checkedAt) return { text: "", tone: "" };
  const at = new Date(u.checkedAt).toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" });
  return { text: `已是最新（${at} 检查过）`, tone: "" };
}

function since(t: number) {
  const m = Math.max(0, Math.floor((Date.now() - t) / 60000));
  if (m < 1) return "刚刚";
  if (m < 60) return `${m} 分钟前`;
  if (m < 1440) return `${Math.floor(m / 60)} 小时前`;
  return `${Math.floor(m / 1440)} 天前`;
}
