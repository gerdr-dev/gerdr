import { Check, TriangleAlert, X } from "lucide-react";
import { useEffect, useRef, useState } from "react";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { api, ApiError, type CliRequest } from "@/lib/api";
import { isCancel, loginPasskey } from "@/lib/passkey";
import { cn } from "@/lib/utils";
import { t } from "@/lib/i18n";

/**
 * 「批准 App 登录」：安卓 App 的 WebView 里刷不了 passkey 时（WebView 太旧、手机上没有凭据提供方），App 显示一个码、
 * 打开这一页（`/?cli`），人在这儿**手输**码、看清是哪台设备、批准（流程见 internal/auth/cliapprove.go）。
 * 远程那条路上批准要当场刷一次 passkey；在跑 gerdr 的电脑上（本机口）批准不用。
 *
 * 三条别改（照 herdr-web 的 CliApprovePage）：
 *   - **码必须手输，不从 URL 里带进来**。`/?cli` 只负责打开这一页 —— 「点开就填好码」的链接正是钓鱼要的
 *     （发起请求谁都能做，包括这台机器上被注入的 agent）。
 *   - **批准前先 peek**，把发起方的设备 / IP / 新设备还是重验摊出来；发起方就是这台机器本身时显式警告。
 *   - 远程批准服务端回 403 stepup 时这儿刷一次 passkey 再试一次（只重试一次）。
 *
 * 打开方式：地址带 `?cli`，或者设置 → 远程访问里的按钮（发 `gerdr:cli-approve` 事件）。
 */

type Step = { at: "code" } | { at: "confirm"; req: CliRequest } | { at: "done"; ok: boolean; reauth: boolean };

const fmt = (v: string) => {
  const c = v
    .replace(/[^0-9a-z]/gi, "")
    .toUpperCase()
    .slice(0, 8);
  return c.length > 4 ? `${c.slice(0, 4)}-${c.slice(4)}` : c;
};

