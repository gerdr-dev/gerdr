import { useState } from "react";
import { api } from "@/lib/api";
import { t } from "@/lib/i18n";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Field } from "@/components/RemoteCert";

// 设置 → 远程访问：还没连中继时的「连接」表单，等于不用敲 `gerdr remote join`。
// 连上当场生效（后端 remote.Service.Connect），证书在下面「证书」那一节接着填，以前配过就沿用。
// 邀请码一次性、30 分钟过期：后端先核主机名 / 中继地址再用它，填错了不会白白烧掉一个码。
// The other way in is direct access (config.Remote.Direct, `gerdr remote direct`): only the host and the port in the
// address; how phones reach this Mac (Tailscale, frp, a port forward) is the person's network.

// Where to get a relay of one's own: its README covers installing it on a VPS.
const DIALHOME_REPO = "https://github.com/zbysir/dialhome";
// Step-by-step direct access (Tailscale / frp) on the website; the README has the same in its remote access section
const DIRECT_GUIDE = "https://gerdr.dev/docs#remote-direct";

export function RemoteConnect({
  relayURL,
  tailnet,
  lanPort,
  onDone,
}: {
  relayURL?: string;
  /** this Mac's Tailscale addresses: where the A record goes with Tailscale */
  tailnet?: string[];
  /** the LAN port: what direct access listens on */
  lanPort?: number;
  onDone: () => void;
}) {
  const [mode, setMode] = useState<"relay" | "direct">("relay");
  const [urlPort, setUrlPort] = useState("");
  const lp = lanPort || 7878;
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
      if (mode === "direct") await api.remoteConnectDirect(host.trim(), Number(urlPort) || 0);
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
          <Button size="sm" variant={mode === "direct" ? "default" : "outline"} onClick={() => setMode("direct")}>
            {t("remoteConnect.modeDirect")}
          </Button>
        </div>
        {mode === "direct" ? (
          <>
            <div className="text-[12px] text-muted-foreground">
              {t("remoteConnect.directIntro", { port: lp })}{" "}
              <a className="text-brand underline-offset-2 hover:underline" href={DIRECT_GUIDE} target="_blank" rel="noreferrer">
                {t("remoteConnect.directGuide")}
              </a>
            </div>
            <Field label={t("remoteConnect.host")}>
              <Input
                className="font-mono text-[12px]"
                placeholder={t("remoteConnect.hostPlaceholder")}
                spellCheck={false}
                value={host}
                onChange={(e) => setHost(e.target.value)}
              />
            </Field>
            <Field label={t("remoteConnect.urlPort")}>
              <Input
                className="font-mono text-[12px]"
                inputMode="numeric"
                placeholder={String(lp)}
                value={urlPort}
                onChange={(e) => setUrlPort(e.target.value.replace(/\D/g, ""))}
              />
            </Field>
            <div className="space-y-1 text-[12px] text-muted-foreground">
              <div>{t("remoteConnect.urlPortHint", { host: h, port: lp, ip: tailnet?.length ? ` ${tailnet.join(" / ")}` : "" })}</div>
              <div>{t("remoteConnect.frpHint", { port: lp })}</div>
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
