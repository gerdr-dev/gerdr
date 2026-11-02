import { PixelLoader } from "@gerdr/chat";
import { useState } from "react";
import { Switch } from "@/components/ui/switch";
import { slots } from "@/lib/addons";
import { t } from "@/lib/i18n";
import { addonMeta } from "@/lib/rail";
import { setConfig, useStore } from "@/lib/store";
import type { LoaderChoice } from "@/lib/store/config";
import { cn } from "@/lib/utils";
import { LoaderPreview, loaderPick } from "./AddonSlot";

// Settings → 扩展 → 素材: the sprites add-ons brought (g.slot("sprite")), and which of them — or an add-on's own
// loader, or gerdr's dots — draws the working indicator (config.loader). Sprites draw nowhere until picked here or as a
// rail button's face (→ 分组), so installing a picture set never changes the interface by itself.
//
// Each option is previewed through the real indicator (PixelLoader → AddonLoader with LoaderPreview), so what's shown
// here is exactly what a running turn shows.

export function SpriteSettings() {
  useStore((s) => s.addonVersion);
  const chosen = useStore((s) => s.config.loader);
  const [waiting, setWaiting] = useState(false);
  // The highlighted option is what draws now: with nothing chosen, what the automatic choice resolves to
  const now = loaderPick(chosen);
  const active: LoaderChoice = !now ? "dots" : "sprite" in now ? `sprite:${now.sprite.key}` : `loader:${now.loader.key}`;
  const options: { choice: LoaderChoice; title: string; from?: string }[] = [
    { choice: "dots", title: t("sprites.dots") },
    ...[...slots.loader, ...slots.sprite].map((r) => {
      const from = addonMeta(r.addon)?.name ?? r.addon;
      const choice: LoaderChoice = `${slots.loader.includes(r) ? "loader" : "sprite"}:${r.key}`;
      return r.title && r.title !== from ? { choice, title: r.title, from } : { choice, title: from };
    }),
  ];
  return (
    <div className="space-y-3">
      <div className="flex items-center gap-3">
        <div className="min-w-0 flex-1">
          <div className="text-[13px] font-medium">{t("sprites.loader")}</div>
          <div className="text-[12px] text-muted-foreground">{t("sprites.loader.hint")}</div>
        </div>
        <label className="flex shrink-0 items-center gap-2 text-[12px] text-muted-foreground">
          {t("sprites.waiting")}
          <Switch checked={waiting} onCheckedChange={setWaiting} />
        </label>
      </div>
      <div className="grid grid-cols-[repeat(auto-fill,minmax(7.5rem,1fr))] gap-2">
        {options.map((o) => (
          <button
            key={o.choice}
            type="button"
            onClick={() => setConfig({ loader: o.choice })}
            className={cn(
              "flex flex-col items-center gap-2 rounded-lg border px-2 pt-4 pb-2.5 text-[12px]",
              active === o.choice ? "border-brand bg-accent" : "hover:bg-accent",
            )}
          >
            <span className="flex h-8 items-center justify-center">
              <LoaderPreview.Provider value={o.choice}>
                <PixelLoader paused={waiting} className={waiting ? "text-warn" : "text-brand"} />
              </LoaderPreview.Provider>
            </span>
            <span className="w-full truncate text-center">{o.title}</span>
            <span className="-mt-2 w-full truncate text-center text-[11px] text-muted-foreground">{o.from ?? "\u00a0"}</span>
          </button>
        ))}
      </div>
      <p className="text-[12px] text-muted-foreground">{slots.sprite.length ? t("sprites.faces") : t("sprites.none")}</p>
    </div>
  );
}
