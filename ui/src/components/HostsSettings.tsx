import { Loader2, Monitor, Server, Trash2 } from "lucide-react";
import { useState } from "react";
import { toast } from "sonner";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover";
import { api } from "@/lib/api";
import { t } from "@/lib/i18n";
import { set, useStore } from "@/lib/store";
import { cn } from "@/lib/utils";

/**
 * Settings → Remote access, "Computers this one controls" (docs/dev/MULTIHOST.md): paste the invite another
 * computer's Gerdr gave and its sessions show up here next to this one's. The list comes from the store ("hosts" frames).
 */
export function HostsSettings() {
  const hosts = useStore((s) => s.hosts);
  const [invite, setInvite] = useState("");
  const [busy, setBusy] = useState(false);
  const [confirm, setConfirm] = useState("");

  const add = async () => {
    if (!invite.trim() || busy) return;
    setBusy(true);
    try {
      const h = await api.addHost(invite.trim());
      setInvite("");
      toast.success(t("hostsSettings.added", { name: h.name }));
    } catch (e: any) {
      toast.error(e.message ?? String(e));
    } finally {
      setBusy(false);
    }
  };

  return (
    <div className="space-y-1.5">
      <div className="text-[13px] font-medium">{t("hostsSettings.title")}</div>
      <div className="text-[12px] text-muted-foreground">{t("hostsSettings.desc")}</div>
      {hosts.map((h) => (
        <div key={h.id} className="flex items-center gap-2 rounded-md border px-3 py-2">
          <Server className="size-4 shrink-0 text-muted-foreground" />
          <div className="min-w-0 flex-1">
            <div className="flex items-center gap-1.5 text-[13px]">
              {h.name}
              <span className={cn("size-1.5 rounded-full", h.online ? "bg-ok" : "bg-muted-foreground/40")} />
            </div>
            <div className="truncate font-mono text-[11px] text-muted-foreground">{h.url}</div>
            {h.error && <div className="truncate text-[11px] text-bad">{h.error}</div>}
          </div>
          {/* Its screen, driven from here: needs "Control this computer remotely" on over there */}
          <Button size="sm" variant="ghost" disabled={!h.online} onClick={() => set({ dialog: { kind: "screen", host: h.id } })}>
            <Monitor className="size-3.5" />
            {t("hostsSettings.screen")}
          </Button>
          {/* Two clicks: its tabs stop working until it's paired again */}
          <Button
            size="sm"
            variant={confirm === h.id ? "destructive" : "ghost"}
            onClick={() =>
              confirm === h.id
                ? api.removeHost(h.id).then(
                    () => setConfirm(""),
                    (e) => toast.error(e.message),
                  )
                : setConfirm(h.id)
            }
            onBlur={() => setConfirm("")}
            title={t("hostsSettings.remove")}
          >
            {confirm === h.id ? t("hostsSettings.removeConfirm") : <Trash2 className="size-3.5" />}
          </Button>
        </div>
      ))}
      <div className="flex gap-2">
        <Input
          className="font-mono text-[12px]"
          value={invite}
          placeholder={t("hostsSettings.placeholder")}
          onChange={(e) => setInvite(e.target.value)}
          onKeyDown={(e) => e.key === "Enter" && !e.nativeEvent.isComposing && add()}
        />
        <Button size="sm" disabled={busy || !invite.trim()} onClick={add}>
          {busy && <Loader2 className="size-3.5 animate-spin" />}
          {t("hostsSettings.add")}
        </Button>
      </div>
      {hosts.length > 0 && <div className="text-[11px] text-warn">{t("hostsSettings.trust")}</div>}
    </div>
  );
}

/**
 * On the computer to be controlled: a one-off invite (address + peer code) to paste into the other Gerdr. Local only,
 * like pairing codes (server/remoteapi.go): whoever holds a peer device can run anything here.
 */
export function PeerInviteButton() {
  const [inv, setInv] = useState<{ url: string; expires: string } | null>(null);
  return (
    <Popover
      open={!!inv}
      onOpenChange={(o) =>
        o
          ? api.peerInvite().then(
              (r) => setInv(r),
              (e) => toast.error(e.message),
            )
          : setInv(null)
      }
    >
      <PopoverTrigger asChild>
        <span className="inline-flex">
          <Button size="sm" variant="outline">
            {t("hostsSettings.invite")}
          </Button>
        </span>
      </PopoverTrigger>
      {inv && (
        <PopoverContent align="end" className="w-96 space-y-2 text-[13px]">
          <div>{t("hostsSettings.inviteHow")}</div>
          <div className="rounded-md border bg-muted/40 p-2 font-mono text-[12px] break-all select-all">{inv.url}</div>
          <div className="flex items-center gap-2">
            <Button size="sm" onClick={() => navigator.clipboard.writeText(inv.url).then(() => toast.success(t("hostsSettings.copied")))}>
              {t("hostsSettings.copy")}
            </Button>
            <span className="text-[12px] text-muted-foreground">{t("hostsSettings.inviteValid", { time: new Date(inv.expires).toLocaleTimeString() })}</span>
          </div>
          <div className="text-[12px] text-warn">{t("hostsSettings.inviteWarn")}</div>
        </PopoverContent>
      )}
    </Popover>
  );
}
