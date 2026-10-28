import { useEffect, useState } from "react";
import { api, type StepUpRequest } from "@/lib/api";
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
    const t = setInterval(async () => {
      try {
        setState((await api.stepUpState(req.id)).state);
      } catch {}
    }, 2000);
    return () => clearInterval(t);
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
          <div>已经有 passkey 了，再加一把之前要先证明是你本人：</div>
          <div className="flex flex-wrap gap-2">
            <Button size="sm" onClick={ask}>
              到电脑上批准
            </Button>
            <Button size="sm" variant="outline" onClick={() => attempt(stepUpWithPasskey)}>
              用已有的 passkey 验证
            </Button>
            <Button size="sm" variant="ghost" onClick={onCancel}>
              算了
            </Button>
          </div>
          <div className="text-[12px] text-muted-foreground">这台设备上还没有 passkey 的话选「到电脑上批准」。</div>
        </>
      ) : state === "pending" ? (
        <>
          <div>到跑 gerdr 的那台电脑上，打开 设置 → 远程访问，核对码一致就点「批准」：</div>
          <div className="font-mono text-[20px] tracking-[.3em]">{req.code}</div>
          <div className="text-[12px] text-muted-foreground">等着批准…（3 分钟内有效）</div>
          <Button size="sm" variant="ghost" onClick={onCancel}>
            算了
          </Button>
        </>
      ) : state === "approved" ? (
        <>
          <div className="text-ok">电脑上批准了。</div>
          <Button size="sm" onClick={() => attempt(registerPasskey)}>
            现在添加 passkey
          </Button>
        </>
      ) : (
        <>
          <div className="text-warn">请求过期了（或者被拒了）。</div>
          <Button size="sm" variant="outline" onClick={ask}>
            再发一次
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
    const t = setInterval(load, 2000);
    return () => clearInterval(t);
  }, [open]);
  if (reqs.length === 0) return null;
  const decide = async (id: string, ok: boolean) => {
    await api.stepUpDecide(id, ok).catch(() => {});
    load();
  };
  return (
    <div className="space-y-1.5">
      <div className="text-[13px] font-medium text-warn">等你批准</div>
      <div className="divide-y rounded-md border border-warn/50">
        {reqs.map((r) => (
          <div key={r.id} className="flex items-center gap-3 px-3 py-2 max-md:flex-col max-md:items-stretch">
            <div className="min-w-0 flex-1">
              <div className="truncate">「{r.label}」请求添加 passkey</div>
              <div className="text-[12px] text-muted-foreground">
                核对码 <span className="font-mono text-[14px] tracking-[.2em] text-foreground">{r.code}</span>，和那台设备上显示的一样才批准
              </div>
            </div>
            <div className="flex gap-2">
              <Button size="sm" onClick={() => decide(r.id, true)}>
                批准
              </Button>
              <Button size="sm" variant="ghost" onClick={() => decide(r.id, false)}>
                拒绝
              </Button>
            </div>
          </div>
        ))}
      </div>
    </div>
  );
}
