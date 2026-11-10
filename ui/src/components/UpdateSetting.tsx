import { Info, Loader2, SquareTerminal, X } from "lucide-react";
import { useEffect, useRef, useState } from "react";
import { toast } from "sonner";
import { api } from "@/lib/api";
import { Button } from "@/components/ui/button";
import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover";
import { cn } from "@/lib/utils";
import { Switch } from "@/components/ui/switch";
import { isNative, nativeApplyUpdate, nativeAskUpdate, nativeSetAutoUpdate, restartEffect, type AppUpdateStatus } from "@/lib/native";
import { set, useStore } from "@/lib/store";
import { locale, t } from "@/lib/i18n";
import type { MessageKey } from "@/lib/messages";
import { allLeaves, type Layout } from "@/lib/layout";
import { focusPane } from "@/lib/actions";
import { AgentIcon } from "@/components/AgentIcon";
import type { SessionSummary, TermInfo } from "@/lib/types";

// Settings → 通用 →「版本」: which gerdr is running and where the App's self-update is (desktop/updater.js, pushed
// by desktop/main.js updateStatus): downloading, waiting for agents to finish, upgrading. A downloaded backend
// update is applied by the App only when nothing would be cut off (docs/dev/UPDATE.md), so "waiting" says on what.
// Off the App (a phone over remote access, a browser) there's no shell: the App's own page relays its status and the
// buttons through the backend (server/appupdate.go), which works while the App has a window open on the computer.

export function UpdateSetting() {
  const version = useStore((s) => s.version);
  const u = useStore((s) => s.appUpdate);
  const [service, setService] = useState<boolean | null>(null); // the background service; null = not known (yet)

  useEffect(() => {
    if (!isNative()) {
      api
        .appUpdate()
        .then((r) => set({ appUpdate: r.status }))
        .catch(() => {});
      return;
    }
    nativeAskUpdate();
    api
      .service()
      .then((st) => setService(st.installed))
      .catch(() => {});
  }, []);

  const installedAt = useStore((s) => s.installedAt);
  const running = u?.running || version;
  // When it was installed, not just which commit: a dirty-tree build keeps the same version string for days
  const when = installedAt
    ? t("updateSetting.installedAt", {
        time: new Date(installedAt).toLocaleString(locale() === "zh" ? "zh-CN" : "en", {
          month: "numeric",
          day: "numeric",
          hour: "2-digit",
          minute: "2-digit",
        }),
        since: since(installedAt),
      })
    : "";
  const shown = (u && u.app !== running ? `Gerdr ${running} · App ${u.app}` : `Gerdr ${running}`) + when;
  const { text, tone } = u ? describe(u, service) : { text: "", tone: "" };
  const p = u?.pending;
  const layout = useStore((s) => s.layout);
  const sessions = useStore((s) => s.sessions);
  const [terms, setTerms] = useState<TermInfo[]>([]);
  useEffect(() => {
    if (!p || !u?.busy?.terms) return void setTerms([]);
    api
      .terms()
      .then(setTerms)
      .catch(() => {});
  }, [p?.version, u?.busy?.terms]);
  const native = isNative();
  const checking = useChecking(u);
  const ask = () => {
    checking.start();
    if (native) nativeAskUpdate(true);
    else relay("check").catch(checking.stop);
  };
  const apply = native ? nativeApplyUpdate : () => relay("apply").catch(() => {});
  const setAuto = native ? nativeSetAutoUpdate : (on: boolean) => relay("auto", on).catch(() => {});

  return (
    <>
      <div className="flex items-center justify-between gap-4">
        <div className="min-w-0">
          <div className="text-[13px]">{t("updateSetting.version")}</div>
          <div className="text-[12px] text-muted-foreground">{shown}</div>
          {text && (
            <div className={cn("text-[12px]", tone || "text-muted-foreground")}>
              {text}
              {p && u?.busy && u.busy.turns + u.busy.waiting + u.busy.tasks + u.busy.terms > 0 && (
                <UpdateBlockers layout={layout} sessions={sessions} terms={terms} onKilled={(id) => setTerms((ts) => ts.filter((x) => x.id !== id))} />
              )}
            </div>
          )}
          {!u && !native && <div className="text-[12px] text-muted-foreground">{t("updateSetting.remoteHint")}</div>}
        </div>
        {u?.enabled &&
          (p ? (
            <Button size="sm" disabled={u.phase === "applying"} onClick={apply}>
              {p.kind === "app" ? t("native.restartToUpdate") : t("systemUpdate.updateNow")}
            </Button>
          ) : (
            <Button size="sm" variant="outline" disabled={u.phase !== "idle" || checking.on} onClick={ask}>
              {(u.phase === "checking" || checking.on) && <Loader2 className="size-3.5 animate-spin" />}
              {t("systemUpdate.check")}
            </Button>
          ))}
      </div>
      {/* Only from a shell that knows the switch (u.auto present): an older one would ignore it */}
      {u?.enabled && typeof u.auto === "boolean" && (
        <div className="flex items-center justify-between gap-4">
          <div>
            <div className="text-[13px]">{t("updateSetting.auto")}</div>
            <div className="text-[12px] text-muted-foreground">{u.auto ? t("updateSetting.autoOn") : t("updateSetting.autoOff")}</div>
          </div>
          <Switch checked={u.auto} onCheckedChange={setAuto} />
        </div>
      )}
    </>
  );
}

