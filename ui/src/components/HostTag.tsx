import { Server } from "lucide-react";
import { t } from "@/lib/i18n";
import { useStore } from "@/lib/store";
import type { SessionSummary } from "@/lib/types";
import { cn } from "@/lib/utils";

/**
 * Which other computer a session runs on (docs/dev/MULTIHOST.md), next to its title. Nothing for this computer's
 * sessions. Dimmed while that computer is unreachable.
 */
export function HostTag({ session, className, iconOnly }: { session?: Pick<SessionSummary, "host">; className?: string; iconOnly?: boolean }) {
  const id = session?.host?.id;
  const host = useStore((s) => (id ? s.hosts.find((h) => h.id === id) : undefined));
  if (!id) return null;
  const name = host?.name ?? session!.host!.name;
  const online = host?.online ?? false;
  return (
    <span
      className={cn("inline-flex shrink-0 items-center gap-1 rounded-sm bg-muted px-1.5 text-[11px] text-muted-foreground", !online && "opacity-60", className)}
      title={online ? t("hostTag.on", { name }) : t("hostTag.offline", { name, error: host?.error ?? "" })}
    >
      <Server className="size-3" />
      {/* A one-line sidebar row has no room for the name next to the title: the title wins, the name is in the tooltip */}
      {!iconOnly && <span className="max-w-[10em] truncate">{name}</span>}
    </span>
  );
}
