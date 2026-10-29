import { useSyncExternalStore } from "react";

/*
 * askConfirm: an in-app replacement for window.confirm(), drawn with shadcn's AlertDialog (components/ConfirmDialog.tsx).
 * The native one is an OS sheet that looks nothing like the rest of gerdr (and, in the Android app's WebView, like neither).
 *
 * Kept apart from store.dialog on purpose: that holds a single overlay, and confirmations often sit on top of one
 * (discarding an edit in Settings → Memory) — routing them through it would close the dialog underneath.
 */

export type ConfirmOptions = {
  title: string;
  description?: string;
  /** label of the confirm button (default "确定") */
  action?: string;
  /** red confirm button: the action closes, stops or deletes something */
  destructive?: boolean;
};

type Pending = ConfirmOptions & { resolve: (ok: boolean) => void };

let pending: Pending | null = null;
const subs = new Set<() => void>();
const emit = () => subs.forEach((f) => f());

/** Resolves true on confirm, false on cancel / Esc / clicking away. A second call while one is open cancels the first. */
export function askConfirm(opts: ConfirmOptions): Promise<boolean> {
  pending?.resolve(false);
  return new Promise((resolve) => {
    pending = { ...opts, resolve };
    emit();
  });
}

export function settleConfirm(ok: boolean) {
  const p = pending;
  pending = null;
  emit();
  p?.resolve(ok);
}

export const usePendingConfirm = () =>
  useSyncExternalStore(
    (f) => (subs.add(f), () => void subs.delete(f)),
    () => pending,
  );