/** The update status only has counts; the shared layout gives those counts useful, clickable identities. */
function UpdateBlockers({
  layout,
  sessions,
  terms,
  onKilled,
}: {
  layout: Layout;
  sessions: Record<string, SessionSummary>;
  terms: TermInfo[];
  onKilled: (id: string) => void;
}) {
  const [open, setOpen] = useState(false);
  const leaves = allLeaves(layout);
  // One row per agent, whatever it blocks with (its turn, a question, background tasks it started), found
  // through the session list rather than the layout so an agent whose tab was closed still shows up.
  const panels = Object.values(sessions)
    .map((session) => {
      const tasks = (session.tasks ?? []).filter((task) => task.status === "running").length;
      const parts: string[] = [];
      if (session.status === "running") parts.push(t("updateSetting.detailRunning"));
      if (session.status === "waiting") parts.push(t("updateSetting.detailWaiting"));
      if (tasks) parts.push(plural("updateSetting.detailTasks", tasks));
      if (!parts.length) return null;
      const located = leaves.find(({ pane }) => pane.sessionId === session.id);
      return {
        pane: located?.pane,
        termId: undefined as string | undefined,
        icon: <AgentIcon agent={session.agent} className="size-3.5" />,
        title: session.title || t("updateSetting.detailUntitled"),
        detail: parts.join(" · "),
        place: located ? placeName(located.ws.name, located.tab.name) : t("updateSetting.detailNotShown"),
      };
    })
    .filter((panel): panel is NonNullable<typeof panel> => !!panel);
  const terminalPanels = terms
    .filter((term) => !term.exited)
    .map((term) => {
      const located = leaves.find(({ pane }) => pane.term?.id === term.id);
      return {
        pane: located?.pane,
        // A terminal no pane shows (its tab was closed without it) can't be reached to exit it, so it is closed here.
        termId: located ? undefined : term.id,
        icon: <SquareTerminal className="size-3.5" />,
        title: t("updateSetting.detailTerminal"),
        detail: term.cwd,
        place: located ? placeName(located.ws.name, located.tab.name) : t("updateSetting.detailNotShown"),
      };
    });
  const blockers = [...panels, ...terminalPanels];

  if (!blockers.length) return null;
  return (
    <Popover open={open} onOpenChange={setOpen}>
      <PopoverTrigger asChild>
        <button className="ml-1 inline-flex align-[-2px] text-current/70 hover:text-current" aria-label={t("updateSetting.detail")}>
          <Info className="size-3.5" />
        </button>
      </PopoverTrigger>
      <PopoverContent align="start" className="w-80 p-2">
        <div className="px-2 py-1 text-[12px] font-medium">{t("updateSetting.detailTitle")}</div>
        <div className="max-h-64 space-y-0.5 overflow-y-auto">
          {blockers.map((panel, index) => (
            <div key={panel.pane?.id ?? panel.termId ?? `blocker-${index}`} className="flex items-center gap-1">
              <button
                className="flex min-w-0 flex-1 items-center gap-2 rounded-md px-2 py-1.5 text-left text-[12px] hover:bg-accent disabled:cursor-default disabled:hover:bg-transparent"
                disabled={!panel.pane}
                onClick={() => {
                  setOpen(false);
                  if (panel.pane) focusPane(panel.pane.id);
                }}
              >
                <span className="shrink-0 text-muted-foreground">{panel.icon}</span>
                <span className="min-w-0 flex-1">
                  <span className="block truncate">{panel.title}</span>
                  <span className="block truncate text-[11px] text-muted-foreground">
                    {panel.detail} · {panel.place}
                  </span>
                </span>
              </button>
              {panel.termId && (
                <button
                  className="shrink-0 rounded-md p-1.5 text-muted-foreground hover:bg-accent hover:text-foreground"
                  aria-label={t("updateSetting.detailClose")}
                  title={t("updateSetting.detailClose")}
                  onClick={() => {
                    const id = panel.termId!;
                    api
                      .termKill(id)
                      .then(() => onKilled(id))
                      .catch((e) => toast.error(String(e?.message ?? e)));
                  }}
                >
                  <X className="size-3.5" />
                </button>
              )}
            </div>
          ))}
        </div>
      </PopoverContent>
    </Popover>
  );
}

