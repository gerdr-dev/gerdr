import { Eye, X } from "lucide-react";
import { useScreenChip } from "@/core/screen";
import { t } from "@/lib/i18n";

/**
 * Above a panel conversation's composer: what goes along with the next message from what the user has on screen
 * (core/screen.ts: the open item, the file, the selected text), so nothing is sent out of sight; × leaves it out.
 */
export function ScreenChip({ sessionId }: { sessionId: string }) {
  const { summary, skip } = useScreenChip(sessionId);
  if (!summary) return null;
  return (
    <div className="mb-2 flex items-center gap-1.5 text-[11px] text-muted-foreground">
      <div className="flex min-w-0 items-center gap-1.5 rounded-md bg-muted py-0.5 pr-0.5 pl-2">
        <Eye className="size-3 shrink-0" />
        <span className="shrink-0">{t("screenChip.label")}</span>
        <span className="min-w-0 truncate text-foreground">{summary}</span>
        <button
          className="shrink-0 rounded-sm p-0.5 hover:bg-accent hover:text-foreground"
          title={t("screenChip.skip")}
          aria-label={t("screenChip.skip")}
          onClick={skip}
        >
          <X className="size-3" />
        </button>
      </div>
    </div>
  );
}
