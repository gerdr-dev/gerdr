import { Check, Copy, ExternalLink, FileText, FolderOpen, Link2, MoreHorizontal, Pin, Plus, Type, X } from "lucide-react";
import { useState } from "react";
import { toast } from "sonner";
import { Button } from "@/components/ui/button";
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger } from "@/components/ui/dropdown-menu";
import { api, type ArtifactItem } from "@/lib/api";
import { t } from "@/lib/i18n";
import { IS_MAC } from "@/lib/keys";
import { isNative, nativeOpenPath, nativeOpenURL } from "@/lib/native";
import { cn } from "@/lib/utils";

/**
 * The records of an artifacts folder (docs/dev/ARTIFACTS.md): what the work left that isn't a file here — a PR, a
 * place on this computer, a line of text — and the pinned files. Never deleted: an outdated one is greyed (and folded
 * away), replaced by a later one.
 */
export function Records({
  items,
  by,
  host,
  onChanged,
  onOpenFile,
  onBrowse,
}: {
  items: ArtifactItem[];
  by: { session: string } | { dir: string };
  host?: string;
  onChanged: () => void;
  onOpenFile: (rel: string) => void;
  onBrowse: (path: string) => void;
}) {
  const [showOld, setShowOld] = useState(false);
  const [adding, setAdding] = useState(false);
  const live = items.filter((i) => !i.superseded).reverse(); // newest first
  const old = items.filter((i) => i.superseded).reverse();
  const pinned = live.filter((i) => i.kind === "file");
  const rest = live.filter((i) => i.kind !== "file");
  const retire = (it: ArtifactItem) =>
    api
      .recordArtifact(by, { kind: "retire" as ArtifactItem["kind"], supersedes: it.id }, host)
      .then(onChanged)
      .catch((e) => toast.error(e.message));
  if (!items.length && !adding)
    return (
      <div className="flex items-center justify-end px-3 pt-2">
        <Button variant="ghost" size="sm" className="h-6 gap-1 px-2 text-[12px] text-muted-foreground" onClick={() => setAdding(true)}>
          <Plus className="size-3.5" /> {t("folder.addRecord")}
        </Button>
      </div>
    );
  return (
    <div className="border-b px-3 py-2">
      <div className="mb-1.5 flex items-center gap-2 text-[11px] font-medium text-muted-foreground">
        {t("folder.records")}
        <div className="flex-1" />
        {old.length > 0 && (
          <button type="button" className="hover:text-foreground" onClick={() => setShowOld((v) => !v)}>
            {showOld ? t("folder.hideOutdated") : t("folder.outdated", { n: old.length })}
          </button>
        )}
        <button type="button" className="flex items-center gap-0.5 hover:text-foreground" onClick={() => setAdding((v) => !v)}>
          <Plus className="size-3" /> {t("folder.addRecord")}
        </button>
      </div>
      {adding && <AddRecord by={by} host={host} onDone={() => (setAdding(false), onChanged())} />}
      {pinned.length > 0 && (
        <div className="mb-1.5 flex flex-wrap gap-1.5">
          {pinned.map((it) => (
            <button
              key={it.id}
              type="button"
              onClick={() => onOpenFile(it.path!)}
              className="flex max-w-full items-center gap-1.5 rounded-md border bg-muted/40 px-2 py-1 text-[12px] hover:bg-accent"
              title={it.path}
            >
              <Pin className="size-3 shrink-0 text-brand" />
              <span className="truncate">{it.label || it.path}</span>
            </button>
          ))}
        </div>
      )}
      <div className="flex flex-col gap-1">
        {rest.map((it) => (
          <Row key={it.id} it={it} host={host} onBrowse={onBrowse} onRetire={() => retire(it)} />
        ))}
        {showOld && old.map((it) => <Row key={it.id} it={it} host={host} onBrowse={onBrowse} />)}
      </div>
    </div>
  );
}

