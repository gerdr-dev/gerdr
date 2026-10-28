import { useEffect, useState } from "react";
import { api, type RemoteCert as Cert } from "@/lib/api";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";

// 设置 → 远程访问 →「证书」：远程那条路的 TLS 在这台机器上终止，得有一张浏览器认的证书。
// 推荐让 gerdr 走 DNS-01 自己去 Let's Encrypt 签：选域名所在的 DNS 服务商、填它的 API 凭据就行。
// 凭据存在 ~/.gerdr/remote/remote.json（0600），读回来只给名字不给值 —— 表单里留空就是沿用存着的那份。
// 只在本机口上出现：远程改不了（后端也拦着，见 remoteGuard）。

export function RemoteCert({ host, onSaved }: { host: string; onSaved: () => void }) {
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

  return (
    <div className="space-y-1.5">
      <div className="flex items-center justify-between">
        <div className="text-[13px] font-medium">证书</div>
        {configured && !editing && (
          <Button size="sm" variant="outline" onClick={() => setEditing(true)}>
            修改
          </Button>
        )}
      </div>
      {!editing ? (
        <div className="space-y-1 rounded-md border p-3">
          <div>
            {c.tlsCert ? (
              <>
                用自己给的证书：<span className="font-mono text-[12px]">{c.tlsCert}</span>
              </>
            ) : (
              <>gerdr 经 {label} 的 DNS 自己去 Let's Encrypt 签，到期前自动续。</>
            )}
          </div>
          {c.dns && c.accountURI && <CAA host={host} account={c.accountURI} />}
        </div>
      ) : (
        <div className="space-y-3 rounded-md border p-3">
          <div className="text-[12px] text-muted-foreground">
            手机浏览器要认 {host} 的证书。最省事的是让 gerdr 自己签：选 {host} 这个域名托管在哪家 DNS 服务商，填一个能改它 DNS 记录的 API
            凭据（签证书时临时加一条 TXT 记录证明域名是你的）。
          </div>
          <div className="flex gap-2">
            <Button size="sm" variant={mode === "dns" ? "default" : "outline"} onClick={() => setMode("dns")}>
              自己签（推荐）
            </Button>
            <Button size="sm" variant={mode === "files" ? "default" : "outline"} onClick={() => setMode("files")}>
              已经有证书文件
            </Button>
          </div>
          {mode === "dns" ? (
            <>
              <Field label="DNS 服务商">
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
              {p && <div className="text-[12px] text-muted-foreground">怎么拿：{p.help}</div>}
              {p?.fields.map((f) => {
                const kept = dns === c.dns && c.set.includes(f.env);
                return (
                  <Field key={f.env} label={f.label}>
                    <Input
                      type={f.secret ? "password" : "text"}
                      autoComplete="off"
                      spellCheck={false}
                      className="font-mono text-[12px]"
                      placeholder={kept ? "已经存着，留空就沿用" : f.env}
                      value={env[f.env] ?? ""}
                      onChange={(e) => setEnv({ ...env, [f.env]: e.target.value })}
                    />
                  </Field>
                );
              })}
              <Field label="邮箱（可空）">
                <Input placeholder="证书快到期还没续上时 Let's Encrypt 发提醒" value={email} onChange={(e) => setEmail(e.target.value)} />
              </Field>
            </>
          ) : (
            <>
              <Field label="证书（fullchain）">
                <Input className="font-mono text-[12px]" placeholder="/path/to/fullchain.pem" value={cert} onChange={(e) => setCert(e.target.value)} />
              </Field>
              <Field label="私钥">
                <Input className="font-mono text-[12px]" placeholder="/path/to/privkey.pem" value={key} onChange={(e) => setKey(e.target.value)} />
              </Field>
              <div className="text-[12px] text-muted-foreground">证书文件被替换后会自动换上新的，不用重启。</div>
            </>
          )}
          {err && <div className="text-[12px] text-bad">{err}</div>}
          <div className="flex gap-2">
            <Button size="sm" disabled={busy} onClick={save}>
              {busy ? "保存中…" : "保存并生效"}
            </Button>
            {configured && (
              <Button size="sm" variant="ghost" onClick={() => (setEditing(false), load())}>
                取消
              </Button>
            )}
          </div>
          {mode === "dns" && <div className="text-[12px] text-muted-foreground">保存后立刻去签，第一次要等 DNS 生效，一两分钟；结果看上面「地址」那一栏。</div>}
        </div>
      )}
    </div>
  );
}

function Field({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <label className="flex items-center gap-3 max-md:flex-col max-md:items-stretch max-md:gap-1">
      <span className="w-32 shrink-0 text-muted-foreground">{label}</span>
      <div className="min-w-0 flex-1">{children}</div>
    </label>
  );
}

// CAA：只许这台机器的 ACME 账户给这个名字签证书。不配一切照常，只是中继那台 VPS 能自己签一张做中间人（docs/dev/REMOTE.md）。
function CAA({ host, account }: { host: string; account: string }) {
  const rec = `${host}. CAA 0 issue "letsencrypt.org; accounturi=${account}; validationmethods=dns-01"`;
  return (
    <div className="space-y-1 pt-1 text-[12px] text-muted-foreground">
      <div>建议在 DNS 上加一条 CAA，只许这台机器签 {host} 的证书（已经有一条覆盖它的 CAA 时要再加这一条，不然签不下来）：</div>
      <pre className="overflow-x-auto rounded-sm bg-muted p-2 font-mono text-[11px] select-all">{rec}</pre>
    </div>
  );
}
