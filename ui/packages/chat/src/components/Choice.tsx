import type { ButtonHTMLAttributes, HTMLAttributes, InputHTMLAttributes, ReactNode, Ref } from "react";
import { Check } from "lucide-react";
import { cn } from "../utils";

/*
 * The look shared by every card that asks the user to pick: the chat's own question card (AskCard) and gerdr's tool
 * cards (a panel's "decide this" card). Only how they look lives here; what a pick does, keys, undo, paging stay with
 * each card, because they are different things (one answers a turn that is waiting, the other calls a panel's function).
 * Restyle here and both follow; before, each kept its own copy of the same classes and every change had to be made twice.
 */

/** The card's frame. */
export function ChoiceCard({ className, ...rest }: HTMLAttributes<HTMLDivElement> & { ref?: Ref<HTMLDivElement> }) {
  return <div className={cn("my-1.5 rounded-xl border bg-card p-4 shadow-sm", className)} {...rest} />;
}

/** The small line above the question: an icon, then a header / progress / who's asking. */
export function ChoiceKicker({ icon, children, className }: { icon: ReactNode; children?: ReactNode; className?: string }) {
  return (
    <div className={cn("mb-1.5 flex items-center gap-1.5 text-[12px] text-muted-foreground", className)}>
      {icon}
      {children}
    </div>
  );
}

/** The question itself, at the chat's text size and weight, so it reads like the reply around it. */
export function ChoiceTitle({ children, className }: { children: ReactNode; className?: string }) {
  return <div className={cn("text-[1em] leading-relaxed", className)}>{children}</div>;
}

/** The options' column. -mx-2.5 cancels the rows' px-2.5: their text lines up with the question, the highlight reaches past it. */
export function ChoiceList({ children, className }: { children: ReactNode; className?: string }) {
  return <div className={cn("-mx-2.5 flex flex-col gap-1.5", className)}>{children}</div>;
}

type RowProps = Omit<ButtonHTMLAttributes<HTMLButtonElement>, "children"> & {
  label: ReactNode;
  /** Muted text after the label (what the option costs, what it means). */
  desc?: ReactNode;
  /** Its position, shown as the number on the right. */
  n: number;
  on?: boolean;
  /** Where the keyboard is: a brand ring, so it is told apart from hover at a glance. */
  focused?: boolean;
  /** How a picked row is tinted: neutral, or warn for a card waiting on the user. */
  tone?: "muted" | "warn";
  /** Something before the text (a checkbox). */
  lead?: ReactNode;
  /** The number / ✓ on the right; off when `lead` already shows the state. */
  badge?: boolean;
  /** Greyed out: the card is answered and this wasn't the pick. */
  dim?: boolean;
};

/** One option: a flat row, label then muted description, its number on the right turning ✓ when picked. */
export function ChoiceRow({ label, desc, n, on, focused, tone = "muted", lead, badge = true, dim, className, ...rest }: RowProps) {
  return (
    <button
      type="button"
      className={cn(
        "flex w-full items-center gap-3 rounded-lg px-2.5 py-2.5 text-left text-[1em] outline-none ring-1 ring-transparent ring-inset transition-colors enabled:hover:bg-muted/60",
        on && (tone === "warn" ? "bg-warn/10 enabled:hover:bg-warn/10" : "bg-muted enabled:hover:bg-muted"),
        focused && "bg-brand/10 ring-brand/50 enabled:hover:bg-brand/10",
        dim && "opacity-50",
        className,
      )}
      {...rest}
    >
      {lead}
      <span className="min-w-0 flex-1">
        <span>{label}</span>
        {desc && <span className="ml-1.5 text-muted-foreground">{desc}</span>}
      </span>
      {badge && (
        <span
          className={cn(
            "flex size-5 shrink-0 items-center justify-center rounded-md border text-[11px] text-muted-foreground tabular-nums",
            focused && "border-brand/60 text-brand",
            on && (tone === "warn" ? "border-transparent bg-warn/20 text-warn" : "border-transparent bg-foreground/10 text-foreground"),
          )}
        >
          {on ? <Check className="size-3" /> : n + 1}
        </span>
      )}
    </button>
  );
}

/** The "other, write your own" line under the options. */
export function ChoiceOther({ className, ...rest }: InputHTMLAttributes<HTMLInputElement> & { ref?: Ref<HTMLInputElement> }) {
  return (
    <input
      className={cn(
        "w-full rounded-lg border border-dashed bg-transparent px-2.5 py-2 text-[1em] outline-none placeholder:text-muted-foreground/70 focus:border-solid focus:border-ring/60",
        className,
      )}
      {...rest}
    />
  );
}
