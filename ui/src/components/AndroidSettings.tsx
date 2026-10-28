import { useEffect, useState } from "react";
import { Button } from "@/components/ui/button";
import { Switch } from "@/components/ui/switch";
import { isAndroid, onAndroid, postAndroid } from "@/lib/android";
import { cn } from "@/lib/utils";

/**
 * 安卓 App 里才有的那几项（设置 → 通用）：后台通知、音量键、走的哪条路、换服务器。状态全在壳那边（android/），
 * 这里只画、只转发点击 —— 网页的系统通知在 WebView 里用不了（没有 Notification），而且只在页面开着时有效；
 * 壳里是一个前台服务长连服务器（NoticeService），App 关着 / 锁屏也收得到。
 */
export function AndroidSettings() {
  const [n, setN] = useState<{ on: boolean; perm: boolean; battery: boolean } | null>(null);
  const [vol, setVol] = useState<boolean | null>(null);
  const route = useRoute();

  useEffect(() => {
    const off1 = onAndroid("notify", (m) => setN({ on: !!m.on, perm: !!m.perm, battery: !!m.battery }));
    const off2 = onAndroid("volkeys", (m) => setVol(!!m.on));
    postAndroid({ t: "notify", op: "get" });
    postAndroid({ t: "volkeys", op: "get" });
    return () => {
      off1();
      off2();
    };
  }, []);

  if (!isAndroid()) return null;
  const live = n?.on && n.perm;
  return (
    <div className="space-y-4 rounded-lg border p-3">
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
            <div className="text-[12px] text-muted-foreground">输入框空着时翻之前发过的话；agent 提问时在选项间移动</div>
          </div>
          <Switch checked={vol} onCheckedChange={(v) => postAndroid({ t: "volkeys", op: "set", on: v })} />
        </div>
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
    () =>
      onAndroid("route", (m) => setR({ lan: Number(m.lan) || 0, relay: Number(m.relay) || 0, next: String(m.next ?? ""), proxied: !!m.proxied })),
    [],
  );
  if (!r?.proxied) return { via: "relay", detail: "经云端中继" };
  if (r.relay > 0 && r.lan > 0) return { via: "relay", detail: "刚切到局域网：新连接走直连，有的还在中继上（重连一次就过去了）" };
  if (r.relay > 0) return { via: "relay", detail: "经云端中继" };
  if (r.lan > 0) return { via: "lan", detail: "局域网直连" };
  return r.next === "lan" ? { via: "lan", detail: "局域网直连" } : { via: "relay", detail: "经云端中继" };
}
