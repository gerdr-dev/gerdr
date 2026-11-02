import { useEffect, useState } from "react";
import { api, type ServiceStatus } from "@/lib/api";
import { Switch } from "@/components/ui/switch";
import { t } from "@/lib/i18n";

// 设置 → 通用 →「后台常驻」：打开后 gerdr 装成 launchd 的后台服务，⌘Q 退出 App 会话照跑、手机远程照样连得上，登录就起。
// On by default: the App installs it at start unless it was turned off here (service.OptedOut).
// 开了就**始终由后台服务跑**，App 只是个窗口：打开的那一下 App 起的后端让位（重启一次，正在跑的 agent 会被打断），
// 关掉时后台停掉、App 的后端接上（docs/dev/SERVICE.md）。远程那条路上拿不到（接口 403），整行不画。

export function ServiceSetting() {
  const [st, setSt] = useState<ServiceStatus | null>(null);
  const [err, setErr] = useState("");
  const [busy, setBusy] = useState(false);

  const load = () =>
    api
      .service()
      .then(setSt)
      .catch(() => setSt(null));
  useEffect(() => {
    load();
  }, []);

  if (!st || st.why.includes("macOS")) return null;
  const on = st.installed;
  const hint = st.why
    ? st.why
    : !on
      ? t("serviceSetting.off")
      : st.self
        ? t("serviceSetting.running", { pid: st.pid, log: st.log })
        : st.pid > 0
          ? t("serviceSetting.other")
          : t("serviceSetting.down", { log: st.log });

  const toggle = async (v: boolean) => {
    setErr("");
    setBusy(true);
    try {
      await api.setService(v);
    } catch (e) {
      setErr((e as Error).message);
    }
    setBusy(false);
    // 两个方向都会换一个后端来答话（打开：App 的后端让位给后台；关掉：后台停了、App 的后端接上），要一两秒
    setTimeout(load, 2500);
  };

  return (
    <div className="flex items-center justify-between gap-4">
      <div className="min-w-0">
        <div className="text-[13px]">{t("serviceSetting.title")}</div>
        <div className="text-[12px] break-all text-muted-foreground">{hint}</div>
        {err && <div className="text-[12px] text-bad">{err}</div>}
      </div>
      <Switch checked={on} disabled={busy || !!st.why} onCheckedChange={toggle} />
    </div>
  );
}
