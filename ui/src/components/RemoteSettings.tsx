import { useCallback, useEffect, useState } from "react";
import { api, type PasskeyInfo, type RemoteDevice, type RemoteStatus } from "@/lib/api";
import { isCancel, passkeySupported, registerPasskey } from "@/lib/passkey";
import { Button } from "@/components/ui/button";

// 设置 →「远程访问」：手机 / 别的电脑经云端中继打开这台机器上的 gerdr（设计见 docs/dev/REMOTE.md）。
// 两个 origin 上看到的不一样：本机（127.0.0.1）能出配对码、不能加 passkey；远程（中继那个域名）反过来 ——
// 配对码只能坐在机器前出（远程能出码的话，手机被拿走一次就能留下一台踢不掉的设备），passkey 绑在那个域名上。

const when = (s?: string) => (s ? new Date(s).toLocaleString() : "—");

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
    const t = setInterval(load, 5000); // 中继状态会变（重连、证书签下来）
    return () => clearInterval(t);
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
        <div className="space-y-2">
          <div>还没开远程访问。gerdr 经云端中继（herdr-relay）对外：TLS 在这台机器上终止，中继只转密文。开法（一次性）：</div>
          <pre className="overflow-x-auto rounded-md bg-muted p-3 font-mono text-[12px]">
            {"# VPS 上出邀请码\nherdr-relay invite gerdr.example.com\n\n# 这台机器上\ngerdr remote join https://relay.example.com gerdr.example.com <邀请码> \\\n  --dns cloudflare --env CLOUDFLARE_DNS_API_TOKEN=…"}
          </pre>
          <div className="text-[12px] text-muted-foreground">它会打出要加的 DNS 记录（A + CAA）。之后重启 gerdr。</div>
        </div>
      ) : (
        <div className="space-y-1.5">
          <div className="text-[13px] font-medium">地址</div>
          <div className="space-y-1 rounded-md border p-3">
            <div>
              <a className="font-mono text-brand underline-offset-2 hover:underline" href={st.url} target="_blank" rel="noreferrer">
                {st.url}
              </a>
            </div>
            <div className="text-[12px] text-muted-foreground">
              中继 {st.relayURL} · {st.state || "—"}
              {st.certExpires && <> · 证书 {new Date(st.certExpires).toLocaleDateString()} 到期</>}
            </div>
            {st.err && <div className="text-[12px] text-bad">{st.err}</div>}
            {st.locked && (
              <div className="flex items-center gap-2 text-[12px] text-warn">
                有人猜配对码猜错太多次，已暂停接受新设备。
                {!isRemote && (
                  <Button size="sm" variant="outline" onClick={() => run(api.remoteUnlock)}>
                    解开
                  </Button>
                )}
              </div>
            )}
          </div>
        </div>
      )}

      {st.enabled && !isRemote && (
        <div className="space-y-1.5">
          <div className="text-[13px] font-medium">配对新设备</div>
          {pair ? (
            <div className="flex items-start gap-4 rounded-md border p-3 max-md:flex-col">
              {/* 图本身是白底黑块（服务端画的 PNG）：扫码要对比度，深色模式下也不反色 */}
              {pair.qr && <img src={pair.qr} alt="配对二维码" className="size-40 shrink-0 rounded" />}
              <div className="space-y-1.5">
                <div>用手机相机扫这个二维码，或者打开 {st.url} 输入：</div>
                <div className="font-mono text-[20px] tracking-[.3em]">
                  {pair.code.slice(0, 4)} {pair.code.slice(4)}
                </div>
                <div className="text-[12px] text-muted-foreground">{new Date(pair.expires).toLocaleTimeString()} 前有效，用一次就废。</div>
                <Button size="sm" variant="outline" onClick={() => setPair(null)}>
                  收起
                </Button>
              </div>
            </div>
          ) : (
            <Button size="sm" onClick={() => run(async () => setPair(await api.remotePairCode()))}>
              出一个配对码
            </Button>
          )}
        </div>
      )}

      <div className="space-y-1.5">
        <div className="flex items-center justify-between">
          <div className="text-[13px] font-medium">配过对的设备</div>
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
                  setMsg(`踢掉了 ${r.revoked} 台设备${r.passkeys ? `，并删掉了 ${r.passkeys} 把 passkey` : ""}`);
                  if (isRemote) location.reload();
                });
              }}
            >
              {confirmAll ? "再点一下：全部踢掉（含 passkey）" : "全部踢掉"}
            </Button>
          )}
        </div>
        <div className="divide-y rounded-md border">
          {devs.length === 0 && <div className="p-3 text-muted-foreground">还没有。</div>}
          {devs.map((d) => (
            <div key={d.id} className="flex items-center gap-3 px-3 py-2">
              <div className="min-w-0 flex-1">
                <div className="truncate">
                  {d.label}
                  {d.id === me && <span className="ml-2 text-[11px] text-brand">这台</span>}
                </div>
                <div className="truncate text-[11px] text-muted-foreground">
                  最后 {when(d.lastSeen)} · {d.lastIp} · 配于 {when(d.created)}
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
                踢掉
              </Button>
            </div>
          ))}
        </div>
      </div>

      <div className="space-y-1.5">
        <div className="flex items-center justify-between">
          <div className="text-[13px] font-medium">passkey</div>
          {pkOK && (
            <Button size="sm" variant="outline" onClick={() => run(async () => setMsg(`加好了：${await registerPasskey()}`))}>
              加一把 passkey
            </Button>
          )}
        </div>
        <div className="text-[12px] text-muted-foreground">
          有了 passkey，新设备不用回机器前拿配对码，Face ID / 指纹一下就进来；配过对的设备超过 {st.reauthHours || "—"} 小时没验证要再验一次。
          {!pkOK && st.enabled && <> passkey 绑在 {st.host} 上，在那个地址打开 gerdr 才能加。</>}
        </div>
        <div className="divide-y rounded-md border">
          {keys.length === 0 && <div className="p-3 text-muted-foreground">还没有。</div>}
          {keys.map((k) => (
            <div key={k.id} className="flex items-center gap-3 px-3 py-2">
              <div className="min-w-0 flex-1">
                <div className="truncate">{k.label}</div>
                <div className="truncate text-[11px] text-muted-foreground">
                  添加于 {when(k.created)} · 最后用 {when(k.lastUsed)}
                </div>
              </div>
              <Button size="sm" variant="ghost" onClick={() => run(() => api.remoteDeletePasskey(k.id))}>
                删掉
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
          退出这台设备
        </Button>
      )}
    </div>
  );
}