export function CliApprove() {
  const [open, setOpen] = useState(() => new URLSearchParams(location.search).has("cli"));
  const [code, setCode] = useState("");
  const [step, setStep] = useState<Step>({ at: "code" });
  const [err, setErr] = useState("");
  const [busy, setBusy] = useState(false);
  const box = useRef<HTMLInputElement>(null);

  useEffect(() => {
    // 把 ?cli 从地址栏抹掉：刷新一次别又弹出来
    if (open && location.search.includes("cli")) {
      const q = new URLSearchParams(location.search);
      q.delete("cli");
      history.replaceState(null, "", location.pathname + (q.size ? `?${q}` : "") + location.hash);
    }
    const on = () => setOpen(true);
    window.addEventListener("gerdr:cli-approve", on);
    return () => window.removeEventListener("gerdr:cli-approve", on);
  }, []);

  const reset = () => {
    setStep({ at: "code" });
    setCode("");
    setErr("");
  };
  const close = () => {
    setOpen(false);
    reset();
  };
  const raw = code.replace(/-/g, "");

  const peek = async (c = raw) => {
    if (c.length !== 8 || busy) return;
    setErr("");
    setBusy(true);
    try {
      setStep({ at: "confirm", req: await api.cliPeek(c) });
    } catch (e) {
      setErr((e as Error).message);
    } finally {
      setBusy(false);
    }
  };

  const approve = async (req: CliRequest) => {
    setErr("");
    setBusy(true);
    try {
      try {
        await api.cliApprove(raw);
      } catch (e) {
        if (!(e instanceof ApiError && e.reason === "stepup")) throw e;
        await loginPasskey(); // 服务端据此刷新这台的 PasskeyAt
        await api.cliApprove(raw);
      }
      setStep({ at: "done", ok: true, reauth: !!req.device });
    } catch (e) {
      if (!isCancel(e)) setErr((e as Error).message);
    } finally {
      setBusy(false);
    }
  };

  const deny = async (req: CliRequest) => {
    setErr("");
    try {
      await api.cliDeny(raw);
      setStep({ at: "done", ok: false, reauth: !!req.device });
    } catch (e) {
      setErr((e as Error).message);
    }
  };

  return (
    <Dialog open={open} onOpenChange={(o) => !o && close()}>
      <DialogContent className="sm:max-w-md" onOpenAutoFocus={(e) => (e.preventDefault(), box.current?.focus())}>
        <DialogHeader>
          <DialogTitle>{t("cliApprove.title")}</DialogTitle>
          {step.at === "code" && <DialogDescription>{t("cliApprove.desc")}</DialogDescription>}
        </DialogHeader>

        {step.at === "code" && (
          <>
            <div className="flex gap-2">
              <Input
                ref={box}
                value={code}
                autoCapitalize="characters"
                autoComplete="off"
                spellCheck={false}
                placeholder="XXXX-XXXX"
                aria-label={t("cliApprove.codeLabel")}
                className={cn("flex-1 text-center text-base uppercase", code && "tracking-[.25em]")}
                onChange={(e) => {
                  const v = fmt(e.target.value);
                  setCode(v);
                  setErr("");
                  const c = v.replace(/-/g, "");
                  if (c.length === 8) void peek(c); // 输够 8 位自己去查，手机上少点一下
                }}
                onKeyDown={(e) => e.key === "Enter" && void peek()}
              />
              <Button disabled={busy || raw.length !== 8} onClick={() => void peek()}>
                {busy ? "…" : t("cliApprove.next")}
              </Button>
            </div>
            <p className="border-t pt-3 text-xs leading-relaxed text-muted-foreground">
              <b className="font-medium text-foreground">{t("cliApprove.warnStrong")}</b>
              {t("cliApprove.warnRest")}
            </p>
          </>
        )}

        {step.at === "confirm" && (
          <>
            <p className="text-[13px] text-muted-foreground">{t("cliApprove.check")}</p>
            <dl className="grid grid-cols-[auto_1fr] gap-x-4 gap-y-1.5 rounded-md border bg-muted/40 px-3 py-2.5 text-[13px]">
              <dt className="text-muted-foreground">{t("cliApprove.device")}</dt>
              <dd className="truncate font-medium">{step.req.label}</dd>
              <dt className="text-muted-foreground">{t("cliApprove.from")}</dt>
              <dd className="truncate font-mono text-muted-foreground">{step.req.ip || "—"}</dd>
              <dt className="text-muted-foreground">{t("cliApprove.what")}</dt>
              <dd className="text-muted-foreground">
                {step.req.device ? t("cliApprove.reauth", { device: step.req.deviceLabel || step.req.device }) : t("cliApprove.newDevice")}
              </dd>
            </dl>
            {step.req.local && (
              <p className="flex gap-2 rounded-md border border-warn/40 bg-warn/10 px-3 py-2 text-xs leading-relaxed text-warn">
                <TriangleAlert className="mt-px size-4 shrink-0" />
                <span>
                  {t("cliApprove.localBefore")}
                  <b>{t("cliApprove.localStrong")}</b>
                  {t("cliApprove.localAfter")}
                </span>
              </p>
            )}
            <Button className="w-full" disabled={busy} onClick={() => void approve(step.req)}>
              {busy ? t("cliApprove.waitPasskey") : t("cliApprove.approve")}
            </Button>
            <div className="flex gap-2">
              <Button variant="destructive" className="flex-1" disabled={busy} onClick={() => void deny(step.req)}>
                {t("cliApprove.deny")}
              </Button>
              <Button variant="outline" className="flex-1" disabled={busy} onClick={reset}>
                {t("cliApprove.otherCode")}
              </Button>
            </div>
          </>
        )}

        {step.at === "done" && (
          <>
            <p className={cn("flex items-center gap-2 text-[15px]", step.ok ? "text-ok" : "text-muted-foreground")}>
              {step.ok ? <Check className="size-5" /> : <X className="size-5" />}
              {step.ok ? (step.reauth ? t("cliApprove.approvedReauth") : t("cliApprove.approved")) : t("cliApprove.denied")}
            </p>
            <p className="text-[13px] text-muted-foreground">{step.ok ? t("cliApprove.approvedHint") : t("cliApprove.deniedHint")}</p>
            <Button variant="outline" className="w-full" onClick={close}>
              {t("common.close")}
            </Button>
          </>
        )}

        {err && <p className="text-[13px] text-bad">{err}</p>}
      </DialogContent>
    </Dialog>
  );
}
