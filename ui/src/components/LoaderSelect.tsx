import { PixelLoader } from "@gerdr/chat";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { slots } from "@/lib/addons";
import { t } from "@/lib/i18n";
import { addonMeta } from "@/lib/rail";
import { setConfig, useStore } from "@/lib/store";
import type { LoaderChoice } from "@/lib/store/config";
import { LoaderPreview, loaderPick } from "./AddonSlot";

// Settings → General → Look: what draws the working indicator (config.loader) — gerdr's dots, an add-on's loader, or a
// sprite (g.slot("sprite")). A plain row among the other looks rather than a page of its own: a gallery page under
// Add-ons read as a feature to understand, while this is just "which loading animation". Each option is previewed
// through the real indicator (PixelLoader → AddonLoader with LoaderPreview), so it shows exactly what a turn shows.
// Hidden while the dots are the only choice.

export function LoaderSelect({ row }: { row: (control: React.ReactNode) => React.ReactNode }) {
  useStore((s) => s.addonVersion);
  const chosen = useStore((s) => s.config.loader);
  const regs = [...slots.loader, ...slots.sprite];
  if (!regs.length) return null;
  // Shown as chosen: what draws now — with nothing chosen, what the automatic choice resolves to
  const now = loaderPick(chosen);
  const value: LoaderChoice = !now ? "dots" : "sprite" in now ? `sprite:${now.sprite.key}` : `loader:${now.loader.key}`;
  const options: { choice: LoaderChoice; title: string; from?: string }[] = [
    { choice: "dots", title: t("settings.loader.dots") },
    ...regs.map((r) => {
      const from = addonMeta(r.addon)?.name ?? r.addon;
      const choice: LoaderChoice = `${slots.loader.includes(r) ? "loader" : "sprite"}:${r.key}`;
      return r.title && r.title !== from ? { choice, title: r.title, from } : { choice, title: from };
    }),
  ];
  return row(
    <Select value={value} onValueChange={(v) => setConfig({ loader: v as LoaderChoice })}>
      <SelectTrigger className="min-w-36">
        <SelectValue />
      </SelectTrigger>
      <SelectContent>
        {options.map((o) => (
          <SelectItem key={o.choice} value={o.choice}>
            <span className="flex w-6 justify-center">
              <LoaderPreview.Provider value={o.choice}>
                <PixelLoader className="text-brand" />
              </LoaderPreview.Provider>
            </span>
            {o.title}
            {o.from && <span className="text-muted-foreground">{o.from}</span>}
          </SelectItem>
        ))}
      </SelectContent>
    </Select>,
  );
}
