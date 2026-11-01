"use client";

import { cn } from "../utils";
import { type CSSProperties, type ElementType, type JSX, memo } from "react";

export type TextShimmerProps = {
  children: string;
  as?: ElementType;
  className?: string;
  duration?: number;
  spread?: number;
};

// A label with a light band sweeping across it. The look lives in style.css (.gerdr-shimmer): real text color plus a
// moving mask, a plain CSS animation. The upstream ai-elements version made the text transparent and painted it through
// background-clip:text, animated from JS by motion; on phones that layer sometimes never got painted and "思考中…"
// showed as a blank gap. The band is at least 16px: a short CJK label ("思考中") gave a 6px sliver.
const ShimmerComponent = ({ children, as: Component = "p", className, duration = 2, spread = 2 }: TextShimmerProps): JSX.Element => (
  <Component
    className={cn("gerdr-shimmer relative inline-block", className)}
    style={
      {
        "--gerdr-shimmer-spread": `${Math.max((children?.length ?? 0) * spread, 16)}px`,
        "--gerdr-shimmer-duration": `${duration}s`,
      } as CSSProperties
    }
  >
    {children}
  </Component>
);

export const Shimmer = memo(ShimmerComponent);
