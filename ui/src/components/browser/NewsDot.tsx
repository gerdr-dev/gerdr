import { cn } from "@/lib/utils";

/**
 * The dot on a folded browser (a minimized chip, a panel's globe button): its page opened or went elsewhere since the
 * user last looked (lib/actions/browser.ts useBrowserNews, core/SessionBrowser.tsx Column.news). The parent is
 * positioned.
 */
export function NewsDot({ className }: { className?: string }) {
  return <span className={cn("pointer-events-none absolute -top-0.5 -right-0.5 size-2 rounded-full bg-brand ring-2 ring-background", className)} />;
}
