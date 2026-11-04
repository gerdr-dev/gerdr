import { Markdown } from "@gerdr/chat";
import { openLightbox } from "@/components/Lightbox";
import { Check, ChevronRight, CircleAlert, ExternalLink, Eye, EyeOff, FolderOpen, Puzzle, RefreshCw, Trash2, X } from "lucide-react";
import { useEffect, useState } from "react";
import { toast } from "sonner";
import { Button } from "@/components/ui/button";
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from "@/components/ui/collapsible";
import { Input } from "@/components/ui/input";
import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Switch } from "@/components/ui/switch";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { Textarea } from "@/components/ui/textarea";
import { addonStatus, assetURL, type AddonStatus } from "@/lib/addons";
import { api, ApiError } from "@/lib/api";
import { askConfirm } from "@/lib/confirm";
import { isNative, nativeOpenPath } from "@/lib/native";
import { useStore } from "@/lib/store";
import type { AddonService, AddonSetting, AddonStore, StoreAddon } from "@/lib/types";
import { t } from "@/lib/i18n";
import { cn } from "@/lib/utils";

// Settings → Add-ons: what is installed (switch on / off, update, uninstall) and the stores to install from
// (internal/addons; the API is internal/server/addonapi.go).
export function AddonSettings({ open }: { open: boolean }) {
  const [tab, setTab] = useState("installed");
  const stores = useStores(open);
  return (
    <Tabs value={tab} onValueChange={setTab} className="gap-3">
      <TabsList>
        <TabsTrigger value="installed">{t("addonSettings.tab.installed")}</TabsTrigger>
        <TabsTrigger value="store">{t("addonSettings.tab.store")}</TabsTrigger>
      </TabsList>
      <TabsContent value="installed">
        <Installed stores={stores} />
      </TabsContent>
      <TabsContent value="store">
        <Stores stores={stores} />
      </TabsContent>
    </Tabs>
  );
}

// The stores, fetched while Settings is open and again on every "addon_stores" push. A store never downloaded is
// downloaded once by itself (an empty default store would look broken); after that, only the refresh button does.
function useStores(open: boolean) {
  const [stores, setStores] = useState<AddonStore[]>([]);
  const v = useStore((s) => s.addonStoresVersion);
  useEffect(() => {
    if (!open) return;
    let live = true;
    api
      .addonStores()
      .then((list) => {
        if (!live) return;
        setStores(list);
        for (const s of list) if (!s.local && !s.fetchedAt && !s.error && !fetching.has(s.source)) void refresh(s.source, true);
      })
      .catch(() => {});
    return () => {
      live = false;
    };
  }, [open, v]);
  return stores;
}

const fetching = new Set<string>();

async function refresh(source: string, quiet = false) {
  fetching.add(source);
  try {
    await api.refreshAddonStore(source);
  } catch (e) {
    if (!quiet) toast.error(t("addonSettings.refreshFailed", { source, error: (e as Error).message }));
  } finally {
    fetching.delete(source);
  }
}

/** Install or update; a folder that isn't the store's own copy (made by hand, or edited since) is overwritten only when asked. */
async function install(source: string, a: StoreAddon, force = false): Promise<void> {
  try {
    await api.installAddon(source, a.id, force);
    toast.success(t("addonSettings.installedToast", { name: a.name }));
  } catch (e) {
    if (e instanceof ApiError && e.code === "conflict" && !force) {
      const ok = await askConfirm({
        title: t("addonSettings.overwrite.title", { id: a.id }),
        description: t("addonSettings.overwrite.desc", { id: a.id }),
        action: t("addonSettings.overwrite.action"),
        destructive: true,
      });
      if (ok) return install(source, a, true);
      return;
    }
    toast.error(t("addonSettings.installFailed", { name: a.name, error: (e as Error).message }));
  }
}

function storeAddon(stores: AddonStore[], a: AddonStatus) {
  if (!a.source) return undefined;
  return stores.find((s) => s.source === a.source!.store)?.addons.find((x) => x.id === a.id);
}

