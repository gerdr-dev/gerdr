import { useCallback, useEffect, useRef, useState } from "react";

/** A copy button's own "copied" state: copy(text) writes the clipboard and `copied` is true for a moment, so the button
 *  itself turns into a ✓. Used instead of a "Copied" toast: toasts sit in a window corner and the bottom-right one covered
 *  the rightmost pane's composer (user report); the confirmation belongs on the thing clicked. */
export function useCopied(ms = 1200): [boolean, (text: string) => void] {
  const [copied, setCopied] = useState(false);
  const timer = useRef<ReturnType<typeof setTimeout>>(undefined);
  useEffect(() => () => clearTimeout(timer.current), []);
  const copy = useCallback(
    (text: string) => {
      navigator.clipboard.writeText(text).then(() => {
        setCopied(true);
        clearTimeout(timer.current);
        timer.current = setTimeout(() => setCopied(false), ms);
      });
    },
    [ms],
  );
  return [copied, copy];
}
