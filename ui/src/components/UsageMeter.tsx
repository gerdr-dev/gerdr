import { RefreshCw } from "lucide-react";
import { useCallback, useEffect, useRef, useState } from "react";
import { api, type UsageInfo, type UsageLimit } from "@/lib/api";
import { cn } from "@/lib/utils";

/*
 * 侧栏底部的用量额度：claude / codex 各一行，常驻看得见，不用发 /usage（那会进对话记录）。
 * 数据是结构化的（后端 /api/usage → 适配器的 _gerdr/usage：claude 的 get_usage、codex 的 account/rateLimits/read）。
 *
 * 收起时每家只画最要紧的两条（5 小时 + 本周；按模型的周额度是 0% 时不占地方），点开看全部和重置时间。
 * 几分钟问一次；后端也缓存，多个窗口一起问只真问一次。
 */

const POLL = 3 * 60_000;

function useUsage(enabled = true) {
  const [list, setList] = useState<UsageInfo[] | null>(null);
  const [loading, setLoading] = useState(false);
  const load = useCallback(async (force?: boolean) => {
    setLoading(true);
    try {
      setList(await api.usage(force));
    } catch {
      /* 拿不到就留着上一次的 */
    } finally {
      setLoading(false);
    }
  }, []);
  useEffect(() => {
    if (!enabled) return;
    load();
    const t = setInterval(() => document.visibilityState === "visible" && load(), POLL);
    const onVis = () => document.visibilityState === "visible" && load();
    document.addEventListener("visibilitychange", onVis);
    return () => {
      clearInterval(t);
      document.removeEventListener("visibilitychange", onVis);
    };
  }, [load, enabled]);
  return { list, loading, refresh: () => load(true) };
}

function tone(pct: number) {
  return pct >= 90 ? "bg-bad" : pct >= 70 ? "bg-warn" : "bg-brand";
}

function Bar({ pct, className }: { pct: number; className?: string }) {
  return (
    <div className={cn("h-1 overflow-hidden rounded-full bg-muted", className)}>
      <div className={cn("h-full rounded-full", tone(pct))} style={{ width: `${Math.min(100, Math.max(pct > 0 ? 3 : 0, pct))}%` }} />
    </div>
  );
}

// 「5 小时」→「5h」、「本周」→「周」：收起时一行要放两条
function short(label: string) {
  return label
    .replace(/^(\d+) 小时$/, "$1h")
    .replace(/^本周$/, "周")
    .replace(/^本周 · /, "周·");
}

function fmtReset(ms: number, now: number) {
  const d = new Date(ms);
  const when = `${d.getMonth() + 1}/${d.getDate()} ${String(d.getHours()).padStart(2, "0")}:${String(d.getMinutes()).padStart(2, "0")}`;
  const left = ms - now;
  if (left <= 0) return `${when} 重置`;
  const mins = Math.round(left / 60_000);
  const rest = mins < 60 ? `${Math.max(1, mins)}m` : mins < 48 * 60 ? `${Math.round(mins / 60)}h` : `${Math.round(mins / 1440)} 天`;
  return `${when} 重置 · 还剩 ${rest}`;
}

// 收起时挑哪两条：先 5 小时那种短窗口、再本周；0% 的按模型额度往后放
function pick(limits: UsageLimit[]) {
  const rank = (l: UsageLimit) => (l.percent === 0 && /·/.test(l.label) ? 2 : 0) + ((l.windowMins ?? 0) > 24 * 60 ? 1 : 0);
  return [...limits].sort((a, b) => rank(a) - rank(b)).slice(0, 2);
}

export function UsageMeter() {
  const { list, loading, refresh } = useUsage();
  const [open, setOpen] = useState(false);
  const shown = (list ?? []).filter((u) => u.data?.limits.length || u.error);
  if (!shown.length) return null;
  const now = Date.now();
  return (
    <div className="shrink-0 border-t px-1.5 py-1">
      <button
        type="button"
        className="w-full rounded-md px-2 py-1 text-left hover:bg-accent/60"
        title={open ? "收起" : "看全部额度和重置时间"}
        onClick={() => setOpen((v) => !v)}
      >
        {shown.map((u) => {
          const limits = u.data?.limits ?? [];
          const name = u.agent === "claude" ? "Claude" : u.agent === "codex" ? "Codex" : u.name;
          return (
            <div key={u.agent} className="py-0.5">
              {open ? (
                <>
                  <div className="flex items-baseline gap-1.5 text-[11px]">
                    <span className="font-medium">{name}</span>
                    {u.data?.plan && <span className="text-muted-foreground">{u.data.plan}</span>}
                    {u.error && (
                      <span className="ml-auto truncate text-warn" title={u.error}>
                        {u.data ? "没刷新上" : "拿不到"}
                      </span>
                    )}
                  </div>
                  {limits.map((l) => (
                    <div key={l.kind + l.label} className="mt-1">
                      <div className="flex items-baseline gap-2 text-[11px]">
                        <span className="min-w-0 flex-1 truncate text-muted-foreground">{l.label}</span>
                        <span className="tabular-nums">{Math.round(l.percent)}%</span>
                      </div>
                      <Bar pct={l.percent} className="mt-0.5" />
                      {!!l.resetsAt && <div className="mt-0.5 text-[10px] text-muted-foreground tabular-nums">{fmtReset(l.resetsAt, now)}</div>}
                    </div>
                  ))}
                </>
              ) : (
                <div className="flex items-center gap-2 text-[11px]">
                  <span className="w-11 shrink-0 truncate text-muted-foreground">{name}</span>
                  {limits.length === 0 && <span className="truncate text-warn">拿不到</span>}
                  {pick(limits).map((l) => (
                    <span key={l.kind + l.label} className="flex min-w-0 flex-1 items-center gap-1" title={`${l.label} ${Math.round(l.percent)}%`}>
                      <span className="shrink-0 text-muted-foreground">{short(l.label)}</span>
                      <Bar pct={l.percent} className="min-w-3 flex-1" />
                      <span className="shrink-0 tabular-nums">{Math.round(l.percent)}%</span>
                    </span>
                  ))}
                </div>
              )}
            </div>
          );
        })}
      </button>
      {open && (
        <div className="flex items-center px-2 pb-1 text-[10px] text-muted-foreground">
          <span className="flex-1 tabular-nums">
            {shown[0]?.at ? `${new Date(shown[0].at).toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" })} 更新` : ""}
          </span>
          <button type="button" className="flex items-center gap-1 rounded-sm px-1 hover:text-foreground" onClick={refresh} disabled={loading}>
            <RefreshCw className={cn("size-3", loading && "animate-spin")} /> 刷新
          </button>
        </div>
      )}
    </div>
  );
}

