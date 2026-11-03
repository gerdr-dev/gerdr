import { useCallback, useEffect, useState } from "react";
import { api, type PasskeyInfo, type RemoteDevice, type RemoteStatus } from "@/lib/api";
import { isCancel, isStepUp, passkeySupported, registerPasskey } from "@/lib/passkey";
import { Button } from "@/components/ui/button";
import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover";
import { t } from "@/lib/i18n";
import { useStore } from "@/lib/store";
import type { RemoteConn } from "@/lib/types";
import { HostsSettings } from "@/components/HostsSettings";
import { RemoteCert } from "@/components/RemoteCert";
import { RemoteConnect } from "@/components/RemoteConnect";
import { RemoteScreenSetting } from "@/components/RemoteScreen";
import { ConfirmDelete } from "@/components/ConfirmDelete";
import { PasskeyStepUp, StepUpApprovals } from "@/components/PasskeyStepUp";

// 设置 →「远程访问」：手机 / 别的电脑经云端中继打开这台机器上的 gerdr（设计见 docs/dev/REMOTE.md）。
// 两个 origin 上看到的不一样：本机（127.0.0.1）能出配对码、不能加 passkey；远程（中继那个域名）反过来 ——
// 配对码只能坐在机器前出（远程能出码的话，手机被拿走一次就能留下一台踢不掉的设备），passkey 绑在那个域名上。

const when = (s?: string) => (s ? new Date(s).toLocaleString() : "—");
const devices = (n: number) => t(n === 1 ? "remoteSettings.devicesOne" : "remoteSettings.devices", { n });
const passkeys = (n: number) => t(n === 1 ? "remoteSettings.passkeysOne" : "remoteSettings.passkeys", { n });

