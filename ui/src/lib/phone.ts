// Moved into the chat package (it needs the same breakpoints); re-exported for the rest of gerdr.
export * from "@gerdr/chat/device";
import { useEffect, useState } from "react";

/**
 * `--vvh` for one box: the visual viewport's height (what's left above a soft keyboard, which iOS doesn't take out of
 * the layout viewport), as a style to put on a dialog that sizes itself with `var(--vvh, …)`. It lives on the box, not
 * on html: a custom property changed on html restyles every element in the page (packages/chat device.ts).
 * `on`: only while the box is open, so a closed one doesn't re-render on every keyboard.
 */
export function useVvhStyle(on = true): React.CSSProperties {
  const [h, setH] = useState(() => Math.round(window.visualViewport?.height ?? innerHeight));
  useEffect(() => {
    const vv = window.visualViewport;
    if (!vv || !on) return;
    setH(Math.round(vv.height));
    const f = () => setH(Math.round(vv.height));
    vv.addEventListener("resize", f);
    return () => vv.removeEventListener("resize", f);
  }, [on]);
  return { "--vvh": `${h}px` } as React.CSSProperties;
}
