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
          <AlertDialogTitle>这个会话还在别处开着</AlertDialogTitle>
          <AlertDialogDescription asChild>
            <div className="space-y-2 text-[13px]">
              {known ? (
                <p>
                  {h?.name ? `「${h.name}」` : "另一个终端"}里的 claude（pid {h?.pid}
                  {h?.status ? `，${h.status === "busy" ? "正在干活" : "闲着"}` : ""}）也开着它。两边同时写，对话会分叉：谁也看不见对方说过的话。
                </p>
              ) : (
                <p>{h?.name ?? "别处"}正开着它，同一时间只能有一处接着聊。在那边关掉它再重试，或者在这里另起一份。</p>
              )}
              <p>你的消息还没发出去，在输入框里。</p>
            </div>
          </AlertDialogDescription>
        </AlertDialogHeader>
        <AlertDialogFooter className="gap-2 sm:flex-col sm:items-stretch">
          <Button disabled={!!busy} onClick={() => run("fork")}>
            {busy === "fork" ? "处理中…" : "另起一份，在这里接着聊（推荐）"}
          </Button>
          {known ? (
            <Button variant="outline" disabled={!!busy} onClick={() => run("kill")}>
              {busy === "kill" ? "正在关掉那边…" : "关掉那边的 claude，在这里接着聊"}
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
              我已经在那边关掉了，重试
            </Button>
          )}
          <AlertDialogCancel disabled={!!busy}>取消</AlertDialogCancel>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  );
}
