import { useState } from "react";
import { toast } from "sonner";
import {
  AlertDialog,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@/components/ui/alert-dialog";
import { Button } from "@/components/ui/button";
import { api, type Holder } from "@/lib/api";
import { t } from "@/lib/i18n";

// 这个会话还在别处开着（herdr / 终端里的交互式 claude）。两边同时写，记录会分叉 —— 谁也看不见对方说过的话。
// 发消息时服务端先查（acpx.MethodHolders），有人占着就不发、回 409，在这里让人挑：
//   - 另起一份（推荐）：带着全部历史复制出一个新会话，只在 gerdr 里接着聊；别处那个不受影响；
//   - 关掉那边：结束那个 claude 进程（它会先把记录写完），然后在这里接着聊。
// codex 自己就防住了分叉（别处开着时这边接不上，报 active writer），但不知道是哪个进程（pid = 0），
// 所以没有「关掉那边」，换成「我已经在那边关掉了，重试」。
// 挑完 onDone()：Composer 拿输入框里的原话再发一次。
export function HeldDialog({ sessionId, holders, onClose, onDone }: { sessionId: string; holders: Holder[] | null; onClose: () => void; onDone: () => void }) {
  const [busy, setBusy] = useState<"" | "fork" | "kill">("");
  const h = holders?.[0];
  const known = !!h?.pid; // 知道是哪个进程（claude 的登记文件）才能替人关
  const run = async (action: "fork" | "kill") => {
    setBusy(action);
    try {
      await api.takeover(sessionId, action);
      onClose();
      onDone();
    } catch (e: any) {
      toast.error(e.message ?? String(e));
    } finally {
      setBusy("");
    }
  };
  return (
    <AlertDialog open={!!holders} onOpenChange={(o) => !o && !busy && onClose()}>
      <AlertDialogContent className="max-w-lg">
        <AlertDialogHeader>
          <AlertDialogTitle>{t("heldDialog.title")}</AlertDialogTitle>
          <AlertDialogDescription asChild>
            <div className="space-y-2 text-[13px]">
              {known ? (
                <p>
                  {t(h?.status ? (h.status === "busy" ? "heldDialog.knownBusy" : "heldDialog.knownIdle") : "heldDialog.known", {
                    where: h?.name ? t("heldDialog.namedWhere", { name: h.name }) : t("heldDialog.otherTerminal"),
                    pid: h?.pid ?? "",
                  })}
                </p>
              ) : (
                <p>{t("heldDialog.unknown", { where: h?.name ?? t("heldDialog.elsewhere") })}</p>
              )}
              <p>{t("heldDialog.notSent")}</p>
            </div>
          </AlertDialogDescription>
        </AlertDialogHeader>
        <AlertDialogFooter className="gap-2 sm:flex-col sm:items-stretch">
          <Button disabled={!!busy} onClick={() => run("fork")}>
            {busy === "fork" ? t("heldDialog.working") : t("heldDialog.fork")}
          </Button>
          {known ? (
            <Button variant="outline" disabled={!!busy} onClick={() => run("kill")}>
              {busy === "kill" ? t("heldDialog.killing") : t("heldDialog.kill")}
            </Button>
          ) : (
            <Button
              variant="outline"
              disabled={!!busy}
              onClick={() => {
                onClose();
                onDone();
              }}
            >
              {t("heldDialog.closedRetry")}
            </Button>
          )}
          <AlertDialogCancel disabled={!!busy}>{t("common.cancel")}</AlertDialogCancel>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  );
}
