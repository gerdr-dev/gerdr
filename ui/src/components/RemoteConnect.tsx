import { useState } from "react";
import { api } from "@/lib/api";
import { t } from "@/lib/i18n";
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
  const h = host.trim().toLowerCase() || t("remoteConnect.hostSample");

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
      <div className="text-[13px] font-medium">{t("remoteConnect.title")}</div>
      <div className="space-y-3 rounded-md border p-3">
        <div className="text-[12px] text-muted-foreground">{t("remoteConnect.intro")}</div>
        <Field label={t("remoteConnect.relay")}>
          <Input className="font-mono text-[12px]" placeholder="https://relay.example.com" value={relay} onChange={(e) => setRelay(e.target.value)} />
        </Field>
        <Field label={t("remoteConnect.host")}>
          <Input
            className="font-mono text-[12px]"
            placeholder={t("remoteConnect.hostPlaceholder")}
            spellCheck={false}
            value={host}
            onChange={(e) => setHost(e.target.value)}
          />
        </Field>
        <Field label={t("remoteConnect.invite")}>
          <Input className="font-mono text-[12px]" autoComplete="off" spellCheck={false} value={invite} onChange={(e) => setInvite(e.target.value)} />
        </Field>
        <div className="space-y-1 text-[12px] text-muted-foreground">
          <div>{t("remoteConnect.inviteHint")}</div>
          <pre className="overflow-x-auto rounded-md bg-muted p-2 font-mono">dialhome invite {h}</pre>
          <div>{t("remoteConnect.dnsHint", { host: h })}</div>
        </div>
        {err && <div className="text-[12px] text-bad">{err}</div>}
        <Button size="sm" disabled={busy || !relay.trim() || !host.trim() || !invite.trim()} onClick={connect}>
          {busy ? t("remoteConnect.connecting") : t("remoteConnect.connect")}
        </Button>
      </div>
    </div>
  );
}
