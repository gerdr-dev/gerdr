import { useEffect, useState } from "react";
import { Loader2 } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Switch } from "@/components/ui/switch";
import { androidCan, isAndroid, onAndroid, postAndroid, type AndroidUpdate } from "@/lib/android";
import { cn } from "@/lib/utils";

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

  if (!isAndroid()) return null;
  const live = n?.on && n.perm;
  return (
    <div className="space-y-4">
      <div className="flex items-center justify-between gap-4">
        <div>
          <div className="text-[13px]">安卓 App</div>
          <div className="text-[12px] text-muted-foreground">
            <span className={cn("mr-1.5 inline-block size-2 rounded-full align-middle", route.via === "lan" ? "bg-ok" : "bg-brand")} />
            {route.detail}
          </div>
        </div>
        <Button size="sm" variant="outline" onClick={() => postAndroid({ t: "setup" })}>
          换服务器
        </Button>
      </div>

      {up && <UpdateRow u={up} />}

      {n && (
        <div>
          <div className="flex items-center justify-between gap-4">
            <div>
              <div className="text-[13px]">后台通知</div>
              <div className="text-[12px] text-muted-foreground">
                {!n.on
                  ? "关着：agent 等你、跑完了，只有打开 App 才看得到。"
                  : !n.perm
                    ? "系统没给通知权限：点「试一下」申请；拒过的话要去系统设置里打开。"
                    : "App 关着 / 锁屏也能收到；通知栏常驻一条 agent 状态一览。"}
              </div>
            </div>
            <Switch checked={n.on} onCheckedChange={(v) => postAndroid({ t: "notify", op: "set", on: v })} />
          </div>
          <div className="mt-2 flex flex-wrap gap-2">
            <Button size="sm" variant="outline" onClick={() => postAndroid({ t: "notify", op: "test" })}>
              试一下
            </Button>
            {live && !n.battery && (
              <Button size="sm" variant="outline" onClick={() => postAndroid({ t: "notify", op: "battery" })}>
                允许后台运行
              </Button>
            )}
          </div>
          {live && (
            <p className="mt-1.5 text-[12px] leading-relaxed text-muted-foreground">
              {n.battery ? "已经不受电池优化限制。" : "手机省电策略会把后台服务杀掉，点「允许后台运行」放行。"}
              小米 / 华为 / OPPO / vivo 还要在系统设置里给这个 App 开「自启动」和「后台运行」，不然锁屏一会儿就收不到了。
            </p>
          )}
        </div>
      )}

      {vol !== null && (
        <div className="flex items-center justify-between gap-4">
          <div>
            <div className="text-[13px]">音量键当 ↑ / ↓</div>
            <div className="text-[12px] text-muted-foreground">
              只在键盘弹起、下拉菜单开着、agent 在提问时这样用（翻历史、挪光标、选选项）；其他时候还是调音量
            </div>
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
      ? "正在检查更新…"
      : u.phase === "downloading"
        ? `正在下载 ${u.latest}（${u.progress}%）`
        : u.phase === "installing"
          ? `正在安装 ${u.latest}…`
          : u.phase === "ready"
            ? `${u.latest} 已下载好，点「安装」`
            : u.error
              ? `检查更新失败：${u.error}`
              : u.checkedAt
                ? `已是最新（${new Date(u.checkedAt).toLocaleString("zh-CN", { month: "numeric", day: "numeric", hour: "2-digit", minute: "2-digit" })} 检查过）`
                : "每次打开 App 时检查，最多 6 小时一次";
  return (
    <div className="flex items-center justify-between gap-4">
      <div className="min-w-0">
        <div className="text-[13px]">App 版本 {u.version}</div>
        <div className={cn("text-[12px]", u.phase === "ready" ? "text-brand" : u.error && u.phase === "idle" ? "text-bad" : "text-muted-foreground")}>
          {text}
        </div>
      </div>
      {u.phase === "ready" ? (
        <Button size="sm" onClick={() => postAndroid({ t: "update", op: "install" })}>
          安装
        </Button>
      ) : (
        <Button size="sm" variant="outline" disabled={busy} onClick={() => postAndroid({ t: "update", op: "check" })}>
          {busy && <Loader2 className="size-3.5 animate-spin" />}
          检查更新
        </Button>
      )}
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
  if (!r?.proxied) return { via: "relay", detail: "经云端中继" };
  if (r.relay > 0 && r.lan > 0) return { via: "relay", detail: "刚切到局域网：新连接走直连，有的还在中继上（重连一次就过去了）" };
  if (r.relay > 0) return { via: "relay", detail: "经云端中继" };
  if (r.lan > 0) return { via: "lan", detail: "局域网直连" };
  return r.next === "lan" ? { via: "lan", detail: "局域网直连" } : { via: "relay", detail: "经云端中继" };
}
