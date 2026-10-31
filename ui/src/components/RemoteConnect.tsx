import { useState } from "react";
import { api } from "@/lib/api";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Field } from "@/components/RemoteCert";

// 设置 → 远程访问：还没连中继时的「连接」表单，等于不用敲 `gerdr remote join`。
// 连上当场生效（后端 remote.Service.Connect），证书在下面「证书」那一节接着填，以前配过就沿用。
// 邀请码一次性、30 分钟过期：后端先核主机名 / 中继地址再用它，填错了不会白白烧掉一个码。

export function RemoteConnect({ relayURL, onDone }: { relayURL?: string; onDone: () => void }) {
  const [relay, setRelay] = useState(relayURL ?? "");
  const [host, setHost] = useState("");
  const [invite, setInvite] = useState("");
  const [err, setErr] = useState("");
  const [busy, setBusy] = useState(false);
  const h = host.trim().toLowerCase() || "<主机名>";

  const connect = async () => {
    setErr("");
    setBusy(true);
    try {
      await api.remoteConnect(relay.trim(), host.trim(), invite.trim());
      onDone();
    } catch (e) {
      setErr((e as Error).message);
    } finally {
      setBusy(false);
    }
  };

  return (
    <div className="space-y-1.5">
      <div className="text-[13px] font-medium">连接中继</div>
      <div className="space-y-3 rounded-md border p-3">
        <div className="text-[12px] text-muted-foreground">
          Gerdr 经云端中继（dialhome）对外：TLS 在这台机器上终止，中继只转密文。填中继地址、给这台机器起的域名，和在中继后台里出的邀请码。
        </div>
        <Field label="中继地址">
          <Input className="font-mono text-[12px]" placeholder="https://relay.example.com" value={relay} onChange={(e) => setRelay(e.target.value)} />
        </Field>
        <Field label="主机名">
          <Input
            className="font-mono text-[12px]"
            placeholder="gerdr.example.com（手机上打开的地址）"
            spellCheck={false}
            value={host}
            onChange={(e) => setHost(e.target.value)}
          />
        </Field>
        <Field label="邀请码">
          <Input className="font-mono text-[12px]" autoComplete="off" spellCheck={false} value={invite} onChange={(e) => setInvite(e.target.value)} />
        </Field>
        <div className="space-y-1 text-[12px] text-muted-foreground">
          <div>邀请码在中继的网页后台里出（一次性，30 分钟内有效），或者在 VPS 上：</div>
          <pre className="overflow-x-auto rounded-md bg-muted p-2 font-mono">dialhome invite {h}</pre>
          <div>DNS 上给 {h} 加一条 A 记录指向中继 VPS，中继那边的 SNI 路由也要放行这个名字 —— 漏了的表现是「已连上」但手机打不开，这里一个字都不报。</div>
        </div>
        {err && <div className="text-[12px] text-bad">{err}</div>}
        <Button size="sm" disabled={busy || !relay.trim() || !host.trim() || !invite.trim()} onClick={connect}>
          {busy ? "连接中…" : "连接"}
        </Button>
      </div>
    </div>
  );
}
