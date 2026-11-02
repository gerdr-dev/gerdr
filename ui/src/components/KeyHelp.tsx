import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { allCommands } from "@/lib/commands";
import { ACTION_TITLES, KEY_GROUPS, formatCombo } from "@/lib/keys";
import { closeDialog, keyFor, prefOf, useStore } from "@/lib/store";
import { t } from "@/lib/i18n";

/** The 1–9 families, folded into one line each. */
const NUMBERED: Partial<Record<string, () => string>> = {
  tab1: () => t("keyHelp.tabs"),
  active1: () => t("keyHelp.active"),
  ws1: () => t("keyHelp.workspaces"),
};

// The shortcut sheet (prefix+?): the configured keys by group, plugin commands with keys, and the composer's own keys.
export function KeyHelp({ open }: { open: boolean }) {
  const cfg = useStore((s) => s.config);
  const sendKey = useStore((s) => prefOf(s, "sendKey"));
  useStore((s) => s.addonVersion);
  // The prefix key sits mid-sentence: split the message around it so each language puts it where it reads right
  const [introBefore, introAfter = ""] = t("keyHelp.intro", { prefix: "\u0000" }).split("\u0000");
  const plugin = open ? allCommands().filter((c) => c.source !== "builtin" && c.keys.length) : [];
  return (
    <Dialog open={open} onOpenChange={(o) => !o && closeDialog()}>
      <DialogContent className="max-h-[85vh] max-w-3xl overflow-auto">
        <DialogHeader>
          <DialogTitle>{t("keyHelp.title")}</DialogTitle>
          <DialogDescription>
            {introBefore}
            <kbd className="rounded-sm border bg-muted px-1 font-mono">{formatCombo(cfg.prefix)}</kbd>
            {introAfter}
          </DialogDescription>
        </DialogHeader>
        <div className="grid grid-cols-1 gap-x-8 gap-y-4 sm:grid-cols-2">
          {KEY_GROUPS.map(([g, acts]) => (
            <div key={g}>
              <div className="mb-1 text-[11px] font-medium uppercase tracking-wide text-muted-foreground">{g}</div>
              {acts.map((a) => (
                <div key={a} className="flex items-center justify-between py-0.5 text-[13px]">
                  <span>{NUMBERED[a]?.() ?? ACTION_TITLES[a]}</span>
                  <span className="font-mono text-[12px] text-muted-foreground">
                    {keyFor(cfg, a)
                      .map((k) => (NUMBERED[a] ? formatCombo(k).replace(/1$/, "1…9") : formatCombo(k)))
                      .join("  ") || "—"}
                  </span>
                </div>
              ))}
            </div>
          ))}
          {plugin.length > 0 && (
            <div>
              <div className="mb-1 text-[11px] font-medium uppercase tracking-wide text-muted-foreground">{t("keyHelp.plugins")}</div>
              {plugin.map((c) => (
                <div key={c.id} className="flex items-center justify-between py-0.5 text-[13px]">
                  <span>{c.title}</span>
                  <span className="font-mono text-[12px] text-muted-foreground">{formatCombo(c.keys[0])}</span>
                </div>
              ))}
            </div>
          )}
          <div>
            <div className="mb-1 text-[11px] font-medium uppercase tracking-wide text-muted-foreground">{t("keyHelp.composer")}</div>
            {[
              [t("keyHelp.send"), sendKey === "enter" ? "↵" : "⌘↵"],
              ...(sendKey === "enter" ? [[t("keyHelp.steerNow"), "Ctrl+↵"]] : []),
              [t("keyHelp.newline"), sendKey === "enter" ? "⇧↵" : "↵"],
              [t("keyHelp.stop"), "Esc"],
              [t("keyHelp.slash"), "/"],
            ].map(([a, k]) => (
              <div key={a} className="flex items-center justify-between py-0.5 text-[13px]">
                <span>{a}</span>
                <span className="font-mono text-[12px] text-muted-foreground">{k}</span>
              </div>
            ))}
          </div>
        </div>
      </DialogContent>
    </Dialog>
  );
}
