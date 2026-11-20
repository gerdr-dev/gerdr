import type { ReactNode } from "react";
import { AppWindow, Globe, SquareArrowOutUpRight, Undo2 } from "lucide-react";
import { Button } from "@/components/ui/button";
import { openBrowserWindow } from "@/lib/actions";
import { t } from "@/lib/i18n";
import { nativeBrowserWindow, nativeBrowserWindowClose } from "@/lib/native";
import { cn } from "@/lib/utils";

// A page moved to a window of its own (core/SessionBrowser.tsx BrowserWindow, internal/desktop browserwin.go) is in
// one place: where it came from (a panel's browser column, a Code Browser pane) shows this card instead of a second
// view of it, until its window closes and it is back.

/** "Open in new window": the page moves to its own window, or that window comes forward (onOut: after the move). */
export function PopOutButton({ project, tab, onOut }: { project: string; tab: number; onOut?: () => void }) {
  return (
    <Button
      variant="ghost"
      size="icon"
      className="size-6 shrink-0"
      title={t("browser.newWindow")}
      onClick={() => {
        openBrowserWindow(project, tab);
        onOut?.();
      }}
    >
      <SquareArrowOutUpRight className="size-3.5" />
    </Button>
  );
}

/**
 * The card for a page that is out: its address in a title row like the browser's own (headClassName, its drag, end:
 * what else the row holds), then "show the window" / "put it back" (its window closes, the page comes back here).
 */
export function BrowserOutCard({
  project,
  tab,
  url,
  headClassName,
  onHeadPointerDown,
  end,
  Body,
}: {
  project: string;
  tab: number;
  url?: string;
  headClassName?: string;
  onHeadPointerDown?: (e: React.PointerEvent<HTMLElement>) => void;
  end?: ReactNode;
  Body?: React.ComponentType<{ children: ReactNode }>;
}) {
  const card = (
    <div className="grid h-full w-full place-items-center bg-muted/40 p-6 text-center">
      <div className="max-w-xs space-y-3">
        <AppWindow className="mx-auto size-8 text-muted-foreground" />
        <div className="text-[13px] text-muted-foreground">{t("browser.inWindow")}</div>
        <div className="flex flex-wrap justify-center gap-2">
          <Button size="sm" variant="outline" onClick={() => nativeBrowserWindow(project, tab)}>
            <AppWindow /> {t("browser.showWindow")}
          </Button>
          <Button size="sm" variant="outline" onClick={() => nativeBrowserWindowClose(tab)}>
            <Undo2 /> {t("browser.backToPanel")}
          </Button>
        </div>
      </div>
    </div>
  );
  return (
    <>
      <div onPointerDown={onHeadPointerDown} className={cn("flex shrink-0 items-center gap-1 border-b px-2 text-[12px]", headClassName ?? "h-10")}>
        <Globe className="mr-1 size-4 shrink-0 text-muted-foreground" />
        <span className="min-w-0 flex-1 truncate font-mono text-[11px] text-muted-foreground">{url}</span>
        {end}
      </div>
      {Body ? <Body>{card}</Body> : <div className="min-h-0 flex-1">{card}</div>}
    </>
  );
}
