import { useEffect, useState } from "react";
import { toast } from "sonner";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { api, type AwakeStatus } from "@/lib/api";
import { useStore } from "@/lib/store";

// 设置 → 实验室：手动不让电脑睡一段（和 Amphetamine 的「开启新会话」一个意思）。手机上也能点 ——
// 人在外面想让电脑多醒一会儿，正是用这个的时候。后端在 internal/awake。

const PRESETS: { label: string; b: { minutes?: number; forever?: boolean } }[] = [
  { label: "15 分钟", b: { minutes: 15 } },
  { label: "1 小时", b: { minutes: 60 } },
  { label: "2 小时", b: { minutes: 120 } },
  { label: "5 小时", b: { minutes: 300 } },
  { label: "无限期", b: { forever: true } },
];

function clock(ms: number) {
  const d = new Date(ms);
  const same = d.toDateString() === new Date().toDateString();
  return d.toLocaleString(undefined, same ? { hour: "2-digit", minute: "2-digit" } : { month: "numeric", day: "numeric", hour: "2-digit", minute: "2-digit" });
}

function describe(st: AwakeStatus) {
  if (!st.awake) return "现在没挡着，电脑会照常睡眠。";
  const why: string[] = [];
  if (st.manualUntil === -1) why.push("手动：无限期");
  else if (st.manualUntil > 0) why.push(`手动：到 ${clock(st.manualUntil)}`);
  if (st.auto && st.remote > 0) why.push(`${st.remote} 个远程界面连着`);
  else if (st.auto && st.graceUntil > 0) why.push(`远程刚断开，撑到 ${clock(st.graceUntil)}`);
  if (st.lidOn) why.push("合盖也不睡");
  else if (st.lid && st.lowBattery) why.push("电量低，合盖会睡");
  return `正挡着睡眠（${why.join("；")}）`;
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
    const t = setInterval(load, 10_000);
    return () => {
      alive = false;
      clearInterval(t);
    };
  }, [open]);

  const put = (b: { minutes?: number; until?: number; forever?: boolean }) =>
    api
      .setAwake(b)
      .then(setSt)
      .catch((e) => toast.error(e.message ?? String(e)));
  // 开关一变（存进 config、后端读到）就刷一次：授权按钮要跟着出来
  const lidCfg = useStore((s) => !!s.config.keepAwakeLid);
  useEffect(() => {
    if (!open) return;
    const t = setTimeout(
      () =>
        api
          .awake()
          .then(setSt)
          .catch(() => {}),
      300,
    );
    return () => clearTimeout(t);
  }, [lidCfg, open]);
  const [authing, setAuthing] = useState(false);
  const setupLid = () => {
    setAuthing(true);
    api
      .setupLid()
      .then((r) => {
        setSt(r);
        toast.success("授权好了");
      })
      .catch((e) => toast.error(e.message ?? String(e)))
      .finally(() => setAuthing(false));
  };

  if (!st) return null;
  if (!st.available) return <div className="text-[12px] text-muted-foreground">手动不休眠：这台机器上用不了（只支持 macOS）。</div>;
  const manual = st.manualUntil !== 0;
  return (
    <div className="space-y-2">
      <div>
        <div className="text-[13px]">手动不休眠</div>
        <div className={st.awake ? "text-[12px] text-ok" : "text-[12px] text-muted-foreground"}>{describe(st)}</div>
      </div>
      {st.lid && !st.lidReady && (
        // 远程点会 403（密码框得弹在电脑上、有人输）：后端只在本机口放行
        <div className="flex items-center gap-2 text-[12px] text-warn">
          合上盖子也不睡要授权一次（在电脑上输密码，只放行 pmset disablesleep 这一条命令）
          <Button size="sm" variant="outline" className="h-7 text-[12px]" disabled={authing} onClick={setupLid}>
            {authing ? "等你输密码…" : "授权"}
          </Button>
        </div>
      )}
      {st.lid && st.lidReady && st.lidErr && <div className="text-[12px] text-bad">合盖模式开不起来：{st.lidErr}</div>}
      <div className="flex flex-wrap gap-1.5">
        {PRESETS.map((p) => (
          <Button key={p.label} size="sm" variant="outline" className="h-7 text-[12px]" onClick={() => put(p.b)}>
            {p.label}
          </Button>
        ))}
        {manual && (
          <Button size="sm" variant="outline" className="h-7 text-[12px] text-bad" onClick={() => put({})}>
            结束
          </Button>
        )}
      </div>
      <div className="flex items-center gap-1.5">
        <span className="text-[12px] text-muted-foreground">直到</span>
        <Input type="datetime-local" value={until} onChange={(e) => setUntil(e.target.value)} className="h-7 w-auto text-[12px]" />
        <Button
          size="sm"
          variant="outline"
          className="h-7 text-[12px]"
          disabled={!until}
          onClick={() => {
            const ms = new Date(until).getTime();
            if (!(ms > Date.now())) return toast.error("要选一个以后的时间");
            put({ until: ms });
          }}
        >
          开始
        </Button>
      </div>
    </div>
  );
}
