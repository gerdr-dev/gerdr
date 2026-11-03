import { useEffect, useState } from "react";
import { toast } from "sonner";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { api, type AwakeStatus } from "@/lib/api";
import { t } from "@/lib/i18n";
import type { MessageKey } from "@/lib/messages";
import { useStore } from "@/lib/store";

// 设置 → 实验室：手动不让电脑睡一段（和 Amphetamine 的「开启新会话」一个意思）。手机上也能点 ——
// 人在外面想让电脑多醒一会儿，正是用这个的时候。后端在 internal/awake。

const PRESETS: { label: MessageKey; b: { minutes?: number; forever?: boolean } }[] = [
  { label: "awakeSetting.preset.m15", b: { minutes: 15 } },
  { label: "awakeSetting.preset.h1", b: { minutes: 60 } },
  { label: "awakeSetting.preset.h2", b: { minutes: 120 } },
  { label: "awakeSetting.preset.h5", b: { minutes: 300 } },
  { label: "awakeSetting.preset.forever", b: { forever: true } },
];

function clock(ms: number) {
  const d = new Date(ms);
  const same = d.toDateString() === new Date().toDateString();
  return d.toLocaleString(undefined, same ? { hour: "2-digit", minute: "2-digit" } : { month: "numeric", day: "numeric", hour: "2-digit", minute: "2-digit" });
}

// 合盖不睡时心里得有数：现在是插着电还是在耗电池、还剩多少
function power({ percent, onBattery, charging }: AwakeStatus["battery"]) {
  if (percent < 0) return t("awakeSetting.power.mains");
  if (onBattery) return t("awakeSetting.power.battery", { n: percent });
  return t(charging ? "awakeSetting.power.charging" : "awakeSetting.power.mainsPercent", { n: percent });
}

function describe(st: AwakeStatus) {
  if (!st.awake) return t("awakeSetting.notAwake");
  const why: string[] = [];
  if (st.manualUntil === -1) why.push(t("awakeSetting.why.manualForever"));
  else if (st.manualUntil > 0) why.push(t("awakeSetting.why.manualUntil", { time: clock(st.manualUntil) }));
  if (st.auto && st.remote > 0) why.push(t(st.remote === 1 ? "awakeSetting.why.remotesOne" : "awakeSetting.why.remotes", { n: st.remote }));
  else if (st.auto && st.graceUntil > 0) why.push(t("awakeSetting.why.grace", { time: clock(st.graceUntil) }));
  if (st.lidOn) why.push(t("awakeSetting.why.lidOn"));
  else if (st.lowBattery) why.push(t("awakeSetting.why.lowBattery"));
  return t("awakeSetting.awake", { why: why.join(t("awakeSetting.why.sep")) });
}

export function AwakeSetting({ open }: { open: boolean }) {
  const [st, setSt] = useState<AwakeStatus | null>(null);
  const [until, setUntil] = useState("");

  // 开着这一页时每 10 秒刷一次：远程连上 / 断开、时段到点都是后端自己变的
  useEffect(() => {
    if (!open) return;
    let alive = true;
    const load = () =>
      api
        .awake()
        .then((r) => alive && setSt(r))
        .catch(() => {});
    load();
    const timer = setInterval(load, 10_000);
    return () => {
      alive = false;
      clearInterval(timer);
    };
  }, [open]);

  const put = (b: { minutes?: number; until?: number; forever?: boolean }) =>
    api
      .setAwake(b)
      .then(setSt)
      .catch((e) => toast.error(e.message ?? String(e)));
  // Refresh once the remote toggle changes (saved to config, read by the backend): the status line follows it
  const autoCfg = useStore((s) => !!s.config.keepAwakeRemote);
  useEffect(() => {
    if (!open) return;
    const timer = setTimeout(
      () =>
        api
          .awake()
          .then(setSt)
          .catch(() => {}),
      300,
    );
    return () => clearTimeout(timer);
  }, [autoCfg, open]);
  const [authing, setAuthing] = useState(false);
  const setupLid = () => {
    setAuthing(true);
    api
      .setupLid()
      .then((r) => {
        setSt(r);
        toast.success(t("awakeSetting.authed"));
      })
      .catch((e) => toast.error(e.message ?? String(e)))
      .finally(() => setAuthing(false));
  };

  if (!st) return null;
  if (!st.available) return <div className="text-[12px] text-muted-foreground">{t("awakeSetting.unavailable")}</div>;
  const manual = st.manualUntil !== 0;
  return (
    <div className="space-y-2">
      <div>
        <div className="text-[13px]">{t("awakeSetting.title")}</div>
        <div className={st.awake ? "text-[12px] text-ok" : "text-[12px] text-muted-foreground"}>{describe(st)}</div>
        {st.battery && (
          <div
            className={
              st.battery.onBattery && st.battery.percent >= 0 && st.battery.percent <= 20 ? "text-[12px] text-warn" : "text-[12px] text-muted-foreground"
            }
          >
            {t("awakeSetting.now", { power: power(st.battery) })}
          </div>
        )}
      </div>
      {!st.lidReady && (
        // Offered on its own, not only after the remote toggle: a manual stretch needs it for a closed lid too.
        // A remote click gets 403 (the password prompt opens on the computer, someone has to type it): local port only
        <div className="flex items-center gap-2 text-[12px] text-warn">
          {t("awakeSetting.lidAuth")}
          <Button size="sm" variant="outline" className="h-7 text-[12px]" disabled={authing} onClick={setupLid}>
            {authing ? t("awakeSetting.waitingPassword") : t("awakeSetting.authorize")}
          </Button>
        </div>
      )}
      {st.lidReady && st.lidErr && <div className="text-[12px] text-bad">{t("awakeSetting.lidErr", { error: st.lidErr })}</div>}
      <div className="flex flex-wrap gap-1.5">
        {PRESETS.map((p) => (
          <Button key={p.label} size="sm" variant="outline" className="h-7 text-[12px]" onClick={() => put(p.b)}>
            {t(p.label)}
          </Button>
        ))}
        {manual && (
          <Button size="sm" variant="outline" className="h-7 text-[12px] text-bad" onClick={() => put({})}>
            {t("awakeSetting.end")}
          </Button>
        )}
      </div>
      <div className="flex items-center gap-1.5">
        <span className="text-[12px] text-muted-foreground">{t("awakeSetting.until")}</span>
        <Input type="datetime-local" value={until} onChange={(e) => setUntil(e.target.value)} className="h-7 w-auto text-[12px]" />
        <Button
          size="sm"
          variant="outline"
          className="h-7 text-[12px]"
          disabled={!until}
          onClick={() => {
            const ms = new Date(until).getTime();
            if (!(ms > Date.now())) return toast.error(t("awakeSetting.pastTime"));
            put({ until: ms });
          }}
        >
          {t("awakeSetting.start")}
        </Button>
      </div>
    </div>
  );
}
