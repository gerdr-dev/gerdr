import { useEffect, useRef } from "react";
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@/components/ui/alert-dialog";
import { refocusAfterDialog } from "@/lib/actions";
import { settleConfirm, usePendingConfirm, type ConfirmOptions } from "@/lib/confirm";

// The dialog behind askConfirm (lib/confirm.ts). Mounted once in Dialogs.tsx.
export function ConfirmDialog() {
  const p = usePendingConfirm();
  // Keep showing the last question while the close animation plays (pending is already null by then)
  const last = useRef<ConfirmOptions | null>(null);
  if (p) last.current = p;
  const c = p ?? last.current;
  useEffect(() => {
    if (!p) refocusAfterDialog(); // like the other dialogs: back to the focused pane's input (no-op while another dialog is open)
  }, [p]);
  return (
    <AlertDialog open={!!p} onOpenChange={(o) => !o && settleConfirm(false)}>
      <AlertDialogContent className="max-w-sm">
        <AlertDialogHeader>
          <AlertDialogTitle>{c?.title}</AlertDialogTitle>
          {c?.description && <AlertDialogDescription className="whitespace-pre-line">{c.description}</AlertDialogDescription>}
        </AlertDialogHeader>
        <AlertDialogFooter>
          <AlertDialogCancel onClick={() => settleConfirm(false)}>取消</AlertDialogCancel>
          <AlertDialogAction variant={c?.destructive ? "destructive" : "default"} onClick={() => settleConfirm(true)}>
            {c?.action ?? "确定"}
          </AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  );
}
