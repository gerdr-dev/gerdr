import { useEffect, useState } from "react";
import { api, type StepUpRequest } from "@/lib/api";
import { t } from "@/lib/i18n";
import { isCancel, registerPasskey, stepUpWithPasskey } from "@/lib/passkey";
import { Button } from "@/components/ui/button";

// 已经有 passkey 之后再加一把，要先证明是本人（step-up，internal/auth 的 StepUpNeeded）。两条路：
//   - 用已有的那把验一次：只有这台设备上本来就有 passkey（或者同步过来了）才行；
//   - 到电脑上批准（auth/approve.go）：新手机上一把都没有时只能走这条 —— 系统只会弹「未发现任何通行密钥」。
// 批准之后**还要人再点一下**才去注册：WebAuthn 的 create 要用户手势，轮询到「已批准」时直接调会被浏览器拒掉。

export function PasskeyStepUp({ onDone, onCancel }: { onDone: (label: string) => void; onCancel: () => void }) {
  const [req, setReq] = useState<{ id: string; code: string } | null>(null);
  const [state, setState] = useState<"pending" | "approved" | "expired">("pending");
  const [err, setErr] = useState("");

  useEffect(() => {
    if (!req || state !== "pending") return;
    const timer = setInterval(async () => {
      try {
        setState((await api.stepUpState(req.id)).state);
      } catch {}
    }, 2000);
    return () => clearInterval(timer);
  }, [req, state]);

  const attempt = async (fn: () => Promise<string>) => {
    setErr("");
    try {
      onDone(await fn());
    } catch (e) {
      if (!isCancel(e)) setErr((e as Error).message);
    }
  };
  const ask = async () => {
    setErr("");
    try {
      setReq(await api.stepUpRequest());
      setState("pending");
    } catch (e) {
      setErr((e as Error).message);
    }
  };

  return (
    <div className="space-y-2 rounded-md border p-3">
      {!req ? (
        <>
          <div>{t("passkeyStepUp.intro")}</div>
          <div className="flex flex-wrap gap-2">
            <Button size="sm" onClick={ask}>
              {t("passkeyStepUp.approveOnComputer")}
            </Button>
            <Button size="sm" variant="outline" onClick={() => attempt(stepUpWithPasskey)}>
              {t("passkeyStepUp.usePasskey")}
            </Button>
            <Button size="sm" variant="ghost" onClick={onCancel}>
              {t("passkeyStepUp.nevermind")}
            </Button>
          </div>
          <div className="text-[12px] text-muted-foreground">{t("passkeyStepUp.noPasskeyHint")}</div>
        </>
      ) : state === "pending" ? (
        <>
          <div>{t("passkeyStepUp.pending")}</div>
          <div className="font-mono text-[20px] tracking-[.3em]">{req.code}</div>
          <div className="text-[12px] text-muted-foreground">{t("passkeyStepUp.waiting")}</div>
          <Button size="sm" variant="ghost" onClick={onCancel}>
            {t("passkeyStepUp.nevermind")}
          </Button>
        </>
      ) : state === "approved" ? (
        <>
          <div className="text-ok">{t("passkeyStepUp.approved")}</div>
          <Button size="sm" onClick={() => attempt(registerPasskey)}>
            {t("passkeyStepUp.addNow")}
          </Button>
        </>
      ) : (
        <>
          <div className="text-warn">{t("passkeyStepUp.expired")}</div>
          <Button size="sm" variant="outline" onClick={ask}>
            {t("passkeyStepUp.resend")}
          </Button>
        </>
      )}
      {err && <div className="text-[12px] text-bad">{err}</div>}
    </div>
  );
}

// 本机这边：远程设备请求加 passkey，坐在电脑前的人批准。只在本机口上画（接口远程是 403）。
export function StepUpApprovals({ open }: { open: boolean }) {
  const [reqs, setReqs] = useState<StepUpRequest[]>([]);
  const load = () =>
    api
      .stepUpPending()
      .then((r) => setReqs(r.requests ?? []))
      .catch(() => {});
  useEffect(() => {
    if (!open) return;
    load();
    const timer = setInterval(load, 2000);
    return () => clearInterval(timer);
  }, [open]);
  if (reqs.length === 0) return null;
  const decide = async (id: string, ok: boolean) => {
    await api.stepUpDecide(id, ok).catch(() => {});
    load();
  };
  return (
    <div className="space-y-1.5">
      <div className="text-[13px] font-medium text-warn">{t("passkeyStepUp.awaiting")}</div>
      <div className="divide-y rounded-md border border-warn/50">
        {reqs.map((r) => (
          <div key={r.id} className="flex items-center gap-3 px-3 py-2 max-md:flex-col max-md:items-stretch">
            <div className="min-w-0 flex-1">
              <div className="truncate">{t("passkeyStepUp.request", { label: r.label })}</div>
              <div className="text-[12px] text-muted-foreground">
                {rich(t("passkeyStepUp.verifyCode"), { code: <span className="font-mono text-[14px] tracking-[.2em] text-foreground">{r.code}</span> })}
              </div>
            </div>
            <div className="flex gap-2">
              <Button size="sm" onClick={() => decide(r.id, true)}>
                {t("passkeyStepUp.approve")}
              </Button>
              <Button size="sm" variant="ghost" onClick={() => decide(r.id, false)}>
                {t("passkeyStepUp.decline")}
              </Button>
            </div>
          </div>
        ))}
      </div>
    </div>
  );
}

// A message with {name} placeholders filled by elements (a styled value in the middle of a sentence).
function rich(text: string, parts: Record<string, React.ReactNode>): React.ReactNode[] {
  return text.split(/\{(\w+)\}/).map((p, i) => (i % 2 ? <span key={i}>{parts[p]}</span> : p));
}