export function RemoteSettings({ open }: { open: boolean }) {
  const [st, setSt] = useState<RemoteStatus | null>(null);
  const [isRemote, setIsRemote] = useState(false);
  const [devs, setDevs] = useState<RemoteDevice[]>([]);
  const [me, setMe] = useState("");
  const [keys, setKeys] = useState<PasskeyInfo[]>([]);
  const [pkOK, setPkOK] = useState(false);
  const [pair, setPair] = useState<{ code: string; expires: string; url: string; qr?: string } | null>(null);
  const [err, setErr] = useState("");
  const [msg, setMsg] = useState("");
  const [confirmAll, setConfirmAll] = useState(false);
  const [stepUp, setStepUp] = useState(false);

  const load = useCallback(async () => {
    try {
      const [s, d, p] = await Promise.all([api.remoteStatus(), api.remoteDevices(), api.remotePasskeys()]);
      setSt(s.status);
      setIsRemote(s.remote);
      setDevs(d.devices ?? []);
      setMe(d.me);
      setKeys(p.passkeys ?? []);
      setPkOK(p.available && passkeySupported());
    } catch (e: any) {
      setErr(e.message);
    }
  }, []);
  useEffect(() => {
    if (!open) return;
    load();
    const timer = setInterval(load, 5000); // 中继状态会变（重连、证书签下来）
    return () => clearInterval(timer);
  }, [open, load]);

  const run = async (fn: () => Promise<unknown>) => {
    setErr("");
    setMsg("");
    try {
      await fn();
    } catch (e) {
      if (!isCancel(e)) setErr((e as Error).message);
    }
    load();
  };

  if (!st) return <div className="text-[13px] text-muted-foreground">{err || "…"}</div>;

  return (
    <div className="min-w-0 space-y-5 text-[13px]">
      {err && <div className="text-bad">{err}</div>}
      {msg && <div className="text-ok">{msg}</div>}

      {!st.enabled ? (
        st.envLocked ? (
          <div className="text-[12px] text-muted-foreground">{t("remoteSettings.envLocked")}</div>
        ) : (
          !isRemote && <RemoteConnect relayURL={st.relayURL} onDone={load} />
        )
      ) : (
        <div className="space-y-1.5">
          <div className="flex items-center justify-between">
            <div className="text-[13px] font-medium">{t("remoteSettings.address")}</div>
            {/* 远程那一侧不给：移除 = 把自己锁在门外（后端也拦着）。换地址 = 先移除再连 */}
            {!isRemote && !st.envLocked && (
              <ConfirmDelete
                title={t("remoteSettings.remove.title")}
                label={t("remoteSettings.remove.label")}
                onConfirm={() =>
                  run(async () => {
                    const r = await api.remoteDisconnect();
                    setPair(null);
                    setMsg(
                      r.passkeys
                        ? t("remoteSettings.removedWithKeys", { devices: devices(r.revoked), passkeys: passkeys(r.passkeys) })
                        : t("remoteSettings.removed", { devices: devices(r.revoked) }),
                    );
                  })
                }
              />
            )}
          </div>
          <div className="space-y-1 rounded-md border p-3">
            <div>
              <a className="font-mono text-brand underline-offset-2 hover:underline" href={st.url} target="_blank" rel="noreferrer">
                {st.url}
              </a>
            </div>
            <div className="text-[12px] text-muted-foreground">
              {t("remoteSettings.relayLine", { relay: st.relayURL ?? "", state: st.state || "—" })}
              {st.certExpires && isRemote && t("remoteSettings.certExpires", { date: new Date(st.certExpires).toLocaleDateString() })}
            </div>
            {st.err && <div className="text-[12px] text-bad">{st.err}</div>}
            {st.locked && (
              <div className="flex items-center gap-2 text-[12px] text-warn">
                {t("remoteSettings.locked")}
                {!isRemote && (
                  <Button size="sm" variant="outline" onClick={() => run(api.remoteUnlock)}>
                    {t("remoteSettings.unlock")}
                  </Button>
                )}
              </div>
            )}
          </div>
        </div>
      )}

      {st.enabled && <RemoteConns me={me} />}

      {/* Other computers driven from here (docs/dev/MULTIHOST.md): needs no remote access on this one, only on them */}
      {/* Both directions in one place: this computer controlling others, and others controlling this one (the invite
          is local only, like pairing codes: it hands out full control) */}
      <HostsSettings canInvite={st.enabled && !isRemote} />

      <RemoteScreenSetting />

      {st.enabled && !isRemote && <StepUpApprovals open={open} />}

      {st.enabled && !isRemote && <RemoteCert host={st.host ?? ""} expires={st.certExpires} onSaved={load} />}

      <div className="space-y-1.5">
        <div className="flex items-center justify-between">
          <div className="text-[13px] font-medium">{t("remoteSettings.paired")}</div>
          <div className="flex items-center gap-2">
            {/* Pairing codes only come from the machine itself (see the note at the top). A fresh code each time the
              popover opens; closing it drops the code from the page. */}
            {st.enabled && !isRemote && (
              <Popover open={!!pair} onOpenChange={(o) => (o ? run(async () => setPair(await api.remotePairCode())) : setPair(null))}>
                {/* The trigger goes on a wrapper: asChild straight on the Button would overwrite its data-slot, and the
                    settings dialog sizes buttons by [data-slot=button] (SettingsDialog.tsx). */}
                <PopoverTrigger asChild>
                  <span className="inline-flex">
                    <Button size="sm">{t("remoteSettings.addDevice")}</Button>
                  </span>
                </PopoverTrigger>
                {pair && (
                  <PopoverContent align="end" className="w-80 space-y-2 text-[13px]">
                    {/* 图本身是白底黑块（服务端画的 PNG）：扫码要对比度，深色模式下也不反色 */}
                    {pair.qr && <img src={pair.qr} alt={t("remoteSettings.qrAlt")} className="mx-auto size-48 rounded-sm" />}
                    <div>{t("remoteSettings.scan", { url: st.url ?? "" })}</div>
                    <div className="text-center font-mono text-[20px] tracking-[.3em]">
                      {pair.code.slice(0, 4)} {pair.code.slice(4)}
                    </div>
                    <div className="text-[12px] text-muted-foreground">
                      {t("remoteSettings.codeValid", { time: new Date(pair.expires).toLocaleTimeString() })}
                      {st.pairMinutes > 0 && t("remoteSettings.tempMinutes", { n: st.pairMinutes })}
                    </div>
                  </PopoverContent>
                )}
              </Popover>
            )}
            {devs.length + keys.length > 0 && (
              // 两下才生效：这是急停，连 passkey 一起清（只清设备的话，留着的 passkey 能立刻换回一份新凭据）
              <Button
                size="sm"
                variant={confirmAll ? "destructive" : "outline"}
                onBlur={() => setConfirmAll(false)}
                onClick={() => {
                  if (!confirmAll) return setConfirmAll(true);
                  setConfirmAll(false);
                  run(async () => {
                    const r = await api.remoteRevokeAll();
                    setMsg(
                      r.passkeys
                        ? t("remoteSettings.revokedAllWithKeys", { devices: devices(r.revoked), passkeys: passkeys(r.passkeys) })
                        : t("remoteSettings.revokedAll", { devices: devices(r.revoked) }),
                    );
                    if (isRemote) location.reload();
                  });
                }}
              >
                {confirmAll ? t("remoteSettings.revokeAllConfirm") : t("remoteSettings.revokeAll")}
              </Button>
            )}
          </div>
        </div>
        <div className="divide-y rounded-md border">
          {devs.length === 0 && <div className="p-3 text-muted-foreground">{t("remoteSettings.none")}</div>}
          {devs.map((d) => (
            <div key={d.id} className="flex items-center gap-3 px-3 py-2">
              <div className="min-w-0 flex-1">
                <div className="truncate">
                  {d.label}
                  {d.id === me && <span className="ml-2 text-[11px] text-brand">{t("remoteSettings.thisDevice")}</span>}
                  {/* 配对码进来的临时设备：到点就掉，在那台设备上过一次 passkey 才转正（auth.Device.Temp） */}
                  {d.temp && (
                    <span className="ml-2 text-[11px] text-warn">
                      {t("remoteSettings.temp", { time: new Date(d.expires).toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" }) })}
                    </span>
                  )}
                </div>
                <div className="truncate text-[11px] text-muted-foreground">
                  {t("remoteSettings.deviceMeta", { seen: when(d.lastSeen), ip: d.lastIp, created: when(d.created) })}
                </div>
              </div>
              <Button
                size="sm"
                variant="ghost"
                onClick={() =>
                  run(async () => {
                    await api.remoteRevoke(d.id);
                    if (d.id === me) location.reload();
                  })
                }
              >
                {t("remoteSettings.revoke")}
              </Button>
            </div>
          ))}
        </div>
      </div>

      {st.enabled && (
        <div className="space-y-1.5">
          <div className="flex items-center justify-between">
            <div className="text-[13px] font-medium">{t("remoteSettings.android")}</div>
            <Button size="sm" variant="outline" onClick={() => window.dispatchEvent(new Event("gerdr:cli-approve"))}>
              {t("remoteSettings.approveApp")}
            </Button>
          </div>
          <div className="text-[12px] text-muted-foreground">
            {t("remoteSettings.androidHint", { url: st.url ?? "" })}
            {st.lanErr && <span className="text-bad"> {st.lanErr}</span>}
          </div>
        </div>
      )}

      <div className="space-y-1.5">
        <div className="flex items-center justify-between">
          <div className="text-[13px] font-medium">passkey</div>
          {pkOK && !stepUp && (
            <Button
              size="sm"
              variant="outline"
              onClick={() =>
                run(async () => {
                  try {
                    setMsg(t("remoteSettings.added", { label: await registerPasskey() }));
                  } catch (e) {
                    if (!isStepUp(e)) throw e;
                    setStepUp(true); // 已经有 passkey 了：先证明是本人（PasskeyStepUp）
                  }
                })
              }
            >
              {t("remoteSettings.addPasskey")}
            </Button>
          )}
        </div>
        {stepUp && (
          <PasskeyStepUp
            onCancel={() => setStepUp(false)}
            onDone={(label) => {
              setStepUp(false);
              setMsg(t("remoteSettings.added", { label }));
              load();
            }}
          />
        )}
        <div className="text-[12px] text-muted-foreground">
          {t("remoteSettings.passkeyHint", { hours: st.reauthHours || "—" })}
          {!pkOK && st.enabled && t("remoteSettings.passkeyHost", { host: st.host ?? "" })}
        </div>
        <div className="divide-y rounded-md border">
          {keys.length === 0 && <div className="p-3 text-muted-foreground">{t("remoteSettings.none")}</div>}
          {keys.map((k) => (
            <div key={k.id} className="flex items-center gap-3 px-3 py-2">
              <div className="min-w-0 flex-1">
                <div className="truncate">{k.label}</div>
                <div className="truncate text-[11px] text-muted-foreground">
                  {t("remoteSettings.passkeyMeta", { created: when(k.created), used: when(k.lastUsed) })}
                </div>
              </div>
              <Button size="sm" variant="ghost" onClick={() => run(() => api.remoteDeletePasskey(k.id))}>
                {t("remoteSettings.delete")}
              </Button>
            </div>
          ))}
        </div>
      </div>

      {isRemote && (
        <Button
          size="sm"
          variant="outline"
          onClick={() =>
            run(async () => {
              await api.remoteLogout();
              location.reload();
            })
          }
        >
          {t("remoteSettings.logout")}
        </Button>
      )}
    </div>
  );
}

// 「多久」：连上到现在。
function lasted(since: number, now: number) {
  const s = Math.max(0, Math.floor((now - since) / 1000));
  if (s < 60) return t("remoteSettings.lasted.s", { s });
  const m = Math.floor(s / 60);
  if (m < 60) return t("remoteSettings.lasted.m", { m });
  const h = Math.floor(m / 60);
  if (h < 24) return t("remoteSettings.lasted.hm", { h, m: m % 60 });
  return t("remoteSettings.lasted.dh", { d: Math.floor(h / 24), h: h % 24 });
}

// 正在连接：现在开着远程界面的设备（一个标签页一条）。列表是服务端推的（remote_conns），不用轮询；时长每秒走一下。
function RemoteConns({ me }: { me: string }) {
  const conns = useStore((s) => s.remoteConns);
  const [now, setNow] = useState(Date.now());
  useEffect(() => {
    if (conns.length === 0) return;
    const timer = setInterval(() => setNow(Date.now()), 1000);
    return () => clearInterval(timer);
  }, [conns.length]);
  return (
    <div className="space-y-1.5">
      <div className="text-[13px] font-medium">
        {t("remoteSettings.conns")}
        {conns.length > 0 && <span className="ml-1 text-muted-foreground">{conns.length}</span>}
      </div>
      <div className="divide-y rounded-md border">
        {conns.length === 0 && <div className="p-3 text-muted-foreground">{t("remoteSettings.noConns")}</div>}
        {conns.map((c: RemoteConn) => (
          <div key={c.id} className="flex items-center gap-3 px-3 py-2">
            <span className="size-2 shrink-0 rounded-full bg-ok" />
            <div className="min-w-0 flex-1">
              <div className="truncate">
                {c.label || t("remoteSettings.unknownDevice")}
                {c.client && c.client !== c.label && <span className="ml-2 text-[11px] text-muted-foreground">{c.client}</span>}
                {me && c.device === me && <span className="ml-2 text-[11px] text-brand">{t("remoteSettings.thisDevice")}</span>}
              </div>
              <div className="truncate text-[11px] text-muted-foreground">
                {t("remoteSettings.connMeta", { dur: lasted(c.since, now), time: new Date(c.since).toLocaleTimeString(), ip: c.ip })}
              </div>
            </div>
          </div>
        ))}
      </div>
    </div>
  );
}
