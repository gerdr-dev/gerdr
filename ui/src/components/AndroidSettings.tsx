import { useEffect, useState } from "react";
import { Loader2 } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Switch } from "@/components/ui/switch";
import { androidCan, isAndroid, isIOSApp, onAndroid, openInBrowser, postAndroid, useLanRoute, type AndroidUpdate } from "@/lib/android";
import { cn } from "@/lib/utils";
import { locale, t } from "@/lib/i18n";

/**
 * 安卓 App 里才有的那几项（设置 → 通用）：后台通知、音量键、走的哪条路、换服务器。状态全在壳那边（android/），
 * 这里只画、只转发点击 —— 网页的系统通知在 WebView 里用不了（没有 Notification），而且只在页面开着时有效；
 * 壳里是一个前台服务长连服务器（NoticeService），App 关着 / 锁屏也收得到。
 */
export function AndroidSettings() {
  const [n, setN] = useState<{ on: boolean; perm: boolean; battery: boolean } | null>(null);
  const [vol, setVol] = useState<boolean | null>(null);
  const [up, setUp] = useState<AndroidUpdate | null>(null);
  const route = useRoute();

  useEffect(() => {
    if (!isAndroid()) return;
    const off1 = onAndroid("notify", (m) => setN({ on: !!m.on, perm: !!m.perm, battery: !!m.battery }));
    const off2 = onAndroid("volkeys", (m) => setVol(!!m.on));
    const off3 = onAndroid("update", (m) => setUp(m as unknown as AndroidUpdate));
    postAndroid({ t: "notify", op: "get" });
    postAndroid({ t: "volkeys", op: "get" });
    if (androidCan("update")) postAndroid({ t: "update", op: "get" });
    return () => {
      off1();
      off2();
      off3();
    };
  }, []);

  if (isIOSApp()) return <IOSSettings />;
  if (!isAndroid()) return null;
  const live = n?.on && n.perm;
  return (
    <div className="space-y-4">
      <div className="flex items-center justify-between gap-4">
        <div>
          <div className="text-[13px]">{t("androidSettings.title")}</div>
          <div className="text-[12px] text-muted-foreground">
            <span className={cn("mr-1.5 inline-block size-2 rounded-full align-middle", route.via === "lan" ? "bg-ok" : "bg-brand")} />
            {route.detail}
          </div>
        </div>
        <Button size="sm" variant="outline" onClick={() => postAndroid({ t: "setup" })}>
          {t("androidSettings.switchServer")}
        </Button>
      </div>

      {up && <UpdateRow u={up} />}

      {n && (
        <div>
          <div className="flex items-center justify-between gap-4">
            <div>
              <div className="text-[13px]">{t("androidSettings.notify")}</div>
              <div className="text-[12px] text-muted-foreground">
                {!n.on ? t("androidSettings.notifyOff") : !n.perm ? t("androidSettings.notifyNoPerm") : t("androidSettings.notifyOn")}
              </div>
            </div>
            <Switch checked={n.on} onCheckedChange={(v) => postAndroid({ t: "notify", op: "set", on: v })} />
          </div>
          <div className="mt-2 flex flex-wrap gap-2">
            <Button size="sm" variant="outline" onClick={() => postAndroid({ t: "notify", op: "test" })}>
              {t("androidSettings.test")}
            </Button>
            {live && !n.battery && (
              <Button size="sm" variant="outline" onClick={() => postAndroid({ t: "notify", op: "battery" })}>
                {t("androidSettings.allowBackground")}
              </Button>
            )}
          </div>
          {live && (
            <p className="mt-1.5 text-[12px] leading-relaxed text-muted-foreground">
              {n.battery ? t("androidSettings.batteryOk") : t("androidSettings.batteryHint")}
              {t("androidSettings.oemHint")}
            </p>
          )}
        </div>
      )}

      {vol !== null && (
        <div className="flex items-center justify-between gap-4">
          <div>
            <div className="text-[13px]">{t("androidSettings.volKeys")}</div>
            <div className="text-[12px] text-muted-foreground">{t("androidSettings.volKeysHint")}</div>
          </div>
          <Switch checked={vol} onCheckedChange={(v) => postAndroid({ t: "volkeys", op: "set", on: v })} />
        </div>
      )}
    </div>
  );
}

