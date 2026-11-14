import { useState } from "react";
import { api } from "@/lib/api";
import { t } from "@/lib/i18n";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Field } from "@/components/RemoteCert";

// 设置 → 远程访问：还没连中继时的「连接」表单，等于不用敲 `gerdr remote join`。
// 连上当场生效（后端 remote.Service.Connect），证书在下面「证书」那一节接着填，以前配过就沿用。
// 邀请码一次性、30 分钟过期：后端先核主机名 / 中继地址再用它，填错了不会白白烧掉一个码。
// The other way in is a tunnel of one's own (frp): host + a local forward port, no relay
// (internal/remote/forward.go, `gerdr remote forward`).

// Where to get a relay of one's own: its README covers installing it on a VPS.
const DIALHOME_REPO = "https://github.com/zbysir/dialhome";

// What `gerdr remote forward` prints too (remote.FrpcSnippet)
const frpc = (host: string, port: number) =>
  `[[proxies]]
name = "gerdr"
type = "https"
customDomains = ["${host}"]
localIP = "127.0.0.1"
localPort = ${port}
transport.proxyProtocolVersion = "v2"`;

export function RemoteConnect({ relayURL, onDone }: { relayURL?: string; onDone: () => void }) {
  const [mode, setMode] = useState<"relay" | "forward">("relay");
  const [port, setPort] = useState("7879");
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
      if (mode === "forward") await api.remoteConnectForward(host.trim(), Number(port) || 0);
      else await api.remoteConnect(relay.trim(), host.trim(), invite.trim());
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
        <div className="flex gap-2">
          <Button size="sm" variant={mode === "relay" ? "default" : "outline"} onClick={() => setMode("relay")}>
            {t("remoteConnect.modeRelay")}
          </Button>
          <Button size="sm" variant={mode === "forward" ? "default" : "outline"} onClick={() => setMode("forward")}>
            {t("remoteConnect.modeForward")}
          </Button>
        </div>
        {mode === "forward" ? (
          <>
            <div className="text-[12px] text-muted-foreground">{t("remoteConnect.forwardIntro")}</div>
            <Field label={t("remoteConnect.host")}>
              <Input
                className="font-mono text-[12px]"
                placeholder={t("remoteConnect.hostPlaceholder")}
                spellCheck={false}
                value={host}
                onChange={(e) => setHost(e.target.value)}
              />
            </Field>
            <Field label={t("remoteConnect.forwardPort")}>
              <Input className="font-mono text-[12px]" inputMode="numeric" value={port} onChange={(e) => setPort(e.target.value.replace(/\D/g, ""))} />
            </Field>
            <div className="space-y-1 text-[12px] text-muted-foreground">
              <div>{t("remoteConnect.frpcHint")}</div>
              <pre className="overflow-x-auto rounded-md bg-muted p-2 font-mono">{frpc(h, Number(port) || 7879)}</pre>
              <div>{t("remoteConnect.forwardDns", { host: h })}</div>
            </div>
          </>
        ) : (
          <>
            <div className="text-[12px] text-muted-foreground">{t("remoteConnect.intro")}</div>
            <div className="text-[12px] text-muted-foreground">
              {t("remoteConnect.noRelay")}{" "}
              <a className="font-mono text-brand underline-offset-2 hover:underline" href={DIALHOME_REPO} target="_blank" rel="noreferrer">
                github.com/zbysir/dialhome
              </a>
            </div>
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
          </>
        )}
        {err && <div className="text-[12px] text-bad">{err}</div>}
        <Button size="sm" disabled={busy || !host.trim() || (mode === "relay" && (!relay.trim() || !invite.trim()))} onClick={connect}>
          {busy ? t("remoteConnect.connecting") : t("remoteConnect.connect")}
        </Button>
      </div>
    </div>
  );
}
