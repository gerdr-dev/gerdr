import type { HTMLAttributes, InputHTMLAttributes, KeyboardEvent, MouseEvent, ReactNode, Ref } from "react";
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

type RowProps = Omit<HTMLAttributes<HTMLDivElement>, "children"> & {
  /** Not answerable now (being sent, or the card is settled): no click, no hover. */
  disabled?: boolean;
  label: ReactNode;
  /** Muted text after the label (what the option costs, what it means). */
  desc?: ReactNode;
  /** Its position, shown as the number in front. */
  n: number;
  on?: boolean;
  /** Where the keyboard is: a brand ring, so it is told apart from hover at a glance. */
  focused?: boolean;
  /** How a picked row is tinted: neutral, warn for a card waiting on the user, ok once its answer went through. */
  tone?: "muted" | "warn" | "ok";
  /** Something before the text (a checkbox). */
  lead?: ReactNode;
  /** The number / ✓ in front; off when `lead` already shows the state. */
  badge?: boolean;
  /** Greyed out: the card is answered and this wasn't the pick. */
  dim?: boolean;
};

const pickedRow = {
  muted: "bg-muted hover:bg-muted",
  warn: "border-warn/40 bg-warn/10 hover:bg-warn/10",
  ok: "border-ok/40 bg-ok/10 hover:bg-ok/10",
};
const pickedBadge = { muted: "bg-foreground/10 text-foreground", warn: "bg-warn/20 text-warn", ok: "bg-ok/20 text-ok" };

/*
 * One option: a bordered row, its number in front (✓ when picked), then label and muted description.
 * The number leads and the row has an edge because options are set in the same size and colour as the question: with
 * the number trailing on the right and no edge, a long option read as more of the question's text (user report).
 * The number and lead sit on the first line (h-[1lh]), not centred on a wrapped option.
 */
export function ChoiceRow({
  label,
  desc,
  n,
  on,
  focused,
  tone = "muted",
  lead,
  badge = true,
  dim,
  disabled,
  className,
  onClick,
  onKeyDown,
  ...rest
}: RowProps) {
  // A div acting as a button, not a <button>: text in a button can't be drag-selected (Chromium / WebKit start no
  // selection inside one), and people want to copy an option's text (a domain, a command) out of the card.
  // A click that ends a drag-selection inside the row is that selection, not a pick.
  const click = (e: MouseEvent<HTMLDivElement>) => {
    if (disabled) return;
    const sel = window.getSelection();
    if (sel && !sel.isCollapsed && sel.anchorNode && e.currentTarget.contains(sel.anchorNode)) return;
    onClick?.(e);
  };
  const key = (e: KeyboardEvent<HTMLDivElement>) => {
    onKeyDown?.(e);
    if (e.defaultPrevented || disabled || e.target !== e.currentTarget) return;
    if (e.key === "Enter" || e.key === " ") {
      e.preventDefault();
      onClick?.(e as unknown as MouseEvent<HTMLDivElement>);
    }
  };
  return (
    <div
      role="button"
      tabIndex={disabled ? -1 : 0}
      aria-disabled={disabled || undefined}
      className={cn(
        "flex w-full cursor-pointer items-start gap-2.5 rounded-lg border px-2.5 py-2 text-left text-[1em] leading-normal outline-none ring-1 ring-transparent ring-inset transition-colors select-text",
        disabled ? "cursor-default" : "hover:bg-muted/60",
        on && pickedRow[tone],
        focused && "bg-brand/10 ring-brand/50 hover:bg-brand/10",
        dim && "opacity-50",
        className,
      )}
      onClick={click}
      onKeyDown={key}
      {...rest}
    >
      {lead && <span className="flex h-[1lh] shrink-0 items-center">{lead}</span>}
      {badge && (
        <span className="flex h-[1lh] shrink-0 items-center select-none">
          <span
            className={cn(
              "flex size-5 items-center justify-center rounded-md bg-muted text-[11px] text-muted-foreground tabular-nums",
              focused && "bg-brand/15 text-brand",
              on && pickedBadge[tone],
            )}
          >
            {on ? <Check className="size-3" /> : n + 1}
          </span>
        </span>
      )}
      <span className="min-w-0 flex-1">
        <span>{label}</span>
        {desc && <span className="ml-1.5 text-muted-foreground">{desc}</span>}
      </span>
    </div>
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