function Row({ it, host, onBrowse, onRetire }: { it: ArtifactItem; host?: string; onBrowse: (path: string) => void; onRetire?: () => void }) {
  const [copied, setCopied] = useState(false);
  const Icon = it.kind === "link" ? Link2 : it.kind === "path" ? FolderOpen : it.kind === "file" ? FileText : Type;
  const value = it.url ?? it.path ?? it.text ?? "";
  const copy = () => {
    navigator.clipboard.writeText(value);
    setCopied(true);
    setTimeout(() => setCopied(false), 1200);
  };
  const open = () => {
    if (it.kind === "link") {
      if (isNative()) nativeOpenURL(it.url!);
      else window.open(it.url, "_blank", "noopener");
    } else if (it.kind === "path") onBrowse(it.path!);
  };
  return (
    <div className={cn("group flex items-start gap-2 rounded-md px-1.5 py-1 text-[12px] hover:bg-accent/60", it.superseded && "opacity-50")}>
      <Icon className="mt-0.5 size-3.5 shrink-0 text-muted-foreground" />
      <div className="min-w-0 flex-1">
        {it.kind === "text" ? (
          <>
            {it.label && <div className="text-[11px] text-muted-foreground">{it.label}</div>}
            <div className={cn("break-words whitespace-pre-wrap", it.superseded && "line-through")}>{it.text}</div>
          </>
        ) : (
          <button type="button" onClick={open} className="flex w-full min-w-0 items-baseline gap-2 text-left">
            {it.label && <span className={cn("shrink-0 font-medium", it.superseded && "line-through")}>{it.label}</span>}
            <span className="min-w-0 truncate font-mono text-[11px] text-muted-foreground" title={value}>
              {it.kind === "link" ? shortURL(value) : value.replace(/^\/(?:Users|home)\/[^/]+/, "~")}
            </span>
          </button>
        )}
      </div>
      <span className="shrink-0 text-[11px] text-muted-foreground tabular-nums">{when(it.at)}</span>
      <div className="flex shrink-0 items-center opacity-0 group-hover:opacity-100 [@media(hover:none)]:opacity-100">
        <button type="button" title={t("folder.copy")} onClick={copy} className="flex size-5 items-center justify-center rounded-sm hover:bg-accent">
          {copied ? <Check className="size-3 text-ok" /> : <Copy className="size-3" />}
        </button>
        {(it.kind === "link" || (it.kind === "path" && isNative() && !host) || onRetire) && (
          <DropdownMenu>
            <DropdownMenuTrigger asChild>
              <button type="button" className="flex size-5 items-center justify-center rounded-sm hover:bg-accent">
                <MoreHorizontal className="size-3" />
              </button>
            </DropdownMenuTrigger>
            <DropdownMenuContent align="end" onCloseAutoFocus={(e) => e.preventDefault()}>
              {it.kind === "link" && (
                <DropdownMenuItem onClick={open}>
                  <ExternalLink /> {it.url && shortURL(it.url)}
                </DropdownMenuItem>
              )}
              {it.kind === "path" && (
                <DropdownMenuItem onClick={() => onBrowse(it.path!)}>
                  <FolderOpen /> {t("folder.browse")}
                </DropdownMenuItem>
              )}
              {it.kind === "path" && isNative() && !host && (
                <DropdownMenuItem onClick={() => nativeOpenPath(it.path!, undefined, true)}>
                  <FolderOpen /> {IS_MAC ? t("folder.reveal") : t("folder.revealOther")}
                </DropdownMenuItem>
              )}
              {onRetire && (
                <DropdownMenuItem onClick={onRetire}>
                  <X /> {t("folder.retire")}
                </DropdownMenuItem>
              )}
            </DropdownMenuContent>
          </DropdownMenu>
        )}
      </div>
    </div>
  );
}

function AddRecord({ by, host, onDone }: { by: { session: string } | { dir: string }; host?: string; onDone: () => void }) {
  const [kind, setKind] = useState<"link" | "path" | "text">("link");
  const [value, setValue] = useState("");
  const [label, setLabel] = useState("");
  const [busy, setBusy] = useState(false);
  const save = () => {
    if (!value.trim() || busy) return;
    setBusy(true);
    const v = value.trim();
    api
      .recordArtifact(by, { kind, label: label.trim() || undefined, ...(kind === "link" ? { url: v } : kind === "path" ? { path: v } : { text: v }) }, host)
      .then(onDone)
      .catch((e) => toast.error(e.message))
      .finally(() => setBusy(false));
  };
  return (
    <div className="mb-2 flex flex-col gap-1.5 rounded-md border bg-muted/30 p-2">
      <div className="flex gap-1">
        {(["link", "path", "text"] as const).map((k) => (
          <button
            key={k}
            type="button"
            onClick={() => setKind(k)}
            className={cn("rounded-sm px-2 py-0.5 text-[12px] text-muted-foreground hover:bg-accent", kind === k && "bg-accent text-foreground")}
          >
            {t(`folder.kind.${k}`)}
          </button>
        ))}
      </div>
      <input
        autoFocus
        value={value}
        onChange={(e) => setValue(e.target.value)}
        onKeyDown={(e) => e.key === "Enter" && save()}
        placeholder={t(`folder.valuePh.${kind}`)}
        className="h-7 rounded-sm border bg-background px-2 text-[12px] outline-none focus:border-brand"
      />
      <input
        value={label}
        onChange={(e) => setLabel(e.target.value)}
        onKeyDown={(e) => e.key === "Enter" && save()}
        placeholder={t("folder.labelPh")}
        className="h-7 rounded-sm border bg-background px-2 text-[12px] outline-none focus:border-brand"
      />
      <div className="flex justify-end gap-1">
        <Button variant="ghost" size="sm" className="h-6 px-2 text-[12px]" onClick={onDone}>
          {t("folder.cancel")}
        </Button>
        <Button size="sm" className="h-6 px-2 text-[12px]" disabled={!value.trim() || busy} onClick={save}>
          {t("folder.save")}
        </Button>
      </div>
    </div>
  );
}

function shortURL(u: string) {
  try {
    const x = new URL(u);
    return x.host + x.pathname.replace(/\/$/, "");
  } catch {
    return u;
  }
}

function when(at: number) {
  const d = new Date(at);
  const now = new Date();
  const p = (n: number) => String(n).padStart(2, "0");
  return d.toDateString() === now.toDateString() ? `${p(d.getHours())}:${p(d.getMinutes())}` : `${d.getMonth() + 1}/${d.getDate()}`;
}
