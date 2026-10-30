import clsx from "clsx";

const VARIANTS = {
  default: "bg-primary text-primary-foreground hover:bg-primary/90",
  outline: "border bg-background hover:bg-accent",
  ghost: "hover:bg-accent",
};
const SIZES = { sm: "h-8 gap-1.5 px-3 text-[13px]", icon: "size-8" };

export function Button({
  variant = "default",
  size = "sm",
  className,
  ...props
}: React.ButtonHTMLAttributes<HTMLButtonElement> & { variant?: keyof typeof VARIANTS; size?: keyof typeof SIZES }) {
  return (
    <button
      className={clsx("inline-flex shrink-0 items-center justify-center rounded-md font-medium disabled:opacity-50", VARIANTS[variant], SIZES[size], className)}
      {...props}
    />
  );
}
