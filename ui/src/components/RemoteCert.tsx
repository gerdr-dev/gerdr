import { useEffect, useState } from "react";
import { api, type HostRecord, type RemoteCert as Cert } from "@/lib/api";
import { t } from "@/lib/i18n";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";

// 设置 → 远程访问 →「证书」：远程那条路的 TLS 在这台机器上终止，得有一张浏览器认的证书。
// 推荐让 gerdr 走 DNS-01 自己去 Let's Encrypt 签：选域名所在的 DNS 服务商、填它的 API 凭据就行。
// 凭据存在 ~/.gerdr/remote/remote.json（0600），读回来只给名字不给值 —— 表单里留空就是沿用存着的那份。
// 只在本机口上出现：远程改不了（后端也拦着，见 remoteGuard）。

// expires: when the certificate in use runs out (shown here; the address line only shows it on the remote side).
export function RemoteCert({ host, expires, onSaved }: { host: string; expires?: string; onSaved: () => void }) {
  const [c, setC] = useState<Cert | null>(null);
  const [editing, setEditing] = useState(false);
  const [mode, setMode] = useState<"dns" | "files">("dns");
  const [dns, setDns] = useState("");
  const [email, setEmail] = useState("");
  const [env, setEnv] = useState<Record<string, string>>({});
  const [cert, setCert] = useState("");
  const [key, setKey] = useState("");
  const [err, setErr] = useState("");
  const [busy, setBusy] = useState(false);

  const load = async () => {
    try {
      const r = await api.remoteCert();
      setC(r);
      setMode(r.tlsCert ? "files" : "dns");
      setDns(r.dns || "cloudflare");
      setEmail(r.email);
      setCert(r.tlsCert);
      setKey(r.tlsKey);
      setEnv({});
      // 还没配证书来源就直接展开表单
      if (!r.dns && !r.tlsCert) setEditing(true);
    } catch (e) {
      setErr((e as Error).message);
    }
  };
  useEffect(() => {
    load();
  }, []);

  if (!c) return err ? <div className="text-[12px] text-bad">{err}</div> : null;
  const p = c.providers.find((x) => x.name === dns);
  const configured = !!(c.dns || c.tlsCert);
  const label = c.providers.find((x) => x.name === c.dns)?.label ?? c.dns;

  const save = async () => {
    setErr("");
    setBusy(true);
    try {
      await api.remoteSaveCert(
        mode === "dns"
          ? { dns, email, staging: c.staging, env, tlsCert: "", tlsKey: "" }
          : { dns: "", email, staging: c.staging, env: {}, tlsCert: cert.trim(), tlsKey: key.trim() },
      );
      setEditing(false);
      await load();
      onSaved();
    } catch (e) {
      setErr((e as Error).message);
    } finally {
      setBusy(false);
    }
  };

  // Drawn inside the address box (RemoteSettings), under the relay line: the certificate is what makes that address
  // open in a browser, so it reads as part of it rather than a section of its own.
  return (
    <div className="space-y-2 border-t pt-2 text-[12px]">
      <div className="flex items-start justify-between gap-3">
        <div className="min-w-0 space-y-0.5">
          <div>
            <span className="font-medium">{t("remoteCert.title")}</span>
            {configured && !editing && (
              <span className="text-muted-foreground">
                {" · "}
                {c.tlsCert
                  ? rich(t("remoteCert.ownCert"), { path: <span className="font-mono text-[11px]">{c.tlsCert}</span> })
                  : t("remoteCert.autoCert", { provider: label })}
              </span>
            )}
          </div>
          {!editing && expires && <div className="text-muted-foreground">{t("remoteCert.expires", { date: new Date(expires).toLocaleDateString() })}</div>}
        </div>
        {configured && !editing && (
          <Button size="sm" variant="outline" className="shrink-0" onClick={() => setEditing(true)}>
            {t("remoteCert.change")}
          </Button>
        )}
      </div>
      {!editing ? (
        <>
          {/* keyed on the provider: saving another one changes whether the button can be offered */}
          <HostA key={c.dns} host={host} provider={label} />
          {c.dns && c.accountURI && <CAA host={host} account={c.accountURI} auto={c.caaAuto ? label : ""} />}
        </>
      ) : (
        <div className="space-y-3">
          <div className="text-[12px] text-muted-foreground">{t("remoteCert.intro", { host })}</div>
          <div className="flex gap-2">
            <Button size="sm" variant={mode === "dns" ? "default" : "outline"} onClick={() => setMode("dns")}>
              {t("remoteCert.modeDns")}
            </Button>
            <Button size="sm" variant={mode === "files" ? "default" : "outline"} onClick={() => setMode("files")}>
              {t("remoteCert.modeFiles")}
            </Button>
          </div>
          {mode === "dns" ? (
            <>
              <Field label={t("remoteCert.dnsProvider")}>
                <Select value={dns} onValueChange={(v) => (setDns(v), setEnv({}))}>
                  <SelectTrigger className="w-48">
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    {c.providers.map((x) => (
                      <SelectItem key={x.name} value={x.name}>
                        {x.label}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              </Field>
              {p && <div className="text-[12px] text-muted-foreground">{t("remoteCert.howTo", { help: p.help })}</div>}
              {p?.fields.map((f) => {
                const kept = dns === c.dns && c.set.includes(f.env);
                return (
                  <Field key={f.env} label={f.label}>
                    <Input
                      type={f.secret ? "password" : "text"}
                      autoComplete="off"
                      spellCheck={false}
                      className="font-mono text-[12px]"
                      placeholder={kept ? t("remoteCert.kept") : f.env}
                      value={env[f.env] ?? ""}
                      onChange={(e) => setEnv({ ...env, [f.env]: e.target.value })}
                    />
                  </Field>
                );
              })}
              <Field label={t("remoteCert.email")}>
                <Input placeholder={t("remoteCert.emailPlaceholder")} value={email} onChange={(e) => setEmail(e.target.value)} />
              </Field>
            </>
          ) : (
            <>
              <Field label={t("remoteCert.certFile")}>
                <Input className="font-mono text-[12px]" placeholder="/path/to/fullchain.pem" value={cert} onChange={(e) => setCert(e.target.value)} />
              </Field>
              <Field label={t("remoteCert.keyFile")}>
                <Input className="font-mono text-[12px]" placeholder="/path/to/privkey.pem" value={key} onChange={(e) => setKey(e.target.value)} />
              </Field>
              <div className="text-[12px] text-muted-foreground">{t("remoteCert.reload")}</div>
            </>
          )}
          {err && <div className="text-[12px] text-bad">{err}</div>}
          <div className="flex gap-2">
            <Button size="sm" disabled={busy} onClick={save}>
              {busy ? t("remoteCert.saving") : t("remoteCert.saveApply")}
            </Button>
            {configured && (
              <Button size="sm" variant="ghost" onClick={() => (setEditing(false), load())}>
                {t("common.cancel")}
              </Button>
            )}
          </div>
          {mode === "dns" && <div className="text-[12px] text-muted-foreground">{t("remoteCert.dnsAfter")}</div>}
        </div>
      )}
    </div>
  );
}

export function Field({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <label className="flex items-center gap-3 max-md:flex-col max-md:items-stretch max-md:gap-1">
      <span className="w-32 shrink-0 text-muted-foreground">{label}</span>
      <div className="min-w-0 flex-1">{children}</div>
    </label>
  );
}

// CAA: only this machine's ACME account may issue for this name. Without it everything still works, but the relay VPS
// could get its own certificate and sit in the middle (docs/dev/REMOTE.md). `auto` names the DNS provider when gerdr can
// check and write the record itself with the stored credentials: then the hint only shows once the provider confirms
// the record is missing, so a set-up machine sees nothing. Other providers can't be checked and always show it.
function CAA({ host, account, auto }: { host: string; account: string; auto: string }) {
  const rec = `${host}. CAA 0 issue "letsencrypt.org; accounturi=${account}; validationmethods=dns-01"`;
  // checking → missing / present; "added" after the button worked
  const [state, setState] = useState<"checking" | "missing" | "present" | "added">(auto ? "checking" : "missing");
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState("");
  useEffect(() => {
    if (!auto) return;
    api
      .remoteCheckCAA()
      .then((r) => setState(r.missing ? "missing" : "present"))
      .catch((e) => (setErr(t("remoteCert.caaCheckFailed", { provider: auto, error: (e as Error).message })), setState("missing")));
  }, [auto, host, account]);
  const add = async () => {
    setBusy(true);
    setErr("");
    try {
      await api.remoteAddCAA();
      setState("added");
    } catch (e) {
      setErr((e as Error).message);
    } finally {
      setBusy(false);
    }
  };
  if (state === "checking" || state === "present") return null;
  if (state === "added") return <div className="pt-1 text-[12px] text-ok">{t("remoteCert.caaAdded", { provider: auto, host })}</div>;
  return (
    <div className="space-y-1 pt-1 text-[12px] text-muted-foreground">
      <div className="flex items-center justify-between gap-3">
        <div>{t("remoteCert.caaHint", { host })}</div>
        {auto && (
          <Button size="sm" variant="outline" className="shrink-0" disabled={busy} onClick={add}>
            {busy ? t("remoteCert.adding") : t("remoteCert.addCAA")}
          </Button>
        )}
      </div>
      <pre className="overflow-x-auto rounded-sm bg-muted p-2 font-mono text-[11px] select-all">{rec}</pre>
      {err && <div className="text-bad">{err}</div>}
    </div>
  );
}

// The host's A record: phones connect to the name and the relay forwards it here by SNI, so without it the address
// says "connected" and the phone can't open it. Shows nothing while it points at the relay alone. The backend checks
// through the DNS provider when it can (it also sees a Cloudflare proxy, which public DNS hides behind Cloudflare's
// IPs) and offers the button then; otherwise it compares public DNS and the record is there to copy.
function HostA({ host, provider }: { host: string; provider: string }) {
  const [h, setH] = useState<HostRecord | null>(null);
  const [added, setAdded] = useState(false);
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState("");
  useEffect(() => {
    api
      .remoteCheckHost()
      .then(setH)
      .catch((e) => setErr(t("remoteCert.hostCheckFailed", { host, error: (e as Error).message })));
  }, [host]);
  const fix = async () => {
    setBusy(true);
    setErr("");
    try {
      setH(await api.remoteFixHost());
      setAdded(true);
    } catch (e) {
      setErr((e as Error).message);
    } finally {
      setBusy(false);
    }
  };
  if (added && h) return <div className="text-ok">{t("remoteCert.hostAdded", { provider, host, ip: h.want })}</div>;
  if (!h || !h.missing) return err ? <div className="text-bad">{err}</div> : null;
  const current = h.current ?? [];
  const hint = !current.length
    ? t("remoteCert.hostNone", { host })
    : current.includes(h.want)
      ? t("remoteCert.hostPartial", { host })
      : t("remoteCert.hostWrong", { host, current: current.join(", "), ip: h.want });
  return (
    <div className="space-y-1 text-warn">
      <div className="flex items-center justify-between gap-3">
        <div>{hint}</div>
        {h.auto && (
          <Button size="sm" variant="outline" className="shrink-0" disabled={busy} onClick={fix}>
            {busy ? t("remoteCert.adding") : current.length ? t("remoteCert.hostFix") : t("remoteCert.addCAA")}
          </Button>
        )}
      </div>
      <pre className="overflow-x-auto rounded-sm bg-muted p-2 font-mono text-[11px] text-foreground select-all">{`${host}.  A  ${h.want}`}</pre>
      {err && <div className="text-bad">{err}</div>}
    </div>
  );
}

// A message with {name} placeholders filled by elements (a styled value in the middle of a sentence).
function rich(text: string, parts: Record<string, React.ReactNode>): React.ReactNode[] {
  return text.split(/\{(\w+)\}/).map((p, i) => (i % 2 ? <span key={i}>{parts[p]}</span> : p));
}