function Installed({ stores }: { stores: AddonStore[] }) {
  const dir = useStore((s) => s.addonsDir);
  useStore((s) => s.addonVersion);
  return (
    <div className="space-y-3">
      <div className="flex items-start justify-between gap-4">
        <div className="text-[13px]">
          {rich(t("addonSettings.where"), {
            dir: <code className="rounded-sm bg-muted px-1 font-mono text-[12px]">{dir}</code>,
            dts: <code className="font-mono text-[12px]">gerdr.d.ts</code>,
          })}
        </div>
        {/* Only the App can open a folder on this machine; a browser (maybe a remote one) can't, so no button there */}
        {isNative() && (
          <Button size="sm" variant="outline" className="shrink-0" disabled={!dir} title={dir} onClick={() => nativeOpenPath(dir, undefined, false)}>
            <FolderOpen className="size-3.5" /> {t("addonSettings.openFolder")}
          </Button>
        )}
      </div>
      <div className="divide-y rounded-md border">
        {addonStatus.length === 0 && <div className="p-3 text-[13px] text-muted-foreground">{t("addonSettings.empty")}</div>}
        {addonStatus.map((a) => (
          <InstalledRow key={a.id} a={a} newer={newer(a, storeAddon(stores, a))} />
        ))}
      </div>
    </div>
  );
}

/** The store's copy when it differs from what was installed from it. */
function newer(a: AddonStatus, s?: StoreAddon) {
  return s && a.source && s.hash !== a.source.hash ? s : undefined;
}

function InstalledRow({ a, newer }: { a: AddonStatus; newer?: StoreAddon }) {
  const [busy, setBusy] = useState(false);
  const run = async (f: () => Promise<unknown>) => {
    setBusy(true);
    try {
      await f();
    } catch (e) {
      toast.error((e as Error).message);
    } finally {
      setBusy(false);
    }
  };
  const uninstall = async () => {
    const ok = await askConfirm({
      title: t("addonSettings.uninstall.title", { name: a.name }),
      description: t(a.source ? "addonSettings.uninstall.desc" : "addonSettings.uninstall.descLocal", { dir: a.dir }),
      action: t("common.uninstall"),
      destructive: true,
    });
    if (ok) await run(() => api.uninstallAddon(a.id));
  };
  return (
    // Same shape as a store row (StoreRow): the controls sit on the name's line, the rest goes under it, full width on a phone
    <div className={rowGrid}>
      <div className="md:row-span-2">
        <AddonIcon src={a.icon ? assetURL(a.id, a.icon, a.hash) : undefined} />
      </div>
      <div className={rowTitle}>
        <span className="font-medium">{a.name}</span>
        {a.version && <span className="text-[11px] text-muted-foreground">v{a.version}</span>}
        {a.name !== a.id && <span className="font-mono text-[11px] text-muted-foreground">{a.id}</span>}
        {a.source ? <Tag>{a.source.store}</Tag> : <Tag>{t("addonSettings.tag.local")}</Tag>}
        {a.modified && <Tag className="text-warn">{t("addonSettings.tag.modified")}</Tag>}
      </div>
      <div className="flex min-h-9 shrink-0 items-center gap-1.5 md:-my-1 md:min-h-0">
        {newer && (
          <Button size="sm" variant="outline" disabled={busy} onClick={() => run(() => install(a.source!.store, newer))}>
            {newer.version && newer.version !== a.version ? t("addonSettings.updateTo", { version: newer.version }) : t("common.update")}
          </Button>
        )}
        <Button size="icon-sm" variant="ghost" disabled={busy} title={t("common.uninstall")} onClick={uninstall}>
          <Trash2 className="size-3.5" />
        </Button>
        <Switch checked={!a.disabled} disabled={busy} onCheckedChange={(on) => run(() => api.setAddonEnabled(a.id, on))} />
      </div>
      <div className={rowBody}>
        {a.description && <div className="mt-1 text-[12px] text-muted-foreground">{a.description}</div>}
        {/* No "loaded" / "off" line: the switch already says that. Only a failure gets a line (its text is below) */}
        {a.error && !a.disabled && (
          <div className="mt-1 flex items-center gap-1.5 text-[12px] text-bad">
            <CircleAlert className="size-3.5 shrink-0" /> {t("addonSettings.loadError")}
          </div>
        )}
        {a.service && <ServiceLine s={a.service} />}
        {a.error && <pre className="mt-1.5 whitespace-pre-wrap rounded-sm bg-bad/10 p-2 font-mono text-[11px] text-bad">{a.error}</pre>}
        {!!a.settings?.length && (
          <Collapsible className="mt-1">
            <CollapsibleTrigger className="group flex items-center gap-1.5 text-[12px] text-muted-foreground hover:text-foreground">
              <ChevronRight className="size-3.5 shrink-0 transition-transform group-data-[state=open]:rotate-90" /> {t("addonSettings.settings")}
            </CollapsibleTrigger>
            <CollapsibleContent className="mt-1.5 space-y-2.5 rounded-sm bg-muted/50 p-2.5">
              {a.settings.map((s) =>
                s.type === "link" ? (
                  <LinkSetting key={s.key} spec={s} url={(a.service?.state === "running" ? a.service.links?.[s.key] : undefined) || s.url} />
                ) : (
                  <SettingField key={s.key} id={a.id} spec={s} value={a.settingValues?.[s.key]} />
                ),
              )}
            </CollapsibleContent>
          </Collapsible>
        )}
      </div>
    </div>
  );
}

