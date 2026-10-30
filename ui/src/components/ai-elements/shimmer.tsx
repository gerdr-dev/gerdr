"use client";

import { cn } from "@/lib/utils";
import { motion } from "motion/react";
import {
  type CSSProperties,
  type ElementType,
  type JSX,
  memo,
  useMemo,
} from "react";

export type TextShimmerProps = {
  children: string;
  as?: ElementType;
  className?: string;
  duration?: number;
  spread?: number;
};

// motion.create() returns a new component type on every call. Called inside render (as the upstream ai-elements
// version does), each re-render unmounted the element and mounted a fresh one; under a streaming parent (thought
// chunks re-render it many times a second) the text — transparent, painted only by the clipped background — never
// got painted on phones and showed as a blank gap. One motion component per tag, made once.
const motionByTag = new Map<ElementType, typeof motion.p>();
function motionFor(tag: ElementType) {
  let c = motionByTag.get(tag);
  if (!c) {
    c = motion.create(tag as keyof JSX.IntrinsicElements) as typeof motion.p;
    motionByTag.set(tag, c);
  }
  return c;
}

const ShimmerComponent = ({
  children,
  as: Component = "p",
  className,
  duration = 2,
  spread = 2,
}: TextShimmerProps) => {
  const MotionComponent = motionFor(Component);

  const dynamicSpread = useMemo(
    () => (children?.length ?? 0) * spread,
    [children, spread]
  );

  return (
    <MotionComponent
      animate={{ backgroundPosition: "0% center" }}
      className={cn(
        "relative inline-block bg-[length:250%_100%,auto] bg-clip-text text-transparent",
        "[--bg:linear-gradient(90deg,#0000_calc(50%-var(--spread)),var(--color-background),#0000_calc(50%+var(--spread)))] [background-repeat:no-repeat,padding-box]",
        className
      )}
      initial={{ backgroundPosition: "100% center" }}
      style={
        {
          "--spread": `${dynamicSpread}px`,
          backgroundImage:
            "var(--bg), linear-gradient(var(--color-muted-foreground), var(--color-muted-foreground))",
        } as CSSProperties
      }
      transition={{
        repeat: Number.POSITIVE_INFINITY,
        duration,
        ease: "linear",
      }}
    >
      {children}
    </MotionComponent>
  );
};

export const Shimmer = memo(ShimmerComponent);
