import { useLayoutEffect, useRef, useState } from "react";
import { cn } from "@/lib/utils";

/**
 * Text that fades out at its right edge when it doesn't fit, instead of ending in "…": the same look as the tab bar's
 * names (Chrome.tsx). Unlike a tab name, this span is sized to its text, so an always-on fade would eat the last letters
 * of a short one; the fade goes on only while the text actually overflows.
 */
export function FadeText({ className, title, children }: { className?: string; title?: string; children: React.ReactNode }) {
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
      className={cn("overflow-hidden whitespace-nowrap", over && "[mask-image:linear-gradient(to_right,black_calc(100%-1rem),transparent)]", className)}
    >
      {children}
    </span>
  );
}
