import { useEffect, useRef, useState } from "react";
import { Copy, ExternalLink, Loader2, LogIn, SquareTerminal } from "lucide-react";
import { toast } from "sonner";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { api, type LoginState } from "@/lib/api";
import { runInTerminal } from "@/lib/actions";
import { useStore } from "@/lib/store";
import { t } from "@/lib/i18n";

// Above the composer (ChatHost.composerTop) while the last turn failed because the agent isn't signed in any more
// (hub Summary.Login). "Sign in" runs the agent's own login in the background (hub/login.go): its browser opens on the
// Gerdr computer and finishes by itself; the dialog also shows the sign-in link and a box for the code that page
// gives, for someone on a phone or whose browser didn't open. When it succeeds the backend clears the error, this
// bar goes away, and the person resends (the agent's process picks the new login up by itself).
// The terminal is kept as a fallback in the dialog: a login the background run can't do (an SSO quirk, a new prompt).
export function LoginChip({ sessionId }: { sessionId: string }) {
  const login = useStore((s) => s.sessions[sessionId]?.login);
  const cwd = useStore((s) => s.sessions[sessionId]?.cwd);
  const [open, setOpen] = useState(false);
  if (!login || cwd === undefined) return null;
  return (
    <>
      <div className="mb-2 flex items-center gap-3 rounded-xl border border-warn/40 bg-warn/10 py-1.5 pr-1.5 pl-3.5 text-[12px]">
        <span className="min-w-0 flex-1 text-warn">{t("loginChip.text")}</span>
        {/* Same 6px from the bar's edge on every side (pr-1.5 = py-1.5), and a fill instead of an outline: a bordered
            button inside a bordered bar read as a box squeezed into a box */}
        <button
          className="inline-flex h-7 shrink-0 items-center gap-1.5 rounded-lg bg-brand/15 px-3 font-medium text-brand hover:bg-brand/25"
          onClick={() => setOpen(true)}
        >
          <LogIn className="size-3.5" />
          {t("loginChip.button")}
        </button>
      </div>
      {open && <LoginDialog sessionId={sessionId} cwd={cwd} command={login} onClose={() => setOpen(false)} />}
    </>
  );
}

function LoginDialog({ sessionId, cwd, command, onClose }: { sessionId: string; cwd: string; command: string; onClose: () => void }) {
  const [st, setSt] = useState<LoginState | null>(null);
  const [startErr, setStartErr] = useState("");
  const [code, setCode] = useState("");
  const [sending, setSending] = useState(false);
  // The parent's onClose is a new function each render: read it through a ref so the effect starts the login once
  const closeRef = useRef(onClose);
  closeRef.current = onClose;

  // Start (or join the running one), then follow it until it ends
  useEffect(() => {
    let stop = false;
    let timer: ReturnType<typeof setTimeout> | undefined;
    const poll = async () => {
      try {
        const s = await api.loginState(sessionId);
        if (stop) return;
        setSt(s);
        if (s.state === "done") {
          toast.success(t("loginDialog.done"));
          closeRef.current();
          return;
        }
        if (s.state === "failed") return;
      } catch {
        // a blip: keep polling
      }
      timer = setTimeout(poll, 1000);
    };
    api
      .loginStart(sessionId)
      .then((s) => {
        if (stop) return;
        setSt(s);
        timer = setTimeout(poll, 1000);
      })
      .catch((e: any) => !stop && setStartErr(e.message ?? String(e)));
    return () => {
      stop = true;
      clearTimeout(timer);
    };
  }, [sessionId]);

  const close = () => {
    // Closing while it waits gives up on it: a claude left waiting for a code would block the next try's prompt
    if (st?.state === "running") void api.loginCancel(sessionId).catch(() => {});
    onClose();
  };
  const sendCode = async () => {
    setSending(true);
    try {
      await api.loginCode(sessionId, code);
      setCode("");
    } catch (e: any) {
      toast.error(e.message ?? String(e));
    } finally {
      setSending(false);
    }
  };
  const inTerminal = () => {
    close();
    void runInTerminal(sessionId, cwd, command, "new");
  };
  const failed = startErr || (st?.state === "failed" ? st.error || t("loginDialog.failed") : "");

  return (
    <Dialog open onOpenChange={(o) => !o && close()}>
      <DialogContent className="max-w-lg">
        <DialogHeader>
          <DialogTitle>{t("loginDialog.title")}</DialogTitle>
          <DialogDescription>{t("loginDialog.desc")}</DialogDescription>
        </DialogHeader>
        <div className="space-y-3 text-[13px]">
          {failed ? (
            <div className="space-y-1">
              <div className="text-bad">{failed}</div>
              {st?.output && <pre className="max-h-40 overflow-auto rounded-md bg-muted p-2 text-[11px] whitespace-pre-wrap">{st.output}</pre>}
            </div>
          ) : !st?.url ? (
            <div className="flex items-center gap-2 text-muted-foreground">
              <Loader2 className="size-4 animate-spin" />
              {t("loginDialog.starting")}
            </div>
          ) : (
            <>
              <div className="flex items-center gap-2 text-muted-foreground">
                <Loader2 className="size-4 shrink-0 animate-spin" />
                {t("loginDialog.waiting")}
              </div>
              <div className="space-y-1">
                <div>{t("loginDialog.elsewhere")}</div>
                <div className="flex gap-2">
                  <Button asChild size="sm" variant="outline">
                    <a href={st.url} target="_blank" rel="noreferrer">
                      <ExternalLink className="size-3.5" />
                      {t("loginDialog.openLink")}
                    </a>
                  </Button>
                  <Button size="sm" variant="ghost" onClick={() => navigator.clipboard.writeText(st.url!).then(() => toast.success(t("common.copied")))}>
                    <Copy className="size-3.5" />
                    {t("loginDialog.copyLink")}
                  </Button>
                </div>
              </div>
              <form
                className="flex gap-2"
                onSubmit={(e) => {
                  e.preventDefault();
                  if (code.trim() && !sending) void sendCode();
                }}
              >
                <Input value={code} onChange={(e) => setCode(e.target.value)} placeholder={t("loginDialog.codePlaceholder")} className="flex-1" />
                <Button type="submit" size="sm" disabled={!code.trim() || sending}>
                  {t("loginDialog.submit")}
                </Button>
              </form>
            </>
          )}
        </div>
        <DialogFooter className="gap-2">
          <Button variant="ghost" size="sm" onClick={inTerminal} title={command}>
            <SquareTerminal className="size-3.5" />
            {t("loginDialog.terminal")}
          </Button>
          <Button variant="outline" size="sm" onClick={close}>
            {t("common.cancel")}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
