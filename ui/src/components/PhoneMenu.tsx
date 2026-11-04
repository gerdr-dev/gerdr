import { set } from "@/lib/store";
import type { PhoneMenuItem } from "@/core/phoneMenu";

/**
 * The shown panel's own items in the phone drawer (core/phoneMenu.ts), where Code's list of tabs would be: the drawer
 * opens from the panel's top-left button, and what is worth a tap there is the panel's to say. Rows the size of the
 * drawer's other rows; a tap closes the drawer first, so whatever the item opens isn't under it.
 */
export function PanelPhoneMenu({ items }: { items: PhoneMenuItem[] }) {
  return (
    <div className="min-h-0 flex-1 overflow-y-auto px-1.5 pt-1.5 pb-2">
      {items.map((x) => (
        <button
          key={x.id ?? x.label}
          className="flex min-h-11 w-full items-center gap-2.5 rounded-md px-2.5 text-left text-[13px] hover:bg-accent/60 active:bg-accent"
          onClick={() => {
            set({ drawer: false });
            x.onSelect();
          }}
        >
          {x.icon && <span className="flex size-4 shrink-0 items-center justify-center text-muted-foreground [&_svg]:size-4">{x.icon}</span>}
          <span className="min-w-0 flex-1 truncate">{x.label}</span>
          {x.badge != null && x.badge !== "" && <span className="shrink-0 text-[12px] text-muted-foreground tabular-nums">{x.badge}</span>}
        </button>
      ))}
    </div>
  );
}
