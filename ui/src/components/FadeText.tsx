import { useLayoutEffect, useRef, useState } from "react";
import { cn } from "@/lib/utils";

/**
 * Text that fades out at its right edge when it doesn't fit, instead of ending in "…": the same look as the tab bar's
 * names (TabBar.tsx). Unlike a tab name, this span is sized to its text, so an always-on fade would eat the last letters
 * of a short one; the fade goes on only while the text actually overflows.
 *
 * `start` keeps the end and cuts the beginning instead, for paths: their last segments say where you are. The box is
 * right-to-left so the overflow spills off its left edge; the text inside stays left-to-right, so "/" doesn't move.
 */
export function FadeText({ className, title, start, children }: { className?: string; title?: string; start?: boolean; children: React.ReactNode }) {
  const ref = useRef<HTMLSpanElement>(null);
  const [over, setOver] = useState(false);
  useLayoutEffect(() => {
    const el = ref.current;
    if (!el) return;
    const check = () => setOver(el.scrollWidth > el.clientWidth + 1);
    check();
    const ro = new ResizeObserver(check);
    ro.observe(el);
    return () => ro.disconnect();
  }, [children]);
  return (
    <span
      ref={ref}
      title={title}
      dir={start ? "rtl" : undefined}
      className={cn(
        "overflow-hidden whitespace-nowrap",
        over &&
          (start
            ? "[mask-image:linear-gradient(to_left,black_calc(100%-1rem),transparent)]"
            : "[mask-image:linear-gradient(to_right,black_calc(100%-1rem),transparent)]"),
        className,
      )}
    >
      {start ? <span dir="ltr">{children}</span> : children}
    </span>
  );
}