/**
 * The App's own version and self-update (AppUpdate.kt): checked against GitHub when the App comes to the front
 * (every 6 h at most) and downloaded by itself; installing takes a tap because Android asks.
 */
function UpdateRow({ u }: { u: AndroidUpdate }) {
  const busy = u.phase === "checking" || u.phase === "downloading" || u.phase === "installing";
  const text =
    u.phase === "checking"
      ? t("systemUpdate.checking")
      : u.phase === "downloading"
        ? t("systemUpdate.downloading", { name: u.latest, progress: u.progress })
        : u.phase === "installing"
          ? t("androidSettings.installing", { version: u.latest })
          : u.phase === "ready"
            ? t("androidSettings.ready", { version: u.latest })
            : u.error
              ? t("systemUpdate.checkFailed", { error: u.error })
              : u.checkedAt
                ? t("systemUpdate.upToDate", {
                    time: new Date(u.checkedAt).toLocaleString(locale() === "zh" ? "zh-CN" : "en", {
                      month: "numeric",
                      day: "numeric",
                      hour: "2-digit",
                      minute: "2-digit",
                    }),
                  })
                : t("androidSettings.checkSchedule");
  return (
    <div className="flex items-center justify-between gap-4">
      <div className="min-w-0">
        <div className="text-[13px]">{t("androidSettings.appVersion", { version: u.version })}</div>
        <div className={cn("text-[12px]", u.phase === "ready" ? "text-brand" : u.error && u.phase === "idle" ? "text-bad" : "text-muted-foreground")}>
          {text}
        </div>
      </div>
      {u.phase === "ready" ? (
        <Button size="sm" onClick={() => postAndroid({ t: "update", op: "install" })}>
          {t("common.install")}
        </Button>
      ) : (
        <Button size="sm" variant="outline" disabled={busy} onClick={() => postAndroid({ t: "update", op: "check" })}>
          {busy && <Loader2 className="size-3.5 animate-spin" />}
          {t("systemUpdate.check")}
        </Button>
      )}
    </div>
  );
}

/** The iOS App's shell (ios/): the server and, on iOS 17+, which way it reaches it. No notifications or update yet. */
function IOSSettings() {
  const route = useRoute();
  const lan = useLanRoute();
  return (
    <div className="flex items-center justify-between gap-4">
      <div>
        <div className="text-[13px]">{t("androidSettings.iosTitle")}</div>
        {lan && (
          <div className="text-[12px] text-muted-foreground">
            <span className={cn("mr-1.5 inline-block size-2 rounded-full align-middle", route.via === "lan" ? "bg-ok" : "bg-brand")} />
            {route.detail}
          </div>
        )}
      </div>
      <div className="flex shrink-0 gap-2">
        <Button size="sm" variant="outline" onClick={openInBrowser}>
          {t("androidSettings.openSafari")}
        </Button>
        <Button size="sm" variant="outline" onClick={() => postAndroid({ t: "setup" })}>
          {t("androidSettings.switchServer")}
        </Button>
      </div>
    </div>
  );
}

/**
 * 「现在走的是局域网直连还是中继」。按壳报的**此刻开着的**连接算（LanRoute.kt），不按「新连接往哪儿拨」：
 * 刚切到局域网时 WebSocket 那条长连接还在中继上，按新连接画就是「直连」，而最在意的那条其实还绕着公网。
 * 两条路都有（刚切过去）就算中继 —— 说的是最慢的那条。
 */
export function useRoute(): { via: "lan" | "relay"; detail: string } {
  const [r, setR] = useState<{ lan: number; relay: number; next: string; proxied: boolean } | null>(null);
  useEffect(
    () => onAndroid("route", (m) => setR({ lan: Number(m.lan) || 0, relay: Number(m.relay) || 0, next: String(m.next ?? ""), proxied: !!m.proxied })),
    [],
  );
  const relay = { via: "relay" as const, detail: t("androidSettings.routeRelay") };
  const lan = { via: "lan" as const, detail: t("androidSettings.routeLan") };
  if (!r?.proxied) return relay;
  if (r.relay > 0 && r.lan > 0) return { via: "relay", detail: t("androidSettings.routeSwitching") };
  if (r.relay > 0) return relay;
  if (r.lan > 0) return lan;
  return r.next === "lan" ? lan : relay;
}