const placeName = (workspace: string, tab?: string) => (tab ? `${workspace} / ${tab}` : workspace);

/** Off the App: the backend hands it to the App's page, whose next status report shows what happened. */
function relay(op: "check" | "apply" | "auto", on?: boolean) {
  return api.appUpdateOp(op, on).catch((e) => {
    toast.error((e as Error).message);
    throw e;
  });
}

/**
 * "检查更新" spins from the click until that check's answer is in: the shell's "checking" phase alone lasts a few
 * hundred ms, and off the App it comes through a relay, so it would mostly never show. Done = checkedAt or the error
 * moved, a download started, or it went through "checking" and back (a repeat of the same failure moves neither);
 * at least 600ms so it doesn't just flicker, at most 30s in case the answer is lost.
 */
function useChecking(u: AppUpdateStatus | null) {
  const [on, setOn] = useState(false);
  const from = useRef({ at: 0, checkedAt: 0, error: "", saw: false });
  const stop = () => setOn(false);
  useEffect(() => {
    if (!on || !u) return;
    const f = from.current;
    if (u.phase === "checking") f.saw = true;
    const done = u.checkedAt !== f.checkedAt || u.error !== f.error || u.phase === "downloading" || !!u.pending || (f.saw && u.phase === "idle");
    if (!done) return;
    const t = setTimeout(stop, Math.max(0, f.at + 600 - Date.now()));
    return () => clearTimeout(t);
  }, [on, u]);
  useEffect(() => {
    if (!on) return;
    const t = setTimeout(stop, 30_000);
    return () => clearTimeout(t);
  }, [on]);
  return {
    on,
    stop,
    start: () => {
      from.current = { at: Date.now(), checkedAt: u?.checkedAt ?? 0, error: u?.error ?? "", saw: false };
      setOn(true);
    },
  };
}

function describe(u: AppUpdateStatus, service: boolean | null): { text: string; tone: string } {
  const p = u.pending;
  if (u.phase === "applying" && p) return { text: t("updateSetting.applying", { version: p.version }), tone: "text-brand" };
  if (u.phase === "downloading") return { text: t("systemUpdate.downloading", { name: u.downloading, progress: u.progress }), tone: "" };
  if (u.phase === "checking") return { text: t("systemUpdate.checking"), tone: "" };
  if (p?.kind === "app") return { text: t("updateSetting.pendingApp", { version: p.version, effect: restartEffect(u, service) }), tone: "text-brand" };
  if (p) {
    const b = u.busy;
    const who = service ? t("updateSetting.whoService") : t("updateSetting.whoApp");
    if (!b) return { text: t("updateSetting.pending", { version: p.version }), tone: "text-brand" };
    const wait: string[] = [];
    if (b.turns) wait.push(plural("updateSetting.waitTurns", b.turns));
    if (b.waiting) wait.push(plural("updateSetting.waitRequests", b.waiting));
    if (b.tasks) wait.push(plural("updateSetting.waitTasks", b.tasks));
    if (b.terms) wait.push(plural("updateSetting.waitTerms", b.terms));
    if (u.auto === false) return { text: t("updateSetting.pendingManual", { version: p.version }), tone: "text-brand" };
    if (!wait.length) return { text: t("updateSetting.pendingSoon", { version: p.version }), tone: "text-brand" };
    return { text: t("updateSetting.waiting", { who, version: p.version, list: wait.join(t("updateSetting.listSep")) }), tone: "text-warn" };
  }
  if (u.error) return { text: t("systemUpdate.checkFailed", { error: u.error }), tone: "text-bad" };
  if (!u.enabled) return { text: t("updateSetting.noAuto"), tone: "" };
  if (!u.checkedAt) return { text: "", tone: "" };
  const at = new Date(u.checkedAt).toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" });
  return { text: t("systemUpdate.upToDate", { time: at }), tone: "" };
}

/** key.one / key.other by n (English needs both; zh has the same text in each). */
const plural = (key: string, n: number) => t(`${key}.${n === 1 ? "one" : "other"}` as MessageKey, { n });

function since(at: number) {
  const m = Math.max(0, Math.floor((Date.now() - at) / 60000));
  if (m < 1) return t("updateSetting.justNow");
  if (m < 60) return t("updateSetting.minutesAgo", { n: m });
  if (m < 1440) return t("updateSetting.hoursAgo", { n: Math.floor(m / 60) });
  return t("updateSetting.daysAgo", { n: Math.floor(m / 1440) });
}