// A "link" setting: its title and a button opening the page. The address comes from addon.json, or from the running
// service (a port it picked); without one yet (the service is starting or stopped) the button waits, disabled.
function LinkSetting({ spec, url }: { spec: AddonSetting; url?: string }) {
  const label = (
    <>
      <ExternalLink className="size-3.5" />
      {spec.label || t("addonSettings.open")}
    </>
  );
  return (
    <div className="text-[12px]">
      <div className="flex items-center gap-2">
        <span className="min-w-0 flex-1">{spec.title || spec.key}</span>
        {url ? (
          <Button size="sm" variant="outline" className="h-7 text-[12px]" asChild>
            <a href={url} target="_blank" rel="noreferrer" title={url}>
              {label}
            </a>
          </Button>
        ) : (
          <Button size="sm" variant="outline" className="h-7 text-[12px]" disabled title={t("addonSettings.linkPending")}>
            {label}
          </Button>
        )}
      </div>
      {spec.description && <div className="mt-0.5 text-[11px] text-muted-foreground">{spec.description}</div>}
    </div>
  );
}

// One entry of an add-on's addon.json "settings". Saved on change (a number on blur / Enter); stored by gerdr, so
// every window and device sees the same value.
function SettingField({ id, spec, value }: { id: string; spec: AddonSetting; value: unknown }) {
  const shown = value !== undefined ? value : spec.default;
  const [draft, setDraft] = useState(String(shown ?? ""));
  useEffect(() => setDraft(String(shown ?? "")), [shown]);
  const [reveal, setReveal] = useState(false); // a "secret" shown in the clear
  const save = (v: unknown) => api.setAddonSettings(id, { [spec.key]: v }).catch((e) => toast.error((e as Error).message));
  const commitNumber = () => {
    const n = Number(draft);
    if (draft.trim() === "" || Number.isNaN(n)) return setDraft(String(shown ?? ""));
    if (n !== shown) save(n);
  };
  // On a phone the label goes above its field: side by side, a long label wrapped a character per line next to the
  // input. A switch stays on the label's line, it's small enough.
  const stacked = spec.type !== "boolean";
  // A "text" (several lines: a list, a table) gets the whole width under its label, on every screen
  const below = spec.type === "text";
  return (
    <div className="text-[12px]">
      <div
        className={cn(
          "flex gap-2",
          below ? "flex-col items-stretch gap-1" : stacked ? "max-md:flex-col max-md:items-stretch max-md:gap-1 md:items-center" : "items-center",
        )}
      >
        <span className={cn("min-w-0", stacked ? "md:flex-1" : "flex-1")}>{spec.title || spec.key}</span>
        {spec.type === "text" ? (
          <Textarea
            className="min-h-20 font-mono text-[12px]"
            spellCheck={false}
            value={draft}
            onChange={(e) => setDraft(e.target.value)}
            onBlur={() => draft !== shown && save(draft)}
          />
        ) : spec.type === "boolean" ? (
          <Switch checked={!!shown} onCheckedChange={(v) => save(v)} />
        ) : spec.type === "select" ? (
          // Values can be numbers or strings: Radix wants strings, so they travel as JSON. Options may be worked out
          // from what is installed (optionsFrom): none yet shows as a disabled "nothing to choose", its description
          // says what to install
          <Select value={shown == null ? undefined : JSON.stringify(shown)} onValueChange={(v) => save(JSON.parse(v))} disabled={!spec.options?.length}>
            <SelectTrigger size="sm" className="h-7 text-[12px] max-md:w-full">
              <SelectValue placeholder={spec.options?.length ? undefined : t("addonSettings.noOptions")} />
            </SelectTrigger>
            <SelectContent>
              {(spec.options ?? []).map((o) => (
                <SelectItem key={JSON.stringify(o.value)} value={JSON.stringify(o.value)} className="text-[12px]">
                  {o.label ?? String(o.value)}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        ) : spec.type === "color" ? (
          <ColorField value={String(shown ?? "")} onSave={save} />
        ) : (
          <span className="flex items-center gap-1">
            <Input
              className={cn("h-7 text-[12px]", spec.type === "number" ? "w-20 text-right" : "w-48 max-md:w-auto max-md:flex-1")}
              type={spec.type === "number" ? "number" : spec.type === "secret" && !reveal ? "password" : "text"}
              // Keep the browser's password manager away from add-on tokens
              autoComplete={spec.type === "secret" ? "off" : undefined}
              spellCheck={spec.type === "secret" ? false : undefined}
              min={spec.min}
              max={spec.max}
              value={draft}
              onChange={(e) => setDraft(e.target.value)}
              onBlur={() => (spec.type === "number" ? commitNumber() : draft !== shown && save(draft))}
              onKeyDown={(e) => e.key === "Enter" && (e.target as HTMLInputElement).blur()}
            />
            {spec.type === "secret" && (
              <Button
                size="icon-sm"
                variant="ghost"
                title={reveal ? t("addonSettings.hide") : t("addonSettings.show")}
                aria-label={reveal ? t("addonSettings.hide") : t("addonSettings.show")}
                onClick={() => setReveal(!reveal)}
              >
                {reveal ? <EyeOff className="size-3.5" /> : <Eye className="size-3.5" />}
              </Button>
            )}
            {spec.unit && <span className="text-muted-foreground">{spec.unit}</span>}
          </span>
        )}
      </div>
      {(spec.description || value !== undefined) && (
        <div className="mt-0.5 flex gap-2 text-[11px] text-muted-foreground">
          <span className="min-w-0 flex-1">{spec.description}</span>
          {value !== undefined && (
            <button type="button" className="shrink-0 hover:text-foreground" onClick={() => save(null)}>
              {/* A secret's default is not echoed either (usually empty anyway) */}
              {spec.type === "secret"
                ? t("addonSettings.clear")
                : spec.type === "text"
                  ? t("addonSettings.resetDefault")
                  : t("addonSettings.resetDefaultTo", { value: String(spec.default) })}
            </button>
          )}
        </div>
      )}
    </div>
  );
}

// A "color" setting: a swatch opening a palette plus the hex, editable (any color the palette lacks).
// prettier-ignore
const SWATCHES = [
  "#2f6fd0", "#1f9ed1", "#14a39a", "#2f9e5b", "#7aa02c", "#d9a21b",
  "#e8772e", "#dc4b3e", "#e5569b", "#c04fc8", "#8a5cf0", "#5b6472",
];

function ColorField({ value, onSave }: { value: string; onSave: (v: string) => void }) {
  const [open, setOpen] = useState(false);
  const [draft, setDraft] = useState(value);
  useEffect(() => setDraft(value), [value, open]);
  const valid = (v: string) => /^#[0-9a-f]{6}$/i.test(v);
  const pick = (v: string) => {
    v = v.trim().toLowerCase();
    if (!valid(v)) return setDraft(value);
    if (v !== value.toLowerCase()) onSave(v);
  };
  return (
    <Popover open={open} onOpenChange={setOpen}>
      <PopoverTrigger asChild>
        <button type="button" className="flex h-7 items-center gap-1.5 rounded-md border px-1.5 font-mono text-[12px] hover:bg-accent">
          <span className="size-4 rounded-sm border" style={{ background: valid(value) ? value : undefined }} />
          {value}
        </button>
      </PopoverTrigger>
      <PopoverContent align="end" className="w-auto p-2">
        <div className="grid grid-cols-6 gap-1.5">
          {SWATCHES.map((c) => (
            <button
              key={c}
              type="button"
              title={c}
              onClick={() => (pick(c), setOpen(false))}
              className={cn(
                "flex size-6 items-center justify-center rounded-sm transition-transform hover:scale-110",
                c === value.toLowerCase() && "ring-2 ring-foreground/40 ring-offset-1 ring-offset-popover",
              )}
              style={{ background: c }}
            >
              {c === value.toLowerCase() && <Check className="size-3.5 text-white" />}
            </button>
          ))}
        </div>
        <div className="mt-2 flex items-center gap-1.5">
          <span className="size-6 shrink-0 rounded-sm border" style={{ background: valid(draft) ? draft : undefined }} />
          <Input
            className="h-7 flex-1 font-mono text-[12px]"
            value={draft}
            placeholder="#rrggbb"
            onChange={(e) => setDraft(e.target.value)}
            onBlur={() => pick(draft)}
            onKeyDown={(e) => e.key === "Enter" && (pick(draft), setOpen(false))}
          />
        </div>
      </PopoverContent>
    </Popover>
  );
}

function Stores({ stores }: { stores: AddonStore[] }) {
  const [source, setSource] = useState("");
  const [adding, setAdding] = useState(false);
  const add = async () => {
    if (!source.trim()) return;
    setAdding(true);
    try {
      await api.addAddonStore(source.trim());
      setSource("");
    } catch (e) {
      toast.error(t("addonSettings.addStoreFailed", { error: (e as Error).message }));
    } finally {
      setAdding(false);
    }
  };
  return (
    <div className="space-y-4">
      {stores.length === 0 && <div className="text-[13px] text-muted-foreground">{t("addonSettings.noStores")}</div>}
      {stores.map((s) => (
        <StoreSection key={s.source} store={s} />
      ))}
      <div className="space-y-1.5">
        <div className="flex gap-2">
          <Input
            value={source}
            placeholder={t("addonSettings.storePlaceholder")}
            className="h-8 text-[13px]"
            onChange={(e) => setSource(e.target.value)}
            onKeyDown={(e) => e.key === "Enter" && add()}
          />
          <Button size="sm" disabled={adding || !source.trim()} onClick={add}>
            {t("addonSettings.addStore")}
          </Button>
        </div>
        <div className="text-[12px] text-muted-foreground">{rich(t("addonSettings.storeHint"), { file: <code className="font-mono">addon.json</code> })}</div>
      </div>
    </div>
  );
}

function StoreSection({ store }: { store: AddonStore }) {
  const [busy, setBusy] = useState(false);
  useStore((s) => s.addonVersion);
  const doRefresh = async () => {
    setBusy(true);
    await refresh(store.source);
    setBusy(false);
  };
  const remove = async () => {
    const ok = await askConfirm({
      title: t("addonSettings.removeStore.title", { source: store.source }),
      description: t("addonSettings.removeStore.desc"),
      action: t("common.remove"),
      destructive: true,
    });
    if (ok) api.removeAddonStore(store.source).catch((e) => toast.error((e as Error).message));
  };
  const url = store.local ? undefined : `https://github.com/${store.source.replace(/#.*/, "")}`;
  return (
    <div className="space-y-2">
      {/* On a phone the refresh time takes a line of its own (order-last + basis-full): next to the name it squeezed the
          name down to "ge…" and the tag to one character a line */}
      <div className="flex flex-wrap items-center gap-x-2 text-[13px]">
        <span className="min-w-0 truncate font-medium">{store.source}</span>
        {url && (
          <a href={url} target="_blank" rel="noreferrer" className="shrink-0 text-muted-foreground hover:text-foreground" title={url}>
            <ExternalLink className="size-3.5" />
          </a>
        )}
        {store.default && <Tag>{t("common.default")}</Tag>}
        {store.local && <Tag>{t("addonSettings.tag.localFolder")}</Tag>}
        <span className="ml-auto shrink-0 text-[11px] text-muted-foreground max-md:order-last max-md:ml-0 max-md:basis-full">
          {busy || fetching.has(store.source)
            ? t("addonSettings.downloading")
            : store.local
              ? ""
              : store.fetchedAt
                ? t("addonSettings.refreshedAt", { time: new Date(store.fetchedAt).toLocaleString() })
                : t("addonSettings.notDownloaded")}
        </span>
        <Button size="icon-sm" variant="ghost" title={t("common.refresh")} className="max-md:ml-auto" disabled={busy} onClick={doRefresh}>
          <RefreshCw className={cn("size-3.5", busy && "animate-spin")} />
        </Button>
        <Button size="icon-sm" variant="ghost" title={t("addonSettings.removeStore")} onClick={remove}>
          <X className="size-3.5" />
        </Button>
      </div>
      {store.error && <div className="rounded-sm bg-bad/10 p-2 text-[12px] text-bad">{store.error}</div>}
      <div className="divide-y rounded-md border">
        {store.addons.length === 0 && (
          <div className="p-3 text-[13px] text-muted-foreground">{store.fetchedAt ? t("addonSettings.storeEmpty") : t("addonSettings.storeNotFetched")}</div>
        )}
        {store.addons.map((a) => (
          <StoreRow key={a.id} source={store.source} a={a} />
        ))}
      </div>
    </div>
  );
}

function StoreRow({ source, a }: { source: string; a: StoreAddon }) {
  const [busy, setBusy] = useState(false);
  const mine = addonStatus.find((x) => x.id === a.id);
  const fromHere = mine?.source?.store === source;
  const action = !mine ? "install" : !fromHere ? "replace" : mine.source!.hash !== a.hash ? "update" : "";
  const label = { install: t("common.install"), replace: t("addonSettings.replaceLocal"), update: t("common.update"), "": "" }[action];
  const go = async () => {
    setBusy(true);
    await install(source, a, false);
    setBusy(false);
  };
  const file = (path: string) =>
    `${location.origin}/api/addon-stores/file?source=${encodeURIComponent(source)}&id=${encodeURIComponent(a.id)}&path=${encodeURIComponent(path)}&v=${a.hash}`;
  return (
    // The button sits on the name's line and the rest goes under it: as a column of its own it left the description
    // and screenshots a third of a phone's width. The body lines up with the name, and takes the full width on a phone (rowGrid)
    <div className={rowGrid}>
      <div className="md:row-span-2">
        <AddonIcon src={a.icon ? file(a.icon) : undefined} />
      </div>
      <div className={rowTitle}>
        <span className="font-medium">{a.name}</span>
        {a.version && <span className="text-[11px] text-muted-foreground">v{a.version}</span>}
        {a.name !== a.id && <span className="font-mono text-[11px] text-muted-foreground">{a.id}</span>}
        {a.hasService && <Tag>{t("addonSettings.tag.service")}</Tag>}
      </div>
      <div className="flex min-h-9 shrink-0 items-center md:-my-1 md:min-h-0">
        {label ? (
          <Button size="sm" variant={action === "install" ? "default" : "outline"} disabled={busy} onClick={go}>
            {busy ? t("addonSettings.installing") : label}
          </Button>
        ) : (
          <span className="flex items-center gap-1 text-[12px] text-muted-foreground">
            <Check className="size-3.5 text-ok" /> {t("addonSettings.tab.installed")}
          </span>
        )}
      </div>
      <div className={rowBody}>
        {a.description && <div className="mt-1 text-[12px] text-muted-foreground">{a.description}</div>}
        {!!a.screenshots?.length && (
          <div className="mt-2 flex gap-2 overflow-x-auto pb-1">
            {a.screenshots.map((p, i) => (
              <button
                key={p}
                type="button"
                className="shrink-0 cursor-zoom-in"
                onClick={(e) =>
                  openLightbox(
                    a.screenshots!.map((s) => ({ url: file(s), alt: a.name })),
                    i,
                    e.currentTarget.firstElementChild,
                  )
                }
              >
                <img src={file(p)} alt="" className="h-28 rounded-md border object-cover hover:opacity-90" />
              </button>
            ))}
          </div>
        )}
        {a.readme && (
          <Collapsible className="mt-1">
            <CollapsibleTrigger className="group flex items-center gap-1.5 text-[12px] text-muted-foreground hover:text-foreground">
              <ChevronRight className="size-3.5 shrink-0 transition-transform group-data-[state=open]:rotate-90" /> {t("addonSettings.readme")}
            </CollapsibleTrigger>
            <CollapsibleContent
              // A README is a document, not a reply: headings at chat sizes dwarfed the row (its title repeats the
              // add-on's name anyway and is dropped). Its images open in the lightbox, not as a page of their own.
              className="mt-1.5 rounded-sm bg-muted/50 p-2.5 text-[13px] [&_h1]:!text-[15px] [&_h2]:!text-[14px] [&_h3]:!text-[13px] [&_:is(h1,h2,h3)]:!mt-3 [&_:is(h1,h2,h3)]:!mb-1.5 [&_:is(h1,h2,h3)]:!font-semibold [&_img]:max-h-64 [&_img]:w-auto [&_img]:cursor-zoom-in [&_img]:rounded-md [&_img]:border"
              onClickCapture={zoomImage}
            >
              <Markdown>{relinkImages(dropTitle(a.readme), file)}</Markdown>
            </CollapsibleContent>
          </Collapsible>
        )}
      </div>
    </div>
  );
}

// The README's own "# Name" line: the row already shows the name.
const dropTitle = (md: string) => md.replace(/^\s*#\s[^\n]*\n+/, "");

// A click on an image in the README: the lightbox over every image in it, starting at that one.
function zoomImage(e: React.MouseEvent<HTMLElement>) {
  const img = (e.target as Element).closest("img");
  if (!img) return;
  e.preventDefault();
  e.stopPropagation();
  const all = Array.from(e.currentTarget.querySelectorAll("img"));
  openLightbox(
    all.map((x) => ({ url: x.currentSrc || x.src, alt: x.alt })),
    all.indexOf(img),
    img,
  );
}

// A README's images point at files next to it (![](shot.png)): serve them from the store's copy.
function relinkImages(md: string, file: (path: string) => string) {
  return md.replace(/(!\[[^\]]*\]\()(?!https?:|data:|\/)\.?\/?([^)\s]+)/g, (_, pre: string, path: string) => pre + file(path));
}

// Layout shared by an installed row and a store row. On a wide screen the icon spans both rows, so the name sits at
// the icon's top edge with the description right under it (centring the name on the icon pushed the description
// below the icon and left a gap). The name line has no min height there, so its text starts at the icon's top; the
// controls (taller than that line) overhang it evenly with -my-1 to stay centred on it. On a phone the icon stays on the name's line and the body takes the full width.
const rowGrid = "grid grid-cols-[auto_minmax(0,1fr)_auto] items-start gap-x-3 p-3";
const rowTitle = "flex min-h-9 min-w-0 flex-wrap items-center gap-x-2 gap-y-0.5 text-[13px] leading-5 md:min-h-0";
const rowBody = "col-span-3 min-w-0 md:col-span-2 md:col-start-2";

function AddonIcon({ src }: { src?: string }) {
  const [broken, setBroken] = useState(false);
  return (
    <div className="flex size-9 shrink-0 items-center justify-center overflow-hidden rounded-md bg-muted">
      {src && !broken ? (
        <img src={src} alt="" className="size-full object-cover" onError={() => setBroken(true)} />
      ) : (
        <Puzzle className="size-4 text-muted-foreground" />
      )}
    </div>
  );
}

function Tag({ children, className }: { children: React.ReactNode; className?: string }) {
  return <span className={cn("shrink-0 whitespace-nowrap rounded-sm bg-muted px-1.5 py-px text-[11px] text-muted-foreground", className)}>{children}</span>;
}

// An add-on's backend process (addon.json "service"): running or not, its own status line, and why it died.
function ServiceLine({ s }: { s: AddonService }) {
  const label = t(`addonSettings.service.${s.state}`);
  return (
    <div className="mt-1.5 space-y-1 text-[12px]">
      <div className="flex items-center gap-1.5">
        <span className="flex size-3.5 shrink-0 items-center justify-center">
          <span className={cn("size-1.5 rounded-full", s.state === "running" ? "bg-ok" : s.state === "exited" ? "bg-bad" : "bg-warn")} />
        </span>
        <span className="text-muted-foreground">
          {label}
          {s.pid ? t("addonSettings.service.pid", { pid: s.pid }) : ""}
          {s.restarts ? t(s.restarts === 1 ? "addonSettings.service.restartsOne" : "addonSettings.service.restarts", { n: s.restarts }) : ""}
        </span>
        {s.text && <span className="truncate">{s.text}</span>}
      </div>
      {s.error && <pre className="whitespace-pre-wrap rounded-sm bg-bad/10 p-2 font-mono text-[11px] text-bad">{s.error}</pre>}
    </div>
  );
}

// A message with {name} placeholders filled by elements (a <code> in the middle of a sentence).
function rich(text: string, parts: Record<string, React.ReactNode>): React.ReactNode[] {
  return text.split(/\{(\w+)\}/).map((p, i) => (i % 2 ? <span key={i}>{parts[p]}</span> : p));
}
