import { useEffect, useState } from "react";
import { ExternalLink, Globe } from "lucide-react";
import { Button } from "@/components/ui/button";
import { api, type BrowserProfile } from "@/lib/api";
import { t } from "@/lib/i18n";

// Settings → Browser: the two browsers agents can use (`gerdr browser`). The user's own Chrome goes through the Gerdr
// browser extension (docs/dev/CHROME-EXT.md): how to get it, and which Chromes are connected with the sites each
// allowed, so whether it works is visible where it is installed from. Read every 2 s while the page shows: the
// bridge has no push for extensions coming and going, and a poll of an in-memory list costs nothing.

// The Chrome Web Store build (its id is in chromeext.ExtensionIDs); the store finds the item from the id alone
const STORE_URL = "https://chromewebstore.google.com/detail/hkooiloghnflaffpkfapfodomgkdpnff";

export function BrowserSettings({ open }: { open: boolean }) {
  const [profiles, setProfiles] = useState<BrowserProfile[] | null>(null);
  const [err, setErr] = useState("");
  useEffect(() => {
    if (!open) return;
    let stop = false;
    const tick = () =>
      api
        .browserTabs()
        .then((r) => {
          if (stop) return;
          setProfiles(r.profiles ?? []);
          setErr("");
        })
        .catch((e: Error) => !stop && setErr(e.message));
    void tick();
    const timer = setInterval(tick, 2000);
    return () => {
      stop = true;
      clearInterval(timer);
    };
  }, [open]);

  return (
    <div className="min-w-0 space-y-6">
      <section className="space-y-3">
        <div className="border-b pb-1.5 text-[12px] font-medium text-muted-foreground">{t("browserSettings.chrome")}</div>
        <div className="text-[12px] text-muted-foreground">{t("browserSettings.chromeHint")}</div>
        {err && <div className="text-[12px] text-bad">{t("browserSettings.loadFailed", { error: err })}</div>}
        {profiles?.length ? (
          <div className="space-y-2">
            {profiles.map((p) => (
              <ProfileCard key={p.id} p={p} />
            ))}
          </div>
        ) : (
          profiles && (
            <div className="space-y-3 rounded-lg border p-3">
              <div className="flex items-center gap-2">
                <span className="size-2 shrink-0 rounded-full bg-muted-foreground/40" />
                <span className="font-medium">{t("browserSettings.none")}</span>
              </div>
              <Button asChild size="sm">
                <a href={STORE_URL} target="_blank" rel="noreferrer">
                  <ExternalLink /> {t("browserSettings.install")}
                </a>
              </Button>
              <div className="text-[12px] text-muted-foreground">{t("browserSettings.installHint")}</div>
            </div>
          )
        )}
        {/* Already connected: the store page stays one click away, for another profile or another computer */}
        {!!profiles?.length && (
          <a
            href={STORE_URL}
            target="_blank"
            rel="noreferrer"
            className="inline-flex items-center gap-1 text-[12px] text-muted-foreground hover:text-foreground"
          >
            <ExternalLink className="size-3" /> {t("browserSettings.install")}
          </a>
        )}
      </section>

      <section className="space-y-3">
        <div className="border-b pb-1.5 text-[12px] font-medium text-muted-foreground">{t("browserSettings.project")}</div>
        <div className="text-[12px] text-muted-foreground">{t("browserSettings.projectHint")}</div>
      </section>

      {/* How both get used, in one place, after what the two are: people saw where to install the extension but not how it gets used
          (reported), and split per browser the two halves read as unrelated */}
      <section className="space-y-2 text-[12px]">
        <div className="border-b pb-1.5 font-medium text-muted-foreground">{t("browserSettings.howTo")}</div>
        <div className="text-muted-foreground">{t("browserSettings.projectUse")}</div>
        <Example text={t("browserSettings.projectEx")} />
        <div className="pt-1 text-muted-foreground">{t("browserSettings.chromeUse")}</div>
        <Example text={t("browserSettings.chromeEx1")} />
        <Example text={t("browserSettings.chromeEx2")} />
        <ul className="list-disc space-y-1 pt-1 pl-4 text-muted-foreground">
          <li>{t("browserSettings.projectSelf")}</li>
          <li>{t("browserSettings.chromeFirst")}</li>
        </ul>
      </section>
    </div>
  );
}

/** Something to say to an agent, as said. */
function Example({ text }: { text: string }) {
  return <div className="border-l-2 border-brand/60 bg-muted/60 px-2.5 py-1 text-foreground">{text}</div>;
}

function ProfileCard({ p }: { p: BrowserProfile }) {
  return (
    <div className="space-y-2 rounded-lg border p-3">
      <div className="flex min-w-0 items-center gap-2">
        <span className="size-2 shrink-0 rounded-full bg-ok" />
        <span className="min-w-0 truncate font-medium">{p.name || t("browserSettings.unnamed")}</span>
        <span className="shrink-0 text-[11px] text-ok">{t("browserSettings.connected")}</span>
        <span className="ml-auto shrink-0 text-[11px] text-muted-foreground">{t("browserSettings.version", { version: p.version })}</span>
      </div>
      {!p.name && <div className="text-[12px] text-muted-foreground">{t("browserSettings.unnamedHint")}</div>}
      {p.sites.length ? (
        <>
          <div className="flex flex-wrap gap-1.5">
            {p.sites.map((s) => (
              <span key={s} className="inline-flex min-w-0 items-center gap-1 rounded-md bg-muted px-1.5 py-0.5 font-mono text-[11px]">
                <Globe className="size-3 shrink-0 text-muted-foreground" />
                <span className="truncate">{s.replace(/\/\*$/, "").replace(/^https:\/\//, "")}</span>
              </span>
            ))}
          </div>
          <div className="text-[11px] text-muted-foreground">{t("browserSettings.sitesHint")}</div>
        </>
      ) : (
        <div className="text-[12px] text-muted-foreground">{t("browserSettings.noSites")}</div>
      )}
    </div>
  );
}