/*
 * 输入框里的 /usage：gerdr 自己接，不发给 agent —— claude 的 /usage 会起一轮、进对话记录，codex 的 app-server 根本没有这个命令。
 * 画成输入框上面的一小块浮层（和斜杠命令列表同一个位置），不用弹窗：看一眼额度不该挡住整个界面。点外面 / Esc 收起。
 * 数据和侧栏那份一样（/api/usage），只画这个会话的 agent 那一家；一打开就强刷一次（hub 里仍有最小间隔，不会真连着问）。
 */
export function UsagePanel({ agent, onClose }: { agent: string; onClose: () => void }) {
  // 不轮询：开着才要，打开时强刷一次就够
  const { list, loading, refresh } = useUsage(false);
  const ref = useRef<HTMLDivElement>(null);
  useEffect(() => {
    refresh();
    const onDown = (e: PointerEvent) => !ref.current?.contains(e.target as Node) && onClose();
    // Esc 只收浮层：截在 document 的捕获阶段，不让它再去打断 agent（输入框里 Esc 是打断）
    const onKey = (e: KeyboardEvent) => {
      if (e.key !== "Escape") return;
      e.stopPropagation();
      e.preventDefault();
      onClose();
    };
    document.addEventListener("pointerdown", onDown, true);
    document.addEventListener("keydown", onKey, true);
    return () => {
      document.removeEventListener("pointerdown", onDown, true);
      document.removeEventListener("keydown", onKey, true);
    };
  }, []);
  const u = list?.find((x) => x.agent === agent);
  const limits = u?.data?.limits ?? [];
  const name = agent === "claude" ? "Claude Code" : agent === "codex" ? "Codex" : agent;
  const now = Date.now();
  return (
    <div
      ref={ref}
      className="absolute bottom-full left-3 z-20 mb-1 w-72 max-w-[calc(100%-1.5rem)] rounded-lg border bg-popover px-3 py-2 text-[12px] shadow-lg"
    >
      <div className="mb-2 flex items-center gap-1.5">
        <span className="font-medium">{name}</span>
        {u?.data?.plan && <span className="text-muted-foreground">{u.data.plan}</span>}
        <span className="flex-1" />
        <button
          type="button"
          className="flex items-center gap-1 rounded-sm px-1 text-[11px] text-muted-foreground hover:text-foreground"
          onClick={refresh}
          disabled={loading}
          title={u?.at ? `${new Date(u.at).toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" })} 更新` : "刷新"}
        >
          <RefreshCw className={cn("size-3", loading && "animate-spin")} />
        </button>
      </div>
      {!list ? (
        <div className="py-1 text-muted-foreground">读取中…</div>
      ) : !u || (!limits.length && !u.error) ? (
        // API key / Bedrock 之类没有套餐额度；没装这个 agent 的 CLI 时 /api/usage 里也没有它
        <div className="py-1 text-muted-foreground">这个账号没有套餐额度可看</div>
      ) : (
        <div className="space-y-2">
          {u.error && <div className="text-warn">{u.data ? "没刷新上，下面是上一次的" : `拿不到：${u.error}`}</div>}
          {limits.map((l) => (
            <div key={l.kind + l.label}>
              <div className="mb-1 flex items-baseline gap-2">
                <span className="min-w-0 flex-1 truncate">{l.label}</span>
                <span className="tabular-nums">{Math.round(l.percent)}%</span>
              </div>
              <Bar pct={l.percent} />
              {!!l.resetsAt && <div className="mt-0.5 text-[11px] text-muted-foreground tabular-nums">{fmtReset(l.resetsAt, now)}</div>}
            </div>
          ))}
        </div>
      )}
    </div>
  );
}
