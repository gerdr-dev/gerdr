import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { allCommands } from "@/lib/commands";
import { ACTION_TITLES, KEY_GROUPS, formatCombo } from "@/lib/keys";
import { closeDialog, keyFor, useStore } from "@/lib/store";

// The shortcut sheet (prefix+?): the configured keys by group, plugin commands with keys, and the composer's own keys.
export function KeyHelp({ open }: { open: boolean }) {
  const cfg = useStore((s) => s.config);
  useStore((s) => s.pluginVersion);
  const plugin = open ? allCommands().filter((c) => c.source !== "builtin" && c.keys.length) : [];
  return (
    <Dialog open={open} onOpenChange={(o) => !o && closeDialog()}>
      <DialogContent className="max-h-[85vh] max-w-3xl overflow-auto">
        <DialogHeader>
          <DialogTitle>快捷键</DialogTitle>
          <DialogDescription>
            先按前缀 <kbd className="rounded-sm border bg-muted px-1 font-mono">{formatCombo(cfg.prefix)}</kbd> 松开，再按后面那个键（和 herdr / tmux
            一样）。中文输入法开着也能用。
          </DialogDescription>
        </DialogHeader>
        <div className="grid grid-cols-1 gap-x-8 gap-y-4 sm:grid-cols-2">
          {KEY_GROUPS.map(([g, acts]) => (
            <div key={g}>
              <div className="mb-1 text-[11px] font-medium uppercase tracking-wide text-muted-foreground">{g}</div>
              {acts.map((a) => (
                <div key={a} className="flex items-center justify-between py-0.5 text-[13px]">
                  <span>{a === "tab1" ? "跳到 tab 1–9" : ACTION_TITLES[a]}</span>
                  <span className="font-mono text-[12px] text-muted-foreground">
                    {keyFor(cfg, a)
                      .map((k) => (a === "tab1" ? formatCombo(k).replace(/1$/, "1…9") : formatCombo(k)))
                      .join("  ") || "—"}
                  </span>
                </div>
              ))}
            </div>
          ))}
          {plugin.length > 0 && (
            <div>
              <div className="mb-1 text-[11px] font-medium uppercase tracking-wide text-muted-foreground">插件</div>
              {plugin.map((c) => (
                <div key={c.id} className="flex items-center justify-between py-0.5 text-[13px]">
                  <span>{c.title}</span>
                  <span className="font-mono text-[12px] text-muted-foreground">{formatCombo(c.keys[0])}</span>
                </div>
              ))}
            </div>
          )}
          <div>
            <div className="mb-1 text-[11px] font-medium uppercase tracking-wide text-muted-foreground">输入框里</div>
            {[
              ["发送 / 插话（等这一步做完）", cfg.sendKey === "enter" ? "↵" : "⌘↵"],
              ...(cfg.sendKey === "enter" ? [["立即插话（打断这一步）", "Ctrl+↵"]] : []),
              ["换行", cfg.sendKey === "enter" ? "⇧↵" : "↵"],
              ["打断这一轮", "Esc"],
              ["斜杠命令", "/"],
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
